// Package mirror copies the paid licenses of a product from its worker into
// the database. The worker reads the payment provider and signs the keys,
// inkan only shows what it hands out. WORKER.md describes the endpoint.
package mirror

import (
	"cmp"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mizuchilabs/inkan/license"

	"github.com/mizuchilabs/inkan/internal/db"
)

const (
	// A sync reads every subscription and purchase of the Stripe account, so
	// it is not cheap. The dashboard has a button for when it has to be now.
	syncEvery = time.Hour
	// The most a worker hands out, fewer pages are fewer calls to Stripe.
	pageSize = 100
	// A million licenses. A worker that never ends its list must not keep a sync going.
	maxPages = 10_000
)

// A redirect could take the token to plain http or another host.
var client = &http.Client{
	Timeout:       30 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// page is one answer of GET /licenses.
type page struct {
	Product  Report   `json:"product"`
	Licenses []listed `json:"licenses"`
	Skipped  []string `json:"skipped"`
	Next     *string  `json:"next"`
}

// Report is what a worker says it runs with. Its settings live in its env
// alone, so this is the only place the dashboard knows them from.
type Report struct {
	Provider        string   `json:"provider"         doc:"Who the worker asks what is paid, like stripe"`
	Prefix          string   `json:"prefix"`
	PublicKey       string   `json:"public_key"       doc:"Hex public key of the key the worker signs with"`
	OldPublicKeys   []string `json:"old_public_keys"  doc:"Keys from before a rotation that a refresh still takes"                             nullable:"false"`
	Plan            string   `json:"plan"             doc:"Plan for Stripe products that set none"`
	Name            string   `json:"name"             doc:"How the key mail calls the product"`
	GraceDays       int64    `json:"grace_days"       doc:"Days a paid license stays valid past its paid period"`
	MaxKeyDays      int64    `json:"max_key_days"     doc:"Days a paid key lives before the app has to refresh, 0 has no limit"`
	ProviderProduct string   `json:"provider_product" doc:"The products at the provider with the plan each gives, like prod_a=team,prod_b=pro"`
	Mail            *Mail    `json:"mail,omitempty"   doc:"The key mail, missing when the worker sends none"`
}

// Mail is how the key mail of a worker looks.
type Mail struct {
	From    string `json:"from"`
	ReplyTo string `json:"reply_to"`
	Logo    string `json:"logo"`
	Accent  string `json:"accent"`
	Footer  string `json:"footer"`
	Company string `json:"company"`
	Custom  bool   `json:"custom"   doc:"The worker was built with a mail of its own"`
}

type listed struct {
	ID           string            `json:"id"`
	Kind         string            `json:"kind"`
	Email        string            `json:"email"`
	Customer     string            `json:"customer"`
	CustomerGone bool              `json:"customer_gone"`
	Plan         string            `json:"plan"`
	Meta         map[string]string `json:"meta"`
	Fingerprint  string            `json:"fingerprint"`
	ExpiresAt    int64             `json:"expires_at"`
	Status       string            `json:"status"`
	Renews       bool              `json:"renews"`
	CreatedAt    int64             `json:"created_at"`
	Key          string            `json:"key"`
}

// Run syncs every product that has a worker on start and then every hour,
// until ctx is done.
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
	n, runs, err := pull(ctx, q, p)
	if errors.Is(err, context.DeadlineExceeded) {
		err = errors.New("worker: reading it took too long, the next sync tries again")
	}
	if err != nil {
		// ctx may be the reason it failed, the outcome is still worth keeping.
		if dbErr := q.SetProductSyncError(context.WithoutCancel(ctx), &db.SetProductSyncErrorParams{
			SyncError: err.Error(),
			ID:        p.ID,
		}); dbErr != nil {
			slog.Error("recording worker sync error failed", "error", dbErr)
		}
		return 0, err
	}
	report, err := json.Marshal(runs)
	if err != nil {
		return 0, err
	}
	return n, q.SetProductSynced(ctx, &db.SetProductSyncedParams{
		SyncNotice:   drift(p, runs),
		WorkerReport: string(report),
		ID:           p.ID,
	})
}

