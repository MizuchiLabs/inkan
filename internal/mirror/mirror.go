// Package mirror copies a product's license subscriptions from Stripe into
// the database. Stripe stays the source of truth, inkan only reads it.
package mirror

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/mizuchilabs/inkan/internal/db"
	"github.com/mizuchilabs/inkan/internal/stripe"
)

const syncEvery = 5 * time.Minute

// Run syncs every connected product on start and then every few minutes,
// until ctx is done.
func Run(ctx context.Context, q *db.Queries) {
	for {
		products, err := q.ListProducts(ctx)
		if err != nil {
			slog.Error("stripe sync: listing products failed", "error", err)
		}
		for _, p := range products {
			if !Connected(p) {
				continue
			}
			n, err := Sync(ctx, q, p)
			if err != nil {
				slog.Warn("stripe sync failed", "product", p.Name, "error", err)
				continue
			}
			slog.Debug("stripe sync done", "product", p.Name, "licenses", n)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(syncEvery):
		}
	}
}

func Connected(p *db.Product) bool {
	return p.StripeKey != "" && p.StripeProduct != ""
}

// Sync mirrors the product's license subscriptions and returns how many it
// holds. The outcome is recorded on the product, so the UI can show it.
func Sync(ctx context.Context, q *db.Queries, p *db.Product) (int, error) {
	n, err := pull(ctx, q, p)
	if err != nil {
		if dbErr := q.SetProductSyncError(ctx, &db.SetProductSyncErrorParams{
			SyncError: err.Error(),
			ID:        p.ID,
		}); dbErr != nil {
			slog.Error("recording stripe sync error failed", "error", dbErr)
		}
		return 0, err
	}
	return n, q.SetProductSynced(ctx, p.ID)
}

func pull(ctx context.Context, q *db.Queries, p *db.Product) (int, error) {
	n := 0
	for sub, err := range stripe.Subscriptions(ctx, p.StripeKey) {
		if err != nil {
			return 0, err
		}
		l, err := sub.License(p.StripeProduct, p.Plan, p.GraceDays)
		if err != nil {
			continue
		}
		if err := q.UpsertStripeLicense(ctx, &db.UpsertStripeLicenseParams{
			ID:           l.ID,
			ProductID:    p.ID,
			Email:        l.Email,
			Customer:     l.Customer,
			Plan:         l.Plan,
			ExpiresAt:    l.ExpiresAt,
			StripeStatus: l.Status,
			Renews:       l.Renews,
			CreatedAt:    l.CreatedAt,
		}); err != nil {
			return 0, fmt.Errorf("storing license %s: %w", l.ID, err)
		}
		n++
	}
	return n, nil
}
