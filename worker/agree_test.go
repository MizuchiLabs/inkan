package worker_test

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/mizuchilabs/inkan/internal/api"
	"github.com/mizuchilabs/inkan/internal/db"
	"github.com/mizuchilabs/inkan/internal/mirror"
	"github.com/mizuchilabs/inkan/license"
	"github.com/mizuchilabs/inkan/worker"
)

// The promise of the whole design: for the same subscription the worker hands
// out the very key the ledger shows.
func TestWorkerAndLedgerAgree(t *testing.T) {
	worker.FakeStripe(t)
	cfg, priv := worker.TestConfig(t)
	// Key days make the expiry depend on the clock, both sides have to cut it alike.
	cfg.KeyDays = 30
	ctx := t.Context()

	q, err := db.Open(ctx, t.TempDir())
	require.NoError(t, err)
	p, err := q.CreateProduct(ctx, &db.CreateProductParams{
		Name: "App", Prefix: cfg.Prefix, Plan: cfg.Plan, GraceDays: cfg.GraceDays, PrivateKey: cfg.PrivateKey,
	})
	require.NoError(t, err)
	p, err = q.UpdateProduct(ctx, &db.UpdateProductParams{
		Name: p.Name, Plan: p.Plan, GraceDays: p.GraceDays, KeyDays: cfg.KeyDays,
		ProviderKey: cfg.StripeKey, ProviderProduct: cfg.StripeProduct, ID: p.ID,
	})
	require.NoError(t, err)
	_, err = mirror.Sync(ctx, q, p)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	ledgerAPI, err := api.New(q, "", "")
	require.NoError(t, err)
	ledgerAPI.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/licenses", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var ledger []api.License
	require.NoError(t, json.UnmarshalRead(rec.Body, &ledger))
	require.Len(t, ledger, 2, "the subscription and the one time purchase")

	h, err := worker.New(cfg)
	require.NoError(t, err)
	for _, l := range ledger {
		// Signed by the same product but stale, so the worker has to ask Stripe.
		stale, err := license.Issue(cfg.Prefix, license.Claims{ExpiresAt: 1, ID: l.ID}, priv)
		require.NoError(t, err)
		status, key := worker.Post(t, h, "/refresh", stale, nil)
		require.Equal(t, http.StatusOK, status, l.ID)
		assert.Equal(t, l.Key, key, l.ID)
	}
}
