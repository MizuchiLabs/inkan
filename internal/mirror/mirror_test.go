package mirror

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/mizuchilabs/inkan/internal/db"
	"github.com/mizuchilabs/inkan/internal/stripe"
)

const (
	periodEnd = 1_800_000_000
	item      = `"items": {"data": [{"price": {"product": "prod_app"}, "current_period_end": 1800000000}]}`
)

func TestSync(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk_good" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error": {"message": "Invalid API Key provided"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"has_more": false, "data": [
			{"id": "sub_1", "status": "active", "customer": {"email": "a@x.io"}, ` + item + `},
			{"id": "sub_2", "status": "incomplete", "customer": {"email": "b@x.io"}, ` + item + `},
			{"id": "sub_3", "status": "canceled", "customer": {"email": "c@x.io"}, ` + item + `},
			{"id": "sub_4", "status": "active", "customer": {"email": "d@x.io"},
				"items": {"data": [{"price": {"product": "prod_other"}, "current_period_end": 1800000000}]}}
		]}`))
	}))
	t.Cleanup(srv.Close)
	live := stripe.APIURL
	stripe.APIURL = srv.URL                    //nolint:reassign // the var exists so tests can fake Stripe
	t.Cleanup(func() { stripe.APIURL = live }) //nolint:reassign // see above

	ctx := t.Context()
	q, err := db.Open(ctx, t.TempDir())
	require.NoError(t, err)
	p, err := q.CreateProduct(ctx, &db.CreateProductParams{Name: "App", Prefix: "APP", Plan: "pro", GraceDays: 14})
	require.NoError(t, err)
	assert.False(t, Connected(p))
	p, err = q.UpdateProduct(ctx, &db.UpdateProductParams{
		Name: p.Name, Plan: p.Plan, GraceDays: p.GraceDays, StripeKey: "sk_good", StripeProduct: "prod_app", ID: p.ID,
	})
	require.NoError(t, err)
	assert.True(t, Connected(p))

	n, err := Sync(ctx, q, p)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "the unpaid subscription and the one of another product are left out")
	_, err = q.GetLicense(ctx, "sub_4")
	require.Error(t, err, "another product's subscription must not become a license here")

	_, err = q.SetLicenseNote(ctx, &db.SetLicenseNoteParams{Note: "vip", ID: "sub_1"})
	require.NoError(t, err)
	_, err = Sync(ctx, q, p)
	require.NoError(t, err)
	l, err := q.GetLicense(ctx, "sub_1")
	require.NoError(t, err)
	assert.Equal(t, "vip", l.Note, "a sync must not wipe the note")
	assert.Equal(t, int64(periodEnd+14*86400), l.ExpiresAt)
	assert.Equal(t, "pro", l.Plan)

	p, err = q.GetProduct(ctx, p.ID)
	require.NoError(t, err)
	assert.NotNil(t, p.SyncedAt)
	assert.Empty(t, p.SyncError)

	p.StripeKey = "sk_bad"
	_, err = Sync(ctx, q, p)
	require.ErrorContains(t, err, "Invalid API Key provided")
	p, err = q.GetProduct(ctx, p.ID)
	require.NoError(t, err)
	assert.Contains(t, p.SyncError, "Invalid API Key provided", "the failure is kept for the UI")
}
