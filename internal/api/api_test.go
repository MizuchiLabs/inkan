package api

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/mizuchilabs/inkan/license"

	"github.com/mizuchilabs/inkan/internal/db"
)

func TestSpecBuilds(t *testing.T) {
	t.Parallel()

	spec := Spec()
	require.NotNil(t, spec.Paths["/api/products"], "registering every route must not panic")
	require.NotNil(t, spec.Paths["/api/products/{product_id}/licenses/{id}"])
	require.NotNil(t, spec.Paths["/api/check"])
}

func newTestServer(t *testing.T) (*httptest.Server, *db.Queries) {
	t.Helper()
	q, err := db.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	api, err := New(q, "")
	require.NoError(t, err)
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)
	return srv, q
}

// call sends a JSON request and decodes the response into out when given.
func call(t *testing.T, srv *httptest.Server, method, path string, body, out any) int {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		require.NoError(t, err)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, srv.URL+path, bytes.NewReader(payload))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	if out != nil {
		require.NoError(t, json.UnmarshalRead(resp.Body, out))
	}
	return resp.StatusCode
}

func licensePath(productID int64, id string) string {
	return fmt.Sprintf("/api/products/%d/licenses/%s", productID, id)
}

func createProduct(t *testing.T, srv *httptest.Server) Product {
	t.Helper()
	var p Product
	status := call(t, srv, http.MethodPost, "/api/products",
		map[string]any{"name": "App", "prefix": "APP", "plan": "pro"}, &p)
	require.Equal(t, http.StatusCreated, status)
	return p
}

func TestProductKeys(t *testing.T) {
	t.Parallel()
	srv, q := newTestServer(t)

	p := createProduct(t, srv)
	assert.Len(t, p.PublicKey, 64, "a fresh product gets a keypair")
	assert.Equal(t, int64(14), p.GraceDays)
	assert.False(t, p.ProviderConnected)

	stored, err := q.GetProduct(t.Context(), p.ID)
	require.NoError(t, err)
	var imported Product
	status := call(t, srv, http.MethodPost, "/api/products", map[string]any{
		"name": "Other", "prefix": "OTHER", "plan": "pro", "private_key": stored.PrivateKey,
	}, &imported)
	require.Equal(t, http.StatusCreated, status)
	assert.Equal(t, p.PublicKey, imported.PublicKey, "an imported private key keeps its public key")

	assert.Equal(t, http.StatusConflict, call(t, srv, http.MethodPost, "/api/products",
		map[string]any{"name": "Dup", "prefix": "APP", "plan": "pro"}, nil), "prefixes are unique")
	assert.Equal(t, http.StatusUnprocessableEntity, call(t, srv, http.MethodPost, "/api/products",
		map[string]any{"name": "Low", "prefix": "low", "plan": "pro"}, nil), "prefixes are uppercase")
	assert.Equal(t, http.StatusUnprocessableEntity, call(t, srv, http.MethodPost, "/api/products",
		map[string]any{"name": "Bad", "prefix": "BAD", "plan": "pro", "private_key": "nope"}, nil))
}

