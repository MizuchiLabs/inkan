package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mizuchilabs/nushi"

	"github.com/mizuchilabs/inkan/internal/db"
)

const (
	sessionPath   = "/api/session"
	sessionCookie = "inkan_session"
	testPassword  = "correct horse battery staple"
)

func guardFor(t *testing.T, c nushi.Config) *nushi.Guard {
	t.Helper()
	guard, err := nushi.New("inkan", c)
	require.NoError(t, err)
	return guard
}

func protectedServer(t *testing.T, q *db.Queries) *Server {
	t.Helper()
	return New(q, guardFor(t, nushi.Config{Password: testPassword}))
}

// do sends one request straight to the handler, with a session cookie if given.
func do(t *testing.T, srv *Server, method, path, body, session string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	// httptest asks for example.com, a server without a sign in only answers to this machine.
	req.Host = "localhost:3000"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if session != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func signIn(t *testing.T, srv *Server, password string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, srv, http.MethodPost, sessionPath, `{"password": "`+password+`"}`, "")
}

func TestOpenWithoutPassword(t *testing.T) {
	t.Parallel()
	q, closeDB, err := db.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(closeDB)
	srv := New(q, guardFor(t, nushi.Config{}))

	assert.Equal(t, http.StatusOK, do(t, srv, http.MethodGet, "/api/licenses", "", "").Code)
	assert.JSONEq(t, `{"mode": "none", "signed_in": true}`,
		do(t, srv, http.MethodGet, sessionPath, "", "").Body.String())
	assert.Equal(t, http.StatusMethodNotAllowed, signIn(t, srv, "anything").Code, "there is nothing to sign in to")
}

func TestSignIn(t *testing.T) {
	t.Parallel()
	q, closeDB, err := db.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(closeDB)
	srv := protectedServer(t, q)

	assert.Equal(t, http.StatusUnauthorized, do(t, srv, http.MethodGet, "/api/licenses", "", "").Code)
	assert.Equal(t, http.StatusUnauthorized,
		do(t, srv, http.MethodPost, "/api/check", `{"key": "APP-AAAAA"}`, "").Code, "every route is closed")
	assert.Equal(t, http.StatusUnauthorized, do(t, srv, http.MethodGet, "/api/docs", "", "").Code, "the docs too")
	assert.Equal(
		t,
		http.StatusUnauthorized,
		do(t, srv, http.MethodGet, "/api/openapi.json", "", "").Code,
		"and the spec",
	)
	assert.Equal(t, http.StatusOK, do(t, srv, http.MethodGet, "/healthz", "", "").Code, "but the health check")
	assert.JSONEq(t, `{"mode": "password", "signed_in": false}`,
		do(t, srv, http.MethodGet, sessionPath, "", "").Body.String())

	wrong := signIn(t, srv, "wrong")
	assert.Equal(t, http.StatusUnauthorized, wrong.Code)
	assert.Empty(t, wrong.Result().Cookies(), "a wrong password gets no cookie")

	right := signIn(t, srv, testPassword)
	require.Equal(t, http.StatusNoContent, right.Code)
	require.Len(t, right.Result().Cookies(), 1)
	cookie := right.Result().Cookies()[0]

	assert.Equal(t, http.StatusOK, do(t, srv, http.MethodGet, "/api/licenses", "", cookie.Value).Code)
	assert.Equal(t, http.StatusOK, do(t, srv, http.MethodGet, "/api/docs", "", cookie.Value).Code)
	assert.JSONEq(t, `{"mode": "password", "signed_in": true}`,
		do(t, srv, http.MethodGet, sessionPath, "", cookie.Value).Body.String())

	out := do(t, srv, http.MethodDelete, sessionPath, "", cookie.Value)
	require.Equal(t, http.StatusNoContent, out.Code)
	require.Len(t, out.Result().Cookies(), 1)
	assert.Negative(t, out.Result().Cookies()[0].MaxAge, "signing out deletes the cookie")
}

func TestNoSignInStaysOnThisMachine(t *testing.T) {
	t.Parallel()
	require.ErrorContains(t, New(nil, guardFor(t, nushi.Config{})).Start(t.Context(), ":0"), "INKAN_PASSWORD")
}

// A site that points its own name at this machine is the same origin to the
// browser, so the cross origin check lets it through.
func TestNoSignInTakesOnlyLocalHosts(t *testing.T) {
	t.Parallel()
	ask := func(c nushi.Config, host string) int {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		New(nil, guardFor(t, c)).Handler().ServeHTTP(rec, req)
		return rec.Code
	}

	for host, want := range map[string]int{
		"localhost:3000":     http.StatusOK,
		"127.0.0.1:3000":     http.StatusOK,
		"[::1]:3000":         http.StatusOK,
		"localhost":          http.StatusOK,
		"evil.example:3000":  http.StatusForbidden,
		"evil.example":       http.StatusForbidden,
		"localhost.evil.com": http.StatusForbidden,
		"":                   http.StatusForbidden,
	} {
		assert.Equal(t, want, ask(nushi.Config{}, host), host)
	}
	assert.Equal(t, http.StatusOK, ask(nushi.Config{Password: testPassword}, "inkan.example.com"),
		"a sign in opens it to any name")
}

func TestAPIToken(t *testing.T) {
	t.Parallel()
	q, closeDB, err := db.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(closeDB)
	const token = "0123456789abcdef0123456789abcdef"
	srv := New(q, guardFor(t, nushi.Config{Password: testPassword, APITokens: []string{token}}))

	get := func(srv *Server, header string) int {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/licenses", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec.Code
	}
	assert.Equal(t, http.StatusOK, get(srv, "Bearer "+token), "a script gets in without a session")
	assert.Equal(t, http.StatusUnauthorized, get(srv, "Bearer "+token+"x"))
	assert.Equal(t, http.StatusUnauthorized, get(srv, ""))
	assert.Equal(t, http.StatusUnauthorized, get(protectedServer(t, q), "Bearer "),
		"without a token no bearer gets in, not even an empty one")
}