// LastReport is what the worker of a product said at its last sync, nil
// before the first one.
func LastReport(p *db.Product) *Report {
	var r Report
	if p.WorkerReport == "" || json.Unmarshal([]byte(p.WorkerReport), &r) != nil {
		return nil
	}
	return &r
}

// pull reads every page of the worker and returns what it runs with.
func pull(ctx context.Context, q *db.Queries, p *db.Product) (n int, runs *Report, err error) {
	current, err := publicKey(p)
	if err != nil {
		return 0, nil, err
	}

	// Rows a full round did not see are gone at the provider.
	round := time.Now().Unix()
	purchases := true
	cursor := ""
	asked := map[string]bool{}
	for {
		asked[cursor] = true
		pg, err := fetch(ctx, p, cursor)
		if err != nil {
			return 0, nil, err
		}
		runs = &pg.Product
		// A worker that was not deployed since a rotation still signs with the old key.
		ours := runs.PublicKey == current || slices.Contains(p.OldKeys(), runs.PublicKey)
		if runs.Prefix != p.Prefix || !ours {
			return 0, nil, errors.New("this worker signs for another product, check the worker url")
		}
		// The dashboard signs every row that says manual, so a worker must never write one.
		if runs.Provider == "manual" || slices.ContainsFunc(pg.Licenses, handIssued) {
			return 0, nil, errors.New(
				"this worker lists hand issued licenses, it is not a worker of inkan. Check the worker url",
			)
		}
		if slices.Contains(pg.Skipped, "purchase") {
			purchases = false
		}
		for _, l := range pg.Licenses {
			if err := store(ctx, q, p, cmp.Or(runs.Provider, "stripe"), l, round); err != nil {
				return 0, nil, err
			}
			n++
		}
		if pg.Next == nil {
			break
		}
		cursor = *pg.Next
		if asked[cursor] {
			return 0, nil, errors.New("worker: its pages go in a circle")
		}
		if len(asked) >= maxPages {
			return 0, nil, errors.New("worker: its list of licenses does not end")
		}
	}

	err = q.DeleteUnseenProviderLicenses(ctx, &db.DeleteUnseenProviderLicensesParams{
		ProductID: p.ID,
		SeenAt:    round,
		Purchases: purchases,
	})
	return n, runs, err
}

func publicKey(p *db.Product) (string, error) {
	priv, err := license.ParsePrivateKey(p.PrivateKey)
	if err != nil {
		return "", err
	}
	pub, _ := priv.Public().(ed25519.PublicKey)
	return hex.EncodeToString(pub), nil
}

func handIssued(l listed) bool {
	return strings.HasPrefix(l.ID, "lic_")
}

// CheckURL says whether the token may travel to a worker url. Plain http is
// only for a worker on this machine or network: localhost, a name without a
// dot like a Docker service, or a loopback or private IP address.
func CheckURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return errors.New("the worker url is no url")
	}
	host := u.Hostname()
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if ip, err := netip.ParseAddr(host); err == nil {
			if ip.IsLoopback() || ip.IsPrivate() {
				return nil
			}
		} else if !strings.Contains(host, ".") {
			return nil
		}
		return errors.New("the worker url needs https, plain http is only for localhost or a private address")
	default:
		return errors.New("the worker url needs https")
	}
}