func TestIssueAndCheck(t *testing.T) {
	t.Parallel()
	srv, _ := newTestServer(t)
	p := createProduct(t, srv)

	expires := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	var l License
	status := call(t, srv, http.MethodPost, "/api/licenses", map[string]any{
		"product_id": p.ID,
		"email":      "ops@acme.io",
		"expires_at": expires,
		"meta":       map[string]string{"seats": "25"},
	}, &l)
	require.Equal(t, http.StatusCreated, status)
	assert.Equal(t, "pro", l.Plan, "the plan defaults to the product's")
	assert.Equal(t, sourceManual, l.Source)

	// The key has to hold up in a product that only knows the public key.
	require.NoError(t, license.SetPublicKey(p.PublicKey))
	t.Cleanup(func() { _ = license.SetPublicKey("") })
	claims, err := license.Verify(l.Key, "APP")
	require.NoError(t, err)
	assert.Equal(t, l.ID, claims.ID)
	assert.Equal(t, "ops@acme.io", claims.Email)
	assert.Equal(t, expires.Unix(), claims.ExpiresAt)
	assert.Equal(t, "25", claims.Meta["seats"])

	var listed []License
	require.Equal(t, http.StatusOK, call(t, srv, http.MethodGet, "/api/licenses", nil, &listed))
	require.Len(t, listed, 1)
	assert.Equal(t, l.Key, listed[0].Key, "a license shows the same key every time")

	var res CheckResult
	require.Equal(t, http.StatusOK, call(t, srv, http.MethodPost, "/api/check", map[string]string{"key": l.Key}, &res))
	assert.Equal(t, stateValid, res.State)
	assert.True(t, res.Known)
	assert.Equal(t, l.ID, res.LicenseID)
	assert.Equal(t, "25", res.Meta["seats"])

	mangled := `"` + l.Key[:40] + "\r\n" + l.Key[40:] + `"`
	call(t, srv, http.MethodPost, "/api/check", map[string]string{"key": mangled}, &res)
	assert.Equal(t, stateValid, res.State, "a key quoted and wrapped on its way still checks")

	// A character well inside the signature, the last one may only hold padding bits.
	i := len(l.Key) - 12
	if l.Key[i] == '-' {
		i--
	}
	flipped := byte('A')
	if l.Key[i] == 'A' {
		flipped = 'B'
	}
	tampered := l.Key[:i] + string(flipped) + l.Key[i+1:]
	call(t, srv, http.MethodPost, "/api/check", map[string]string{"key": tampered}, &res)
	assert.Equal(t, stateInvalid, res.State)
	assert.NotEmpty(t, res.Reason)

	call(t, srv, http.MethodPost, "/api/check", map[string]string{"key": "NOPE-AAAAA"}, &res)
	assert.Equal(t, stateInvalid, res.State)
	assert.Nil(t, res.ProductID, "an unknown prefix has no product")

	// Moving the expiry into the past changes the key and expires it.
	var edited License
	status = call(t, srv, http.MethodPut, licensePath(p.ID, l.ID), map[string]any{
		"email":      "ops@acme.io",
		"expires_at": time.Now().Add(-time.Hour),
	}, &edited)
	require.Equal(t, http.StatusOK, status)
	assert.NotEqual(t, l.Key, edited.Key)
	call(t, srv, http.MethodPost, "/api/check", map[string]string{"key": edited.Key}, &res)
	assert.Equal(t, stateExpired, res.State)

	assert.Equal(t, http.StatusNotFound, call(t, srv, http.MethodPut, licensePath(p.ID+1, l.ID),
		map[string]any{"email": "ops@acme.io"}, nil), "a license is only found under its own product")
	assert.Equal(t, http.StatusNoContent, call(t, srv, http.MethodDelete, licensePath(p.ID, l.ID), nil, nil))
	call(t, srv, http.MethodPost, "/api/check", map[string]string{"key": l.Key}, &res)
	assert.Equal(t, stateValid, res.State, "a deleted license still has a working key out there")
	assert.False(t, res.Known)
}

func TestProviderLicenseIsReadOnly(t *testing.T) {
	t.Parallel()
	srv, q := newTestServer(t)
	p := createProduct(t, srv)

	require.NoError(t, q.UpsertProviderLicense(t.Context(), &db.UpsertProviderLicenseParams{
		ID: "sub_1", ProductID: p.ID, Source: "stripe", Email: "a@x.io", Plan: "pro", ExpiresAt: 1_900_000_000,
		Status: "active", Renews: true, CreatedAt: 1_700_000_000,
	}))

	var l License
	status := call(t, srv, http.MethodPut, licensePath(p.ID, "sub_1"),
		map[string]any{"email": "evil@x.io", "plan": "enterprise", "note": "vip"}, &l)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "vip", l.Note)
	assert.Equal(t, "a@x.io", l.Email, "Stripe owns everything but the note")
	assert.Equal(t, "pro", l.Plan)

	assert.Equal(t, http.StatusConflict, call(t, srv, http.MethodDelete, licensePath(p.ID, "sub_1"), nil, nil))
}

func TestMetaLimit(t *testing.T) {
	t.Parallel()
	srv, _ := newTestServer(t)
	p := createProduct(t, srv)

	status := call(t, srv, http.MethodPost, "/api/licenses", map[string]any{
		"product_id": p.ID,
		"email":      "ops@acme.io",
		"meta":       map[string]string{"blob": string(bytes.Repeat([]byte("x"), maxMetaBytes))},
	}, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, status, "oversized metadata would bloat the key")
}

func TestCrossSiteWritesAreRefused(t *testing.T) {
	t.Parallel()
	srv, _ := newTestServer(t)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/api/products",
		bytes.NewReader([]byte(`{"name":"App","prefix":"APP","plan":"pro"}`)))
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "there is no login, another site must not issue keys")
}
