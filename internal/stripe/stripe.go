// Package stripe reads subscriptions and one time purchases from Stripe and
// decides what license each one is owed. The ledger and the worker both go through it, so they
// sign the same claims.
package stripe

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// APIURL is a var so tests can point it at a fake.
var APIURL = "https://api.stripe.com"

var (
	ErrNotFound     = errors.New("stripe: no such subscription")
	ErrOtherProduct = errors.New("stripe: the subscription is for another product")
	ErrNotOwed      = errors.New("stripe: the subscription was never paid")
	ErrForbidden    = errors.New("stripe: the API key may not read this")
)

var client = &http.Client{Timeout: 30 * time.Second}

type Subscription struct {
	ID                 string            `json:"id"`
	Status             string            `json:"status"`
	Created            int64             `json:"created"`
	EndedAt            int64             `json:"ended_at"`
	CancelAtPeriodEnd  bool              `json:"cancel_at_period_end"`
	CurrentPeriodStart int64             `json:"current_period_start"` // API versions before 2025-03-31
	CurrentPeriodEnd   int64             `json:"current_period_end"`
	Metadata           map[string]string `json:"metadata"`
	Customer           struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	} `json:"customer"`
	CancellationDetails struct {
		Reason string `json:"reason"`
	} `json:"cancellation_details"`
	Items struct {
		Data []item `json:"data"`
	} `json:"items"`
}

type item struct {
	CurrentPeriodStart int64 `json:"current_period_start"`
	CurrentPeriodEnd   int64 `json:"current_period_end"`
	Quantity           int64 `json:"quantity"`
	Price              price `json:"price"`
}

type price struct {
	Product  string            `json:"product"`
	Metadata map[string]string `json:"metadata"`
}

// Session is a Stripe Checkout session. A paid one in payment mode is a one
// time purchase.
type Session struct {
	ID              string            `json:"id"`
	Mode            string            `json:"mode"`
	PaymentStatus   string            `json:"payment_status"`
	Created         int64             `json:"created"`
	Metadata        map[string]string `json:"metadata"`
	CustomerDetails struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	} `json:"customer_details"`
	PaymentIntent struct {
		LatestCharge struct {
			Refunded bool `json:"refunded"`
		} `json:"latest_charge"`
	} `json:"payment_intent"`
	LineItems struct {
		Data []lineItem `json:"data"`
	} `json:"line_items"`
}

type lineItem struct {
	Quantity int64 `json:"quantity"`
	Price    price `json:"price"`
}

// Product is the settings of one inkan product that shape its licenses.
type Product struct {
	ID        string // product id at Stripe
	Plan      string // for prices without plan metadata
	GraceDays int64  // days a key outlives its paid period
	KeyDays   int64  // longest life of a key in days, 0 has no limit
}

// License is what a subscription is owed.
type License struct {
	ID        string // the subscription id
	Email     string
	Customer  string // display name, not part of the key
	Plan      string
	Meta      map[string]string // extra claims from the price metadata, nil without any
	ExpiresAt int64
	Period    int64 // seconds one billing period lasts, 0 for a one time purchase
	Status    string
	Renews    bool
	CreatedAt int64
}

// License decides what the subscription is owed for a product. Together with
// [Session.License] this is the one place that decides what a paid license
// looks like. The plan comes from the plan metadata of the price, prices
// without it get the one of the product.
func (s *Subscription) License(p Product, now time.Time) (*License, error) {
	i := slices.IndexFunc(s.Items.Data, func(it item) bool { return it.Price.Product == p.ID })
	if i < 0 {
		return nil, ErrOtherProduct
	}
	it := &s.Items.Data[i]
	start := cmp.Or(it.CurrentPeriodStart, s.CurrentPeriodStart)
	paid := cmp.Or(it.CurrentPeriodEnd, s.CurrentPeriodEnd)
	period := paid - start

	switch s.Status {
	case "active", "trialing":
	case "incomplete", "incomplete_expired":
		return nil, ErrNotOwed
	case "canceled":
		if s.CancellationDetails.Reason == "payment_failed" {
			paid = start
		} else if s.EndedAt != 0 {
			paid = min(paid, s.EndedAt)
		}
	default:
		// past_due, unpaid, paused: Stripe already moved to a period nobody paid for.
		paid = start
	}
	if paid == 0 {
		return nil, ErrNotOwed
	}

	return &License{
		ID:       s.ID,
		Email:    cmp.Or(s.Metadata["email"], s.Customer.Email),
		Customer: cmp.Or(s.Metadata["org"], s.Customer.Name),
		Plan:     cmp.Or(it.Price.Metadata["plan"], p.Plan),
		Meta:     meta(it.Price.Metadata, it.Quantity),
		// The grace keeps the old key alive until the renewed one is in place.
		ExpiresAt: capped(paid+p.GraceDays*86400, p.KeyDays, now),
		Period:    period,
		Status:    s.Status,
		Renews:    (s.Status == "active" || s.Status == "trialing") && !s.CancelAtPeriodEnd,
		CreatedAt: s.Created,
	}, nil
}

