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
		Name:            p.Name,
		Plan:            p.Plan,
		GraceDays:       p.GraceDays,
		ProviderKey:     "sk_good",
		ProviderProduct: "prod_app",
		ID:              p.ID,
	})
	require.NoError(t, err)
	assert.True(t, Connected(p))

	n, err := Sync(ctx, q, p)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "the unpaid subscription and the one of another product are left out")
	_, err = q.GetLicense(ctx, &db.GetLicenseParams{ProductID: p.ID, ID: "sub_4"})
	require.Error(t, err, "another product's subscription must not become a license here")

	_, err = q.SetLicenseNote(ctx, &db.SetLicenseNoteParams{Note: "vip", ProductID: p.ID, ID: "sub_1"})
	require.NoError(t, err)
	_, err = Sync(ctx, q, p)
	require.NoError(t, err)
	l, err := q.GetLicense(ctx, &db.GetLicenseParams{ProductID: p.ID, ID: "sub_1"})
	require.NoError(t, err)
	assert.Equal(t, "vip", l.Note, "a sync must not wipe the note")
	assert.Equal(t, int64(periodEnd+14*86400), l.ExpiresAt)
	assert.Equal(t, "pro", l.Plan)

	p, err = q.GetProduct(ctx, p.ID)
	require.NoError(t, err)
	assert.NotNil(t, p.SyncedAt)
	assert.Empty(t, p.SyncError)

	p.ProviderKey = "sk_bad"
	_, err = Sync(ctx, q, p)
	require.ErrorContains(t, err, "Invalid API Key provided")
	p, err = q.GetProduct(ctx, p.ID)
	require.NoError(t, err)
	assert.Contains(t, p.SyncError, "Invalid API Key provided", "the failure is kept for the UI")
}

// Stripe keeps only the id of a deleted customer. The row must not lose who
// the license was for.
func TestSyncDeletedCustomer(t *testing.T) {
	customer := `{"email": "a@x.io", "name": "Acme"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/subscriptions" {
			_, _ = w.Write([]byte(`{"has_more": false, "data": []}`))
			return
		}
		_, _ = w.Write([]byte(`{"has_more": false, "data": [
			{"id": "sub_1", "status": "active", "customer": ` + customer + `, ` + item + `}
		]}`))
	}))
	t.Cleanup(srv.Close)
	live := stripe.APIURL
	stripe.APIURL = srv.URL                    //nolint:reassign // the var exists so tests can fake Stripe
	t.Cleanup(func() { stripe.APIURL = live }) //nolint:reassign // see above

	ctx := t.Context()
	q, err := db.Open(ctx, t.TempDir())
	require.NoError(t, err)
	p, err := q.CreateProduct(ctx, &db.CreateProductParams{Name: "App", Prefix: "APP", Plan: "pro"})
	require.NoError(t, err)
	p, err = q.UpdateProduct(ctx, &db.UpdateProductParams{
		Name: p.Name, Plan: p.Plan, ProviderKey: "sk_good", ProviderProduct: "prod_app", ID: p.ID,
	})
	require.NoError(t, err)

	_, err = Sync(ctx, q, p)
	require.NoError(t, err)
	customer = `{"id": "cus_1", "deleted": true}`
	_, err = Sync(ctx, q, p)
	require.NoError(t, err)

	l, err := q.GetLicense(ctx, &db.GetLicenseParams{ProductID: p.ID, ID: "sub_1"})
	require.NoError(t, err)
	assert.True(t, l.CustomerGone)
	assert.Equal(t, "a@x.io", l.Email)
	assert.Equal(t, "Acme", l.Customer)
}

// One subscription can hold two products. Each gets its own license with its
// own plan and expiry, they must not fight over one row.
func TestSyncBundle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"has_more": false, "data": [
			{"id": "sub_1", "status": "active", "customer": {"email": "a@x.io"}, "items": {"data": [
				{"price": {"product": "prod_a", "metadata": {"plan": "team"}}, "current_period_end": 1800000000},
				{"price": {"product": "prod_b", "metadata": {"plan": "solo"}}, "current_period_end": 1900000000}
			]}}
		]}`))
	}))
	t.Cleanup(srv.Close)
	live := stripe.APIURL
	stripe.APIURL = srv.URL                    //nolint:reassign // the var exists so tests can fake Stripe
	t.Cleanup(func() { stripe.APIURL = live }) //nolint:reassign // see above

	ctx := t.Context()
	q, err := db.Open(ctx, t.TempDir())
	require.NoError(t, err)

	want := map[string]struct {
		plan    string
		expires int64
	}{"a": {"team", 1_800_000_000}, "b": {"solo", 1_900_000_000}}
	for name := range want {
		p, err := q.CreateProduct(ctx, &db.CreateProductParams{Name: name, Prefix: "APP" + name, Plan: "pro"})
		require.NoError(t, err)
		p, err = q.UpdateProduct(ctx, &db.UpdateProductParams{
			Name: p.Name, Plan: p.Plan, ProviderKey: "sk_good", ProviderProduct: "prod_" + name, ID: p.ID,
		})
		require.NoError(t, err)
		_, err = Sync(ctx, q, p)
		require.NoError(t, err)
	}

	licenses, err := q.ListLicenses(ctx)
	require.NoError(t, err)
	require.Len(t, licenses, 2)
	products, err := q.ListProducts(ctx)
	require.NoError(t, err)
	for _, p := range products {
		l, err := q.GetLicense(ctx, &db.GetLicenseParams{ProductID: p.ID, ID: "sub_1"})
		require.NoError(t, err, p.Name)
		assert.Equal(t, want[p.Name].plan, l.Plan, p.Name)
		assert.Equal(t, want[p.Name].expires, l.ExpiresAt, p.Name)
	}
}

