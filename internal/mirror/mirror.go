// Package mirror copies the paid licenses of a product from its worker into
// the database. The worker reads the payment provider and signs the keys,
// inkan only shows what it hands out. WORKER.md describes the endpoint.
package mirror

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mizuchilabs/inkan/license"

	"github.com/mizuchilabs/inkan/internal/db"
)

const (
	syncEvery = 5 * time.Minute
	pageSize  = 50
)

var client = &http.Client{Timeout: 30 * time.Second}

// page is one answer of GET /licenses.
type page struct {
	Product  settings `json:"product"`
	Licenses []listed `json:"licenses"`
	Skipped  []string `json:"skipped"`
	Next     *string  `json:"next"`
}

// settings is what the worker runs with.
type settings struct {
	Prefix        string `json:"prefix"`
	PublicKey     string `json:"public_key"`
	Plan          string `json:"plan"`
	GraceDays     int64  `json:"grace_days"`
	KeyDays       int64  `json:"key_days"`
	StripeProduct string `json:"stripe_product"`
}

type listed struct {
	ID           string            `json:"id"`
	Email        string            `json:"email"`
	Customer     string            `json:"customer"`
	CustomerGone bool              `json:"customer_gone"`
	Plan         string            `json:"plan"`
	Meta         map[string]string `json:"meta"`
	ExpiresAt    int64             `json:"expires_at"`
	Status       string            `json:"status"`
	Renews       bool              `json:"renews"`
	CreatedAt    int64             `json:"created_at"`
	Key          string            `json:"key"`
}

// Run syncs every product that has a worker on start and then every few
// minutes, until ctx is done.
func Run(ctx context.Context, q *db.Queries) {
	for {
		products, err := q.ListProducts(ctx)
		if err != nil {
			slog.Error("worker sync: listing products failed", "error", err)
		}
		for _, p := range products {
			if !Connected(p) {
				continue
			}
			n, err := Sync(ctx, q, p)
			if err != nil {
				slog.Warn("worker sync failed", "product", p.Name, "error", err)
				continue
			}
			slog.Debug("worker sync done", "product", p.Name, "licenses", n)
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
	return p.WorkerUrl != ""
}

// Sync mirrors the licenses the worker of a product hands out and returns how
// many there are. The outcome is recorded on the product, so the UI can show it.
func Sync(ctx context.Context, q *db.Queries, p *db.Product) (int, error) {
	n, notice, err := pull(ctx, q, p)
	if err != nil {
		if dbErr := q.SetProductSyncError(ctx, &db.SetProductSyncErrorParams{
			SyncError: err.Error(),
			ID:        p.ID,
		}); dbErr != nil {
			slog.Error("recording worker sync error failed", "error", dbErr)
		}
		return 0, err
	}
	return n, q.SetProductSynced(ctx, &db.SetProductSyncedParams{SyncError: notice, ID: p.ID})
}

// pull reads every page of the worker. notice says what is off with a worker
// that could be read all the same.
func pull(ctx context.Context, q *db.Queries, p *db.Product) (n int, notice string, err error) {
	priv, err := license.ParsePrivateKey(p.PrivateKey)
	if err != nil {
		return 0, "", err
	}
	pub, _ := priv.Public().(ed25519.PublicKey)

	// Rows a full round did not see are gone at the provider.
	round := time.Now().Unix()
	purchases := true
	cursor := ""
	asked := map[string]bool{}
	var runs settings
	for {
		asked[cursor] = true
		pg, err := fetch(ctx, p, cursor)
		if err != nil {
			return 0, "", err
		}
		runs = pg.Product
		if runs.Prefix != p.Prefix || runs.PublicKey != hex.EncodeToString(pub) {
			return 0, "", errors.New("this worker signs for another product, check the worker url")
		}
		if slices.Contains(pg.Skipped, "purchases") {
			purchases = false
		}
		for _, l := range pg.Licenses {
			if err := store(ctx, q, p, l, round); err != nil {
				return 0, "", err
			}
			n++
		}
		if pg.Next == nil {
			break
		}
		cursor = *pg.Next
		if asked[cursor] {
			return 0, "", errors.New("worker: its pages go in a circle")
		}
	}

	notice = drift(p, runs)
	// A worker on another Stripe product lists other licenses. Until that is
	// settled nothing is dropped, the notes on the rows would go with them.
	if runs.StripeProduct != p.ProviderProduct {
		return n, notice, nil
	}
	err = q.DeleteUnseenProviderLicenses(ctx, &db.DeleteUnseenProviderLicensesParams{
		ProductID: p.ID,
		SeenAt:    round,
		Purchases: purchases,
	})
	return n, notice, err
}

func fetch(ctx context.Context, p *db.Product, cursor string) (*page, error) {
	query := url.Values{"cursor": {cursor}, "limit": {strconv.Itoa(pageSize)}}
	target := strings.TrimRight(p.WorkerUrl, "/") + "/licenses?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("worker: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.WorkerToken)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("worker: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("worker: reading the answer: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return nil, errors.New("worker: it has another token, export worker env again and deploy it")
	case http.StatusNotFound:
		return nil, errors.New("worker: no /licenses there, check the worker url and that it has a token")
	default:
		said := strings.TrimSpace(string(body[:min(len(body), 200)]))
		return nil, fmt.Errorf("worker: %s: %s", resp.Status, said)
	}

	var pg page
	if err := json.Unmarshal(body, &pg); err != nil {
		return nil, fmt.Errorf("worker: reading the answer: %w", err)
	}
	return &pg, nil
}

// drift names the settings the worker runs with that the product does not
// show. They only reach the worker when its env is exported and deployed.
func drift(p *db.Product, s settings) string {
	var off []string
	if s.Plan != p.Plan {
		off = append(off, fmt.Sprintf("plan %q", s.Plan))
	}
	if s.GraceDays != p.GraceDays {
		off = append(off, fmt.Sprintf("%d grace days", s.GraceDays))
	}
	if s.KeyDays != p.KeyDays {
		off = append(off, fmt.Sprintf("%d key days", s.KeyDays))
	}
	if s.StripeProduct != p.ProviderProduct {
		off = append(off, "Stripe product "+s.StripeProduct)
	}
	if len(off) == 0 {
		return ""
	}
	return "The worker runs with " + strings.Join(off, ", ") +
		". Export worker env again and deploy it, or set the same here."
}

func store(ctx context.Context, q *db.Queries, p *db.Product, l listed, round int64) error {
	meta := "{}"
	if len(l.Meta) > 0 {
		b, err := json.Marshal(l.Meta, json.Deterministic(true))
		if err != nil {
			return fmt.Errorf("storing license %s: %w", l.ID, err)
		}
		meta = string(b)
	}
	if err := q.UpsertProviderLicense(ctx, &db.UpsertProviderLicenseParams{
		ID:           l.ID,
		ProductID:    p.ID,
		Source:       "stripe",
		Email:        l.Email,
		Customer:     l.Customer,
		CustomerGone: l.CustomerGone,
		Plan:         l.Plan,
		ExpiresAt:    l.ExpiresAt,
		Meta:         meta,
		Status:       l.Status,
		Renews:       l.Renews,
		Key:          l.Key,
		SeenAt:       round,
		CreatedAt:    l.CreatedAt,
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