// License decides what a one time purchase is owed for a product. The key
// never expires, unless the price has days metadata, then it lasts that long.
// A refunded purchase gets a key that ran out when it was bought.
func (s *Session) License(p Product, now time.Time) (*License, error) {
	i := slices.IndexFunc(s.LineItems.Data, func(it lineItem) bool { return it.Price.Product == p.ID })
	// A session in subscription mode is covered by its subscription.
	if i < 0 || s.Mode != "payment" {
		return nil, ErrOtherProduct
	}
	if s.PaymentStatus == "unpaid" {
		return nil, ErrNotOwed
	}
	it := &s.LineItems.Data[i]

	var expires int64
	status := "paid"
	days, _ := strconv.ParseInt(it.Price.Metadata["days"], 10, 64)
	switch {
	case s.PaymentIntent.LatestCharge.Refunded:
		status = "refunded"
		expires = s.Created + p.GraceDays*86400
	case days > 0:
		expires = s.Created + (days+p.GraceDays)*86400
	}

	return &License{
		ID:        s.ID,
		Email:     cmp.Or(s.Metadata["email"], s.CustomerDetails.Email),
		Customer:  cmp.Or(s.Metadata["org"], s.CustomerDetails.Name),
		Plan:      cmp.Or(it.Price.Metadata["plan"], p.Plan),
		Meta:      meta(it.Price.Metadata, it.Quantity),
		ExpiresAt: capped(expires, p.KeyDays, now),
		Status:    status,
		CreatedAt: s.Created,
	}, nil
}

// meta picks the extra claims out of the metadata of a price. meta_seats=5
// becomes the claim seats=5, quantity=seats puts the bought quantity into the
// claim seats.
func meta(priceMeta map[string]string, quantity int64) map[string]string {
	var out map[string]string
	set := func(k, v string) {
		if k == "" {
			return
		}
		if out == nil {
			out = map[string]string{}
		}
		out[k] = v
	}
	for k, v := range priceMeta {
		if name, ok := strings.CutPrefix(k, "meta_"); ok {
			set(name, v)
		}
	}
	set(priceMeta["quantity"], strconv.FormatInt(max(quantity, 1), 10))
	return out
}

// capped cuts an expiry to the key days of the product. The limit only moves
// every half of the key days, so everyone who signs within that time signs
// the same key. A fresh key lives between half and all of the key days.
func capped(expires, keyDays int64, now time.Time) int64 {
	if keyDays <= 0 {
		return expires
	}
	step := keyDays * 86400 / 2
	limit := (now.Unix()/step + 2) * step
	if expires == 0 {
		return limit
	}
	return min(expires, limit)
}

// Owed fetches the subscription or one time purchase behind a license id and
// decides what it is owed.
func Owed(ctx context.Context, key, id string, p Product, now time.Time) (*License, error) {
	if !strings.HasPrefix(id, "cs_") {
		sub, err := Get(ctx, key, id)
		if err != nil {
			return nil, err
		}
		return sub.License(p, now)
	}
	var s Session
	query := url.Values{"expand[]": {"line_items", "payment_intent.latest_charge"}}
	if err := get(ctx, key, "/v1/checkout/sessions/"+url.PathEscape(id)+"?"+query.Encode(), &s); err != nil {
		return nil, err
	}
	return s.License(p, now)
}

// Get fetches one subscription with its customer.
func Get(ctx context.Context, key, id string) (*Subscription, error) {
	var sub Subscription
	query := url.Values{"expand[]": {"customer"}}
	if err := get(ctx, key, "/v1/subscriptions/"+url.PathEscape(id)+"?"+query.Encode(), &sub); err != nil {
		return nil, err
	}
	return &sub, nil
}

// Subscriptions lists every subscription the key can see, whatever its
// status. Stripe cannot filter by product, and a list by price misses
// archived and inline prices, so callers sort out their own with License.
func Subscriptions(ctx context.Context, key string) iter.Seq2[*Subscription, error] {
	query := url.Values{"status": {"all"}, "expand[]": {"data.customer"}}
	return list(ctx, key, "/v1/subscriptions", query, func(s *Subscription) string { return s.ID })
}

// Sessions lists every completed Checkout session the key can see. The one
// time purchases are among them, License sorts them out.
func Sessions(ctx context.Context, key string) iter.Seq2[*Session, error] {
	query := url.Values{
		"status":   {"complete"},
		"expand[]": {"data.line_items", "data.payment_intent.latest_charge"},
	}
	return list(ctx, key, "/v1/checkout/sessions", query, func(s *Session) string { return s.ID })
}

func list[T any](
	ctx context.Context,
	key, path string,
	query url.Values,
	id func(*T) string,
) iter.Seq2[*T, error] {
	return func(yield func(*T, error) bool) {
		query.Set("limit", "100")
		for {
			var page struct {
				Data    []T  `json:"data"`
				HasMore bool `json:"has_more"`
			}
			if err := get(ctx, key, path+"?"+query.Encode(), &page); err != nil {
				yield(nil, err)
				return
			}
			for i := range page.Data {
				if !yield(&page.Data[i], nil) {
					return
				}
			}
			if !page.HasMore || len(page.Data) == 0 {
				return
			}
			query.Set("starting_after", id(&page.Data[len(page.Data)-1]))
		}
	}
}

func get(ctx context.Context, key, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, APIURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("stripe: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return fmt.Errorf("stripe: reading response: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode >= http.StatusMultipleChoices {
		var failure struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &failure)
		if resp.StatusCode == http.StatusForbidden {
			return fmt.Errorf("%w: %s", ErrForbidden, cmp.Or(failure.Error.Message, resp.Status))
		}
		return fmt.Errorf("stripe: %s", cmp.Or(failure.Error.Message, resp.Status))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("stripe: decoding response: %w", err)
	}
	return nil
}