// Two products behind one Stripe key are read in one go. A key that may not
// read Checkout sessions still syncs its subscriptions.
func TestSyncSharedKey(t *testing.T) {
	var lists int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/checkout/sessions" {
			if r.Header.Get("Authorization") == "Bearer sk_subs_only" {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error": {"message": "The key lacks the permission"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"has_more": false, "data": [
				{"id": "cs_1", "mode": "payment", "payment_status": "paid", "created": 1780000000,
					"customer_details": {"email": "b@x.io"},
					"line_items": {"data": [{"quantity": 2,
						"price": {"product": "prod_b", "metadata": {"meta_tier": "gold", "quantity": "seats"}}}]}}
			]}`))
			return
		}
		lists++
		_, _ = w.Write([]byte(`{"has_more": false, "data": [
			{"id": "sub_1", "status": "active", "customer": {"email": "a@x.io"}, "items": {"data": [
				{"price": {"product": "prod_a"}, "current_period_end": 1800000000}]}}
		]}`))
	}))
	t.Cleanup(srv.Close)
	live := stripe.APIURL
	stripe.APIURL = srv.URL                    //nolint:reassign // the var exists so tests can fake Stripe
	t.Cleanup(func() { stripe.APIURL = live }) //nolint:reassign // see above

	ctx := t.Context()
	q, err := db.Open(ctx, t.TempDir())
	require.NoError(t, err)
	var products []*db.Product
	for _, name := range []string{"a", "b"} {
		p, err := q.CreateProduct(ctx, &db.CreateProductParams{Name: name, Prefix: "APP" + name, Plan: "pro"})
		require.NoError(t, err)
		p, err = q.UpdateProduct(ctx, &db.UpdateProductParams{
			Name: p.Name, Plan: p.Plan, ProviderKey: "sk_good", ProviderProduct: "prod_" + name, ID: p.ID,
		})
		require.NoError(t, err)
		products = append(products, p)
	}

	n, err := Sync(ctx, q, products...)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "one subscription for a, one purchase for b")
	assert.Equal(t, 1, lists, "Stripe is read once for both products")

	l, err := q.GetLicense(ctx, &db.GetLicenseParams{ProductID: products[1].ID, ID: "cs_1"})
	require.NoError(t, err)
	assert.Zero(t, l.ExpiresAt, "a one time purchase never expires")
	assert.Equal(t, "paid", l.Status)
	assert.JSONEq(t, `{"seats": "2", "tier": "gold"}`, l.Meta, "the price metadata is signed into the key")

	for _, p := range products {
		p.ProviderKey = "sk_subs_only"
	}
	n, err = Sync(ctx, q, products...)
	require.NoError(t, err, "a key without access to Checkout still syncs")
	assert.Equal(t, 1, n)

	require.NoError(t, snapshot(ctx, q))
	require.NoError(t, snapshot(ctx, q), "a second count on the same day replaces the first")
	snapshots, err := q.ListSnapshots(ctx, "2000-01-01")
	require.NoError(t, err)
	require.Len(t, snapshots, 2)
	assert.Equal(t, int64(1), snapshots[0].Active)
}
