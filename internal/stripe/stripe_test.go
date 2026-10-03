package stripe

import (
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	periodStart = 1_797_000_000
	periodEnd   = 1_800_000_000
	grace       = 14 * 86400
)

func sub(t *testing.T, raw string) *Subscription {
	t.Helper()
	var s Subscription
	require.NoError(t, json.Unmarshal([]byte(raw), &s))
	return &s
}

func TestLicense(t *testing.T) {
	t.Parallel()

	l, err := sub(t, `{
		"id": "sub_1", "status": "active", "created": 1700000000,
		"customer": {"email": "billing@acme.io", "name": "Acme Billing"},
		"metadata": {"email": "ops@acme.io", "org": "Acme Inc"},
		"items": {"data": [
			{"price": {"product": "prod_other"}, "current_period_end": 5},
			{"price": {"product": "prod_app", "metadata": {"plan": "team"}},
				"current_period_start": 1797000000, "current_period_end": 1800000000}
		]}
	}`).License("prod_app", "pro", 14)
	require.NoError(t, err)
	assert.Equal(t, &License{
		ID:        "sub_1",
		Email:     "ops@acme.io",
		Customer:  "Acme Inc",
		Plan:      "team",
		ExpiresAt: periodEnd + grace,
		Status:    "active",
		Renews:    true,
		CreatedAt: 1700000000,
	}, l, "metadata wins over the customer, plan and period come from the item of the product")

	old := sub(t, `{
		"id": "sub_2", "status": "active", "cancel_at_period_end": true,
		"current_period_start": 1797000000, "current_period_end": 1800000000,
		"customer": {"email": "billing@acme.io", "name": null},
		"items": {"data": [{"price": {"product": "prod_app", "metadata": {}}}]}
	}`)
	l, err = old.License("prod_app", "pro", 14)
	require.NoError(t, err, "older API versions carry the period on the subscription")
	assert.Equal(t, "billing@acme.io", l.Email)
	assert.Equal(t, "pro", l.Plan, "a price without plan metadata gets the product's plan")
	assert.Equal(t, int64(periodEnd+grace), l.ExpiresAt)
	assert.False(t, l.Renews, "a subscription that ends with the period does not renew")

	_, err = old.License("prod_else", "pro", 14)
	assert.ErrorIs(t, err, ErrOtherProduct)
}

// A key must never reach further than what was paid.
func TestLicensePaidUntil(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		fields string
		want   int64 // 0 means no key is owed
	}{
		{"active", `"status": "active"`, periodEnd},
		{"trialing", `"status": "trialing"`, periodEnd},
		{"renewal not paid yet", `"status": "past_due"`, periodStart},
		{"renewal never paid", `"status": "unpaid"`, periodStart},
		{"trial ended without a card", `"status": "paused"`, periodStart},
		{"canceled at period end", `"status": "canceled", "ended_at": 1800000000`, periodEnd},
		{"canceled mid period", `"status": "canceled", "ended_at": 1798000000`, 1_798_000_000},
		{
			"canceled after failed payments",
			`"status": "canceled", "ended_at": 1798000000, "cancellation_details": {"reason": "payment_failed"}`,
			periodStart,
		},
		{"first payment open", `"status": "incomplete"`, 0},
		{"first payment never came", `"status": "incomplete_expired"`, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l, err := sub(t, fmt.Sprintf(`{"id": "sub_1", %s, "items": {"data": [{
				"price": {"product": "prod_app"},
				"current_period_start": 1797000000, "current_period_end": 1800000000
			}]}}`, tt.fields)).License("prod_app", "pro", 14)
			if tt.want == 0 {
				require.ErrorIs(t, err, ErrNotOwed)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want+grace, l.ExpiresAt)
		})
	}
}

func TestFetching(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		switch {
		case r.Header.Get("Authorization") != "Bearer sk_good":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error": {"message": "Invalid API Key provided"}}`))
		case r.URL.Path == "/v1/subscriptions/sub_1":
			assert.Equal(t, "customer", query.Get("expand[]"))
			_, _ = w.Write([]byte(`{"id": "sub_1", "customer": {"email": "a@x.io"}}`))
		case r.URL.Path != "/v1/subscriptions":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error": {"message": "No such subscription"}}`))
		case query.Get("starting_after") == "":
			assert.Equal(t, "all", query.Get("status"))
			assert.Equal(t, "data.customer", query.Get("expand[]"))
			_, _ = w.Write([]byte(`{"has_more": true, "data": [{"id": "sub_1"}, {"id": "sub_2"}]}`))
		default:
			assert.Equal(t, "sub_2", query.Get("starting_after"))
			_, _ = w.Write([]byte(`{"has_more": false, "data": [{"id": "sub_3"}]}`))
		}
	}))
	t.Cleanup(srv.Close)
	APIURL = srv.URL
	t.Cleanup(func() { APIURL = "https://api.stripe.com" })
	ctx := t.Context()

	var ids []string
	for s, err := range Subscriptions(ctx, "sk_good") {
		require.NoError(t, err)
		ids = append(ids, s.ID)
	}
	assert.Equal(t, []string{"sub_1", "sub_2", "sub_3"}, ids, "every page is read")

	for _, err := range Subscriptions(ctx, "sk_bad") {
		require.ErrorContains(t, err, "Invalid API Key provided", "Stripe's own words reach the UI")
	}

	s, err := Get(ctx, "sk_good", "sub_1")
	require.NoError(t, err)
	assert.Equal(t, "a@x.io", s.Customer.Email)

	_, err = Get(ctx, "sk_good", "sub_gone")
	assert.ErrorIs(t, err, ErrNotFound)
}