func fetch(ctx context.Context, p *db.Product, cursor string) (*page, error) {
	if err := CheckURL(p.WorkerUrl); err != nil {
		return nil, err
	}
	query := url.Values{"cursor": {cursor}, "limit": {strconv.Itoa(pageSize)}}
	target := strings.TrimRight(p.WorkerUrl, "/") + "/licenses?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("worker: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.WorkerToken)

	resp, err := client.Do(req)
	if err != nil {
		return nil, unreachable(req.URL.Host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("worker: reading the answer: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return nil, errors.New("worker: it has another token, run inkan worker export again and deploy it")
	case http.StatusNotFound:
		if serves := health(ctx, p); serves != "" && !strings.Contains(serves, "licenses") {
			return nil, errors.New(
				"worker: it runs without INKAN_WORKER_TOKEN, so it serves no /licenses. Run inkan worker export again and deploy it",
			)
		}
		return nil, errors.New("worker: no /licenses there, check the worker url and that it has a token")
	default:
		if to := resp.Header.Get("Location"); to != "" {
			return nil, fmt.Errorf("worker: it redirects to %s, enter the url it really runs on", to)
		}
		said := strings.TrimSpace(string(body[:min(len(body), 200)]))
		return nil, fmt.Errorf("worker: %s: %s", resp.Status, said)
	}

	var pg page
	if err := json.Unmarshal(body, &pg); err != nil {
		return nil, fmt.Errorf("worker: reading the answer: %w", err)
	}
	return &pg, nil
}

// unreachable says in plain words why no answer came from host. What Go
// reports names the whole request, which is noise on the Products page.
func unreachable(host string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("worker: %w", err)
	}
	if e, ok := errors.AsType[*url.Error](err); ok {
		err = e.Err
	}
	_, unknown := errors.AsType[*net.DNSError](err)
	_, untrusted := errors.AsType[*tls.CertificateVerificationError](err)
	timeout, _ := errors.AsType[net.Error](err)
	switch {
	case unknown:
		return fmt.Errorf("worker: there is no host %s, check the worker url", host)
	case errors.Is(err, syscall.ECONNREFUSED):
		return fmt.Errorf("worker: %s takes no connection, is the worker running?", host)
	case untrusted:
		return fmt.Errorf("worker: the certificate of %s is not trusted", host)
	case timeout != nil && timeout.Timeout():
		return fmt.Errorf("worker: %s took too long to answer", host)
	}
	return fmt.Errorf("worker: %s could not be reached: %w", host, err)
}

// health is what the worker answers on /healthz, like "ok: refresh, licenses",
// or empty when nothing of ours answers there.
func health(ctx context.Context, p *db.Product) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.WorkerUrl, "/")+"/healthz", nil)
	if err != nil {
		return ""
	}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 200))
	if err != nil || resp.StatusCode != http.StatusOK || !strings.HasPrefix(string(body), "ok") {
		return ""
	}
	return string(body)
}

// drift says what the worker got from an older export. Only the keys, the
// plan and the name come from the dashboard, the rest is the worker's own.
func drift(p *db.Product, runs *Report) string {
	const again = " Run inkan worker export again and deploy it."
	current, _ := publicKey(p)
	switch {
	case runs.PublicKey != current:
		return "The worker still signs with the key from before the rotation." + again
	case !slices.Equal(runs.OldPublicKeys, p.OldKeys()):
		return "The worker takes other old keys than the product has." + again
	case runs.Plan != p.Plan:
		return fmt.Sprintf("The worker runs with the plan %q, the product has %q.", runs.Plan, p.Plan) + again
	case runs.Name != p.Name:
		return fmt.Sprintf("The worker calls the product %q, here it is %q.", runs.Name, p.Name) + again
	}
	return ""
}

func store(ctx context.Context, q *db.Queries, p *db.Product, provider string, l listed, round int64) error {
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
		Source:       provider,
		Kind:         l.Kind,
		Email:        l.Email,
		Customer:     l.Customer,
		CustomerGone: l.CustomerGone,
		Plan:         l.Plan,
		ExpiresAt:    l.ExpiresAt,
		Meta:         meta,
		Fingerprint:  l.Fingerprint,
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
	return q.InTx(ctx, func(q *db.Queries) error {
		if err := q.DeleteSnapshots(ctx, day); err != nil {
			return err
		}
		return q.TakeSnapshot(ctx, day)
	})
}
