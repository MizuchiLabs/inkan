// Package mirror copies a product's subscriptions and one time purchases from
// its payment provider into the database. The provider stays the source of truth, inkan
// only reads it. Stripe is the one provider there is.
package mirror

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/mizuchilabs/inkan/internal/db"
	"github.com/mizuchilabs/inkan/internal/stripe"
)

const syncEvery = 5 * time.Minute

// Run syncs every connected product on start and then every few minutes,
// until ctx is done. Products that share a provider key are read in one go.
func Run(ctx context.Context, q *db.Queries) {
	for {
		products, err := q.ListProducts(ctx)
		if err != nil {
			slog.Error("provider sync: listing products failed", "error", err)
		}
		byKey := map[string][]*db.Product{}
		for _, p := range products {
			if Connected(p) {
				byKey[p.ProviderKey] = append(byKey[p.ProviderKey], p)
			}
		}
		for _, group := range byKey {
			n, err := Sync(ctx, q, group...)
			if err != nil {
				slog.Warn("provider sync failed", "product", group[0].Name, "error", err)
				continue
			}
			slog.Debug("provider sync done", "products", len(group), "licenses", n)
		}
		if err := snapshot(ctx, q); err != nil && ctx.Err() == nil {
			slog.Error("taking the daily snapshot failed", "error", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(syncEvery):
		}
	}
}

func Connected(p *db.Product) bool {
	return p.ProviderKey != "" && p.ProviderProduct != ""
}

// Sync mirrors the licenses of products that share one provider key and
// returns how many they hold. The outcome is recorded on each product, so the
// UI can show it.
func Sync(ctx context.Context, q *db.Queries, products ...*db.Product) (int, error) {
	n, err := pull(ctx, q, products)
	for _, p := range products {
		if err == nil {
			if dbErr := q.SetProductSynced(ctx, p.ID); dbErr != nil {
				return 0, dbErr
			}
			continue
		}
		if dbErr := q.SetProductSyncError(ctx, &db.SetProductSyncErrorParams{
			SyncError: err.Error(),
			ID:        p.ID,
		}); dbErr != nil {
			slog.Error("recording provider sync error failed", "error", dbErr)
		}
	}
	return n, err
}

func pull(ctx context.Context, q *db.Queries, products []*db.Product) (int, error) {
	key := products[0].ProviderKey
	now := time.Now()
	n := 0
	for sub, err := range stripe.Subscriptions(ctx, key) {
		if err != nil {
			return 0, err
		}
		for _, p := range products {
			l, err := sub.License(rules(p), now)
			if err != nil {
				continue
			}
			if err := store(ctx, q, p, l); err != nil {
				return 0, err
			}
			n++
		}
	}
	for s, err := range stripe.Sessions(ctx, key) {
		// A key that only reads subscriptions keeps working, it just shows no one time purchases.
		if errors.Is(err, stripe.ErrForbidden) {
			slog.Debug("one time purchases were not read", "error", err)
			break
		}
		if err != nil {
			return 0, err
		}
		for _, p := range products {
			l, err := s.License(rules(p), now)
			if err != nil {
				continue
			}
			if err := store(ctx, q, p, l); err != nil {
				return 0, err
			}
			n++
		}
	}
	return n, nil
}

func rules(p *db.Product) stripe.Product {
	return stripe.Product{ID: p.ProviderProduct, Plan: p.Plan, GraceDays: p.GraceDays, KeyDays: p.KeyDays}
}

func store(ctx context.Context, q *db.Queries, p *db.Product, l *stripe.License) error {
	meta := "{}"
	if len(l.Meta) > 0 {
		b, err := json.Marshal(l.Meta)
		if err != nil {
			return fmt.Errorf("storing license %s: %w", l.ID, err)
		}
		meta = string(b)
	}
	if err := q.UpsertProviderLicense(ctx, &db.UpsertProviderLicenseParams{
		ID:        l.ID,
		ProductID: p.ID,
		Source:    "stripe",
		Email:     l.Email,
		Customer:  l.Customer,
		Plan:      l.Plan,
		ExpiresAt: l.ExpiresAt,
		Meta:      meta,
		Status:    l.Status,
		Renews:    l.Renews,
		CreatedAt: l.CreatedAt,
	}); err != nil {
		return fmt.Errorf("storing license %s: %w", l.ID, err)
	}
	return nil
}

// snapshot counts the licenses of today. The day is rewritten on every round,
// so it ends on the last count of the day.
func snapshot(ctx context.Context, q *db.Queries) error {
	day := time.Now().UTC().Format(time.DateOnly)
	if err := q.DeleteSnapshots(ctx, day); err != nil {
		return err
	}
	return q.TakeSnapshot(ctx, day)
}
