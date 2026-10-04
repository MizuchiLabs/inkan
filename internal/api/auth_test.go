package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mizuchilabs/inkan/internal/db"
)

const testPassword = "correct horse battery staple"

func protectedServer(t *testing.T, q *db.Queries) *Server {
	t.Helper()
	srv, err := New(q, testPassword, "")
	require.NoError(t, err)
	return srv
}

// do sends one request straight to the handler, with a session cookie if given.
func do(t *testing.T, srv *Server, method, path, body, session string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
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
	q, err := db.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	srv, err := New(q, "", "")
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, do(t, srv, http.MethodGet, "/api/licenses", "", "").Code)
	assert.JSONEq(t, `{"protected": false, "signed_in": true}`,
		do(t, srv, http.MethodGet, sessionPath, "", "").Body.String())
	assert.Equal(t, http.StatusUnauthorized, signIn(t, srv, "anything").Code, "there is nothing to sign in to")
}

func TestSignIn(t *testing.T) {
	t.Parallel()
	q, err := db.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	srv := protectedServer(t, q)

	assert.Equal(t, http.StatusUnauthorized, do(t, srv, http.MethodGet, "/api/licenses", "", "").Code)
	assert.Equal(t, http.StatusUnauthorized,
		do(t, srv, http.MethodPost, "/api/check", `{"key": "APP-AAAAA"}`, "").Code, "every route is closed")
	assert.Equal(t, http.StatusOK, do(t, srv, http.MethodGet, "/healthz", "", "").Code, "but the health check")
	assert.JSONEq(t, `{"protected": true, "signed_in": false}`,
		do(t, srv, http.MethodGet, sessionPath, "", "").Body.String())

	wrong := signIn(t, srv, "wrong")
	assert.Equal(t, http.StatusUnauthorized, wrong.Code)
	assert.Empty(t, wrong.Result().Cookies(), "a wrong password gets no cookie")

	right := signIn(t, srv, testPassword)
	require.Equal(t, http.StatusNoContent, right.Code)
	require.Len(t, right.Result().Cookies(), 1)
	cookie := right.Result().Cookies()[0]
	assert.True(t, cookie.HttpOnly, "scripts must not read the session")
	assert.Equal(t, http.SameSiteStrictMode, cookie.SameSite)
	assert.Equal(t, "/", cookie.Path)
	assert.Equal(t, int(sessionTTL.Seconds()), cookie.MaxAge)
	assert.False(t, cookie.Secure, "plain HTTP has to keep working on a private network")

	assert.Equal(t, http.StatusOK, do(t, srv, http.MethodGet, "/api/licenses", "", cookie.Value).Code)
	assert.JSONEq(t, `{"protected": true, "signed_in": true}`,
		do(t, srv, http.MethodGet, sessionPath, "", cookie.Value).Body.String())

	// Behind a proxy that ends TLS the cookie is kept off plain HTTP.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, sessionPath,
		strings.NewReader(`{"password": "`+testPassword+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Len(t, rec.Result().Cookies(), 1)
	assert.True(t, rec.Result().Cookies()[0].Secure)

	out := do(t, srv, http.MethodDelete, sessionPath, "", cookie.Value)
	require.Equal(t, http.StatusNoContent, out.Code)
	require.Len(t, out.Result().Cookies(), 1)
	assert.Negative(t, out.Result().Cookies()[0].MaxAge, "signing out deletes the cookie")
}

func TestForgedSessions(t *testing.T) {
	t.Parallel()
	q, err := db.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	srv := protectedServer(t, q)
	other, err := newAuth("another password", "")
	require.NoError(t, err)

	inAnHour := time.Now().Add(time.Hour).Unix()
	good := srv.auth.sign(inAnHour)
	stamp, mac, _ := strings.Cut(good, ".")
	require.Equal(t, http.StatusOK, do(t, srv, http.MethodGet, "/api/licenses", "", good).Code)

	for name, session := range map[string]string{
		"expired":                   srv.auth.sign(time.Now().Add(-time.Second).Unix()),
		"signed with another key":   other.sign(inAnHour),
		"a later expiry, old mac":   "9999999999." + mac,
		"the same expiry, bent mac": stamp + "." + strings.ToUpper(mac),
		"no mac":                    stamp,
		"not a session at all":      "hello",
	} {
		assert.Equal(t, http.StatusUnauthorized,
			do(t, srv, http.MethodGet, "/api/licenses", "", session).Code, name)
	}
}

func TestSignInBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := protectedServer(t, nil)
		for range loginAttempts {
			require.Equal(t, http.StatusUnauthorized, signIn(t, srv, "guess").Code)
		}
		assert.Equal(t, http.StatusTooManyRequests, signIn(t, srv, testPassword).Code,
			"once the budget is spent even the right password has to wait")

		time.Sleep(loginWindow)
		assert.Equal(t, http.StatusNoContent, signIn(t, srv, testPassword).Code)
	})
}

func TestNoPasswordStaysOnThisMachine(t *testing.T) {
	t.Parallel()
	for addr, want := range map[string]bool{
		"127.0.0.1:3000":   true,
		"localhost:3000":   true,
		"[::1]:3000":       true,
		":3000":            false,
		"0.0.0.0:3000":     false,
		"[::]:3000":        false,
		"192.168.1.5:3000": false,
		"example.com:3000": false,
		"3000":             false,
	} {
		assert.Equal(t, want, loopback(addr), addr)
	}

	open, err := New(nil, "", "")
	require.NoError(t, err)
	require.ErrorContains(t, open.Start(t.Context(), ":0"), "INKAN_PASSWORD")
}

func TestAPIToken(t *testing.T) {
	t.Parallel()
	q, err := db.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	const token = "0123456789abcdef0123456789abcdef"
	srv, err := New(q, testPassword, token)
	require.NoError(t, err)

	get := func(header string) int {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/licenses", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec.Code
	}
	assert.Equal(t, http.StatusOK, get("Bearer "+token), "a script gets in without a session")
	assert.Equal(t, http.StatusUnauthorized, get("Bearer "+token+"x"))
	assert.Equal(t, http.StatusUnauthorized, get("Bearer "))
	assert.Equal(t, http.StatusUnauthorized, get(""))

	noToken := protectedServer(t, q)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/licenses", nil)
	req.Header.Set("Authorization", "Bearer ")
	rec := httptest.NewRecorder()
	noToken.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "without a token no bearer gets in, not even an empty one")

	_, err = New(q, testPassword, "short")
	require.Error(t, err, "a token is guessed without a limit, so a short one is refused")
}
