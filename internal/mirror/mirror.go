package mirror

import (
	"cmp"
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mizuchilabs/inkan/internal/db"
)

const (
	syncEvery = time.Hour
	// The most a worker hands out.
	pageSize = 100
	// A million licenses, so a worker that never ends its list cannot keep a sync going.
	maxPages = 10_000
)

// A redirect could take the token to plain http or another host.
var client = &http.Client{
	Timeout:       30 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

type page struct {
	Product  Report   `json:"product"`
	Licenses []listed `json:"licenses"`
	Skipped  []string `json:"skipped"`
	Next     *string  `json:"next"`
}

type Report struct {
	Provider        string   `json:"provider"         doc:"Who the worker asks what is paid, like stripe"`
	Prefix          string   `json:"prefix"`
	PublicKey       string   `json:"public_key"       doc:"Hex public key of the key the worker signs with"`
	OldPublicKeys   []string `json:"old_public_keys"  doc:"Keys from before a rotation that a refresh still takes"                             nullable:"false"`
	Plan            string   `json:"plan"             doc:"Plan for Stripe products that set none"`
	Name            string   `json:"name"             doc:"How the key mail calls the product"`
	GraceDays       int64    `json:"grace_days"       doc:"Days a renewal has to get paid"`
	MaxKeyDays      int64    `json:"max_key_days"     doc:"Days a paid key lives before the app has to refresh, 0 has no limit"`
	ProviderProduct string   `json:"provider_product" doc:"The products at the provider with the plan each gives, like prod_a=team,prod_b=pro"`
	Mail            *Mail    `json:"mail,omitempty"   doc:"The key mail, missing when the worker sends none"`
}

type Mail struct {
	From    string `json:"from"`
	ReplyTo string `json:"reply_to"`
	Footer  string `json:"footer"`
	Company string `json:"company"`
	Custom  bool   `json:"custom"   doc:"The worker sends a mail of its own, not the built-in one"`
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

type Mirror struct {
	q    *db.Queries
	wake chan struct{}

	mu      sync.Mutex
	asked   map[int64]bool
	reading int64
}

func New(q *db.Queries) *Mirror {
	return &Mirror{q: q, wake: make(chan struct{}, 1), asked: map[int64]bool{}}
}

func (m *Mirror) Run(ctx context.Context) {
	every := time.Tick(syncEvery)
	m.readAll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-every:
			m.readAll(ctx)
		case <-m.wake:
			m.readAsked(ctx)
		}
	}
}

func (m *Mirror) SyncSoon(id int64) {
	m.mu.Lock()
	m.asked[id] = true
	m.mu.Unlock()
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Mirror) Syncing(id int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.asked[id] || m.reading == id
}

func (m *Mirror) readAsked(ctx context.Context) {
	m.mu.Lock()
	ids := slices.Collect(maps.Keys(m.asked))
	m.mu.Unlock()
	for _, id := range ids {
		m.read(ctx, id)
	}
}

func (m *Mirror) readAll(ctx context.Context) {
	products, err := m.q.ListProducts(ctx)
	if err != nil && ctx.Err() == nil {
		slog.Error("worker sync: listing products failed", "error", err)
	}
	for _, p := range products {
		// Whoever asked is waiting, the round is not.
		m.readAsked(ctx)
		m.read(ctx, p.ID)
	}
	if err := snapshot(ctx, m.q); err != nil && ctx.Err() == nil {
		slog.Error("taking the daily snapshot failed", "error", err)
	}
}

// Cleared before the read, so a product asked for again meanwhile is read once more.
func (m *Mirror) read(ctx context.Context, id int64) {
	m.mu.Lock()
	delete(m.asked, id)
	m.reading = id
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.reading = 0
		m.mu.Unlock()
	}()

	p, err := m.q.GetProduct(ctx, id)
	if err != nil {
		// The product may be deleted by now, that is no failure.
		if !errors.Is(err, sql.ErrNoRows) && ctx.Err() == nil {
			slog.Error("worker sync: loading the product failed", "product", id, "error", err)
		}
		return
	}
	if !Connected(p) {
		return
	}
	n, err := syncProduct(ctx, m.q, p)
	if err != nil {
		slog.Warn("worker sync failed", "product", p.Name, "error", err)
		return
	}
	slog.Debug("worker sync done", "product", p.Name, "licenses", n)
}

func Connected(p *db.Product) bool {
	return p.WorkerUrl != ""
}

func syncProduct(ctx context.Context, q *db.Queries, p *db.Product) (int, error) {
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
		return 0, fmt.Errorf("encoding the worker report: %w", err)
	}
	return n, q.SetProductSynced(ctx, &db.SetProductSyncedParams{WorkerReport: string(report), ID: p.ID})
}

func LastReport(p *db.Product) *Report {
	var r Report
	if p.WorkerReport == "" || json.Unmarshal([]byte(p.WorkerReport), &r) != nil {
		return nil
	}
	return &r
}

func pull(ctx context.Context, q *db.Queries, p *db.Product) (n int, runs *Report, err error) {
	current, err := p.PublicKey()
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

func handIssued(l listed) bool {
	return strings.HasPrefix(l.ID, "lic_")
}

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
		} else if !strings.Contains(host, ".") { // localhost or a Docker service name
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

// Drift only compares what an export sets: the keys, the plan and the name.
func Drift(p *db.Product, runs *Report) string {
	if runs == nil {
		return ""
	}
	const again = " Run inkan worker export again and deploy it."
	current, _ := p.PublicKey()
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

// Rewritten every round, so a day ends on its last count.
func snapshot(ctx context.Context, q *db.Queries) error {
	day := time.Now().UTC().Format(time.DateOnly)
	return q.InTx(ctx, func(q *db.Queries) error {
		if err := q.DeleteSnapshots(ctx, day); err != nil {
			return err
		}
		return q.TakeSnapshot(ctx, day)
	})
}
