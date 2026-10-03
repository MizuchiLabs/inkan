// Package stripe reads subscriptions from Stripe and decides what license
// each one is owed. The ledger and the worker both go through it, so they
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
	"time"
)

// APIURL is a var so tests can point it at a fake.
var APIURL = "https://api.stripe.com"

var (
	ErrNotFound     = errors.New("stripe: no such subscription")
	ErrOtherProduct = errors.New("stripe: the subscription is for another product")
	ErrNotOwed      = errors.New("stripe: the subscription was never paid")
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
	Price              struct {
		Product  string            `json:"product"`
		Metadata map[string]string `json:"metadata"`
	} `json:"price"`
}

// License is what a subscription is owed.
type License struct {
	ID        string // the subscription id
	Email     string
	Customer  string // display name, not part of the key
	Plan      string
	ExpiresAt int64
	Status    string
	Renews    bool
	CreatedAt int64
}

// License decides what the subscription is owed for a Stripe product. This is
// the one place that decides what a paid license looks like. The plan comes
// from the plan metadata of the price, prices without it get the given one.
func (s *Subscription) License(product, plan string, graceDays int64) (*License, error) {
	i := slices.IndexFunc(s.Items.Data, func(it item) bool { return it.Price.Product == product })
	if i < 0 {
		return nil, ErrOtherProduct
	}
	it := &s.Items.Data[i]
	start := cmp.Or(it.CurrentPeriodStart, s.CurrentPeriodStart)
	paid := cmp.Or(it.CurrentPeriodEnd, s.CurrentPeriodEnd)

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
		Plan:     cmp.Or(it.Price.Metadata["plan"], plan),
		// The grace keeps the old key alive until the renewed one is in place.
		ExpiresAt: paid + graceDays*86400,
		Status:    s.Status,
		Renews:    (s.Status == "active" || s.Status == "trialing") && !s.CancelAtPeriodEnd,
		CreatedAt: s.Created,
	}, nil
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
	return func(yield func(*Subscription, error) bool) {
		query := url.Values{
			"status":   {"all"},
			"limit":    {"100"},
			"expand[]": {"data.customer"},
		}
		for {
			var page struct {
				Data    []Subscription `json:"data"`
				HasMore bool           `json:"has_more"`
			}
			if err := get(ctx, key, "/v1/subscriptions?"+query.Encode(), &page); err != nil {
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
			query.Set("starting_after", page.Data[len(page.Data)-1].ID)
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
		return fmt.Errorf("stripe: %s", cmp.Or(failure.Error.Message, resp.Status))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("stripe: decoding response: %w", err)
	}
	return nil
}
