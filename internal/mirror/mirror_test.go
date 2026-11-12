package mirror

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/mizuchilabs/inkan/license"

	"github.com/mizuchilabs/inkan/internal/db"
)

const token = "a-long-token-only-the-dashboard-knows"

// fakeWorker answers with the pages the real worker gives in its own tests,
// see worker/test/pages.json. That file is what the two sides agree on.
type fakeWorker struct {
	noToken bool
	pages   []page
	asked   []string // the cursors, in the order they came
	status  int      // when set, every answer is this and said
	said    string
}

func newWorker(t *testing.T) (*fakeWorker, string) {
	t.Helper()
	raw, err := os.ReadFile("../../worker/test/pages.json")
	require.NoError(t, err)
	w := &fakeWorker{}
	require.NoError(t, json.Unmarshal(raw, &w.pages))
	require.Len(t, w.pages, 3, "the subscriptions on two pages, then the one time purchases")
	srv := httptest.NewServer(w)
	t.Cleanup(srv.Close)
	return w, srv.URL
}

func (w *fakeWorker) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/healthz":
		_, _ = fmt.Fprint(rw, "ok: refresh, webhook", map[bool]string{false: ", licenses", true: ""}[w.noToken])
		return
	case r.URL.Path != "/licenses" || w.noToken:
		rw.WriteHeader(http.StatusNotFound)
		return
	case r.Header.Get("Authorization") != "Bearer "+token:
		rw.WriteHeader(http.StatusUnauthorized)
		return
	case w.status != 0:
		rw.WriteHeader(w.status)
		_, _ = rw.Write([]byte(w.said))
		return
	}
	cursor := r.URL.Query().Get("cursor")
	w.asked = append(w.asked, cursor)
	i := 0
	if cursor != "" {
		i = 1 + slices.IndexFunc(w.pages, func(pg page) bool { return pg.Next != nil && *pg.Next == cursor })
	}
	_ = json.MarshalWrite(rw, w.pages[i])
}

// privateKey is the one the worker signed the pages with, from the vectors of
// the license package.
func privateKey(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../../license/testdata/vectors.json")
	require.NoError(t, err)
	var vectors struct {
		Seed string `json:"seed"`
	}
	require.NoError(t, json.Unmarshal(raw, &vectors))
	seed, err := hex.DecodeString(vectors.Seed)
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(ed25519.NewKeyFromSeed(seed))
}

// newProduct has the name, plan and key the worker of the pages got.
func newProduct(t *testing.T, q *db.Queries, workerURL string) *db.Product {
	t.Helper()
	ctx := t.Context()
	p, err := q.CreateProduct(ctx, &db.CreateProductParams{
		Name: "My <App>", Prefix: "MYAPP", Plan: "pro", PrivateKey: privateKey(t), WorkerToken: token,
	})
	require.NoError(t, err)
	assert.False(t, Connected(p))
	p, err = q.UpdateProduct(ctx, &db.UpdateProductParams{
		Name:      p.Name,
		Plan:      p.Plan,
		WorkerUrl: workerURL,
		ID:        p.ID,
	})
	require.NoError(t, err)
	assert.True(t, Connected(p))
	return p
}

// The fixture ids follow Stripe's shapes, a cs_ id is a purchase.
func stale(t *testing.T, q *db.Queries, p *db.Product, id, email string) {
	t.Helper()
	kind := "subscription"
	if strings.HasPrefix(id, "cs_") {
		kind = "purchase"
	}
	require.NoError(t, q.UpsertProviderLicense(t.Context(), &db.UpsertProviderLicenseParams{
		ID:        id,
		ProductID: p.ID,
		Source:    "stripe",
		Kind:      kind,
		Email:     email,
		Customer:  "Acme",
		Plan:      "pro",
		Meta:      "{}",
		SeenAt:    1,
	}))
}

func has(t *testing.T, q *db.Queries, p *db.Product, id string) bool {
	t.Helper()
	_, err := q.GetLicense(t.Context(), &db.GetLicenseParams{ProductID: p.ID, ID: id})
	return err == nil
}

func TestSync(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	q, closeDB, err := db.Open(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(closeDB)
	w, url := newWorker(t)
	p := newProduct(t, q, url+"/")

	n, err := Sync(ctx, q, p)
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	assert.Equal(t, []string{"", "s:sub_unpaid", "p:"}, w.asked, "every page is read")

	priv, err := license.ParsePrivateKey(p.PrivateKey)
	require.NoError(t, err)
	pub, _ := priv.Public().(ed25519.PublicKey)
	sub, err := q.GetLicense(ctx, &db.GetLicenseParams{ProductID: p.ID, ID: "sub_ok_1"})
	require.NoError(t, err)
	assert.Equal(t, "stripe", sub.Source)
	assert.Equal(t, "ops@acme.io", sub.Email)
	assert.Equal(t, "Acme", sub.Customer)
	assert.Equal(t, "team", sub.Plan)
	assert.Equal(t, int64(4_000_000_000+14*86400), sub.ExpiresAt)
	assert.Equal(t, "active", sub.Status)
	assert.True(t, sub.Renews)
	assert.Equal(t, int64(1_700_000_000), sub.CreatedAt)
	claims, err := license.VerifyWith(sub.Key, "MYAPP", "", pub)
	require.NoError(t, err, "the row holds the key the worker hands out")
	assert.Equal(t, "sub_ok_1", claims.ID)
	assert.Equal(t, sub.ExpiresAt, claims.ExpiresAt)

	bought, err := q.GetLicense(ctx, &db.GetLicenseParams{ProductID: p.ID, ID: "cs_1"})
	require.NoError(t, err)
	assert.Zero(t, bought.ExpiresAt, "a one time purchase never expires")
	assert.Equal(t, "purchase", bought.Kind)
	assert.Equal(t, "paid", bought.Status)
	assert.JSONEq(t, `{"seats": "3"}`, bought.Meta)

	gone, err := q.GetLicense(ctx, &db.GetLicenseParams{ProductID: p.ID, ID: "sub_deleted"})
	require.NoError(t, err)
	assert.Equal(t, "machine-1", gone.Fingerprint, "the row knows which machine the key is bound to")

	_, err = q.SetLicenseNote(ctx, &db.SetLicenseNoteParams{Note: "vip", ProductID: p.ID, ID: "sub_ok_1"})
	require.NoError(t, err)
	_, err = Sync(ctx, q, p)
	require.NoError(t, err)
	sub, err = q.GetLicense(ctx, &db.GetLicenseParams{ProductID: p.ID, ID: "sub_ok_1"})
	require.NoError(t, err)
	assert.Equal(t, "vip", sub.Note, "a sync must not wipe the note")

	p, err = q.GetProduct(ctx, p.ID)
	require.NoError(t, err)
	assert.NotNil(t, p.SyncedAt)
	assert.Empty(t, p.SyncError)

	runs := LastReport(p)
	require.NotNil(t, runs, "what the worker runs with is kept for the UI")
	assert.Equal(t, "prod_app=team,prod_lifetime=lifetime,prod_plain=pro", runs.ProviderProduct)
	assert.Equal(t, int64(14), runs.GraceDays)
	require.NotNil(t, runs.Mail, "this worker mails keys")
	assert.Equal(t, "Keys <keys@example.com>", runs.Mail.From)
	assert.Equal(t, "support@example.com", runs.Mail.ReplyTo)
	assert.False(t, runs.Mail.Custom)

	p, err = q.UpdateProduct(ctx, &db.UpdateProductParams{Name: p.Name, Plan: p.Plan, ID: p.ID})
	require.NoError(t, err)
	assert.Nil(t, LastReport(p), "a product without a worker shows no report")

	require.NoError(t, snapshot(ctx, q))
	require.NoError(t, snapshot(ctx, q), "a second count on the same day replaces the first")
	snapshots, err := q.ListSnapshots(ctx, "2000-01-01")
	require.NoError(t, err)
	require.Len(t, snapshots, 2, "one count per plan")
}

// What the worker no longer lists is gone at Stripe, or was never of this
// product. The rows must not stay forever.
func TestSyncDropsWhatTheWorkerNoLongerLists(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	q, closeDB, err := db.Open(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(closeDB)
	w, url := newWorker(t)
	p := newProduct(t, q, url)

	stale(t, q, p, "sub_test_mode", "a@x.io")
	stale(t, q, p, "cs_test_mode", "a@x.io")
	_, err = q.CreateLicense(ctx, &db.CreateLicenseParams{
		ID: "lic_manual", ProductID: p.ID, Email: "a@x.io", Plan: "pro", Meta: "{}",
	})
	require.NoError(t, err)

	// This worker may not read Checkout sessions, so it knows nothing about purchases.
	w.pages[2].Licenses = nil
	w.pages[2].Skipped = []string{"purchase"}
	_, err = Sync(ctx, q, p)
	require.NoError(t, err)
	assert.False(t, has(t, q, p, "sub_test_mode"))
	assert.True(t, has(t, q, p, "cs_test_mode"), "purchases stay while nobody can say they are gone")
	assert.True(t, has(t, q, p, "lic_manual"), "manual licenses are not the worker's")

	w.pages[2].Skipped = nil
	_, err = Sync(ctx, q, p)
	require.NoError(t, err)
	assert.False(t, has(t, q, p, "cs_test_mode"))
	assert.True(t, has(t, q, p, "sub_ok_1"))
}

// Stripe keeps only the id of a deleted customer. The row must not lose who
// the license was for.
func TestSyncDeletedCustomer(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	q, closeDB, err := db.Open(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(closeDB)
	_, url := newWorker(t)
	p := newProduct(t, q, url)
	stale(t, q, p, "sub_deleted", "left@acme.io")

	_, err = Sync(ctx, q, p)
	require.NoError(t, err)
	l, err := q.GetLicense(ctx, &db.GetLicenseParams{ProductID: p.ID, ID: "sub_deleted"})
	require.NoError(t, err)
	assert.True(t, l.CustomerGone)
	assert.Equal(t, "left@acme.io", l.Email)
	assert.Equal(t, "Acme", l.Customer)
	assert.Equal(t, "canceled", l.Status)
}

func TestSyncFailures(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	q, closeDB, err := db.Open(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(closeDB)
	w, url := newWorker(t)
	p := newProduct(t, q, url)
	stale(t, q, p, "sub_kept", "a@x.io")

	failed := func(wantErr, why string) {
		t.Helper()
		_, err := Sync(ctx, q, p)
		require.ErrorContains(t, err, wantErr, why)
		stored, err := q.GetProduct(ctx, p.ID)
		require.NoError(t, err)
		assert.Contains(t, stored.SyncError, wantErr, "the failure is kept for the UI")
		assert.True(t, has(t, q, p, "sub_kept"), "a sync that failed drops nothing")
	}

	w.status, w.said = http.StatusBadGateway, "stripe: Invalid API Key provided"
	failed("Invalid API Key provided", "Stripe's own words reach the UI")
	w.status = 0

	p.WorkerToken = "another-token-that-is-long-enough-too"
	failed("another token", "the worker turns a wrong token down")
	p.WorkerToken = token

	p.WorkerUrl = url + "/nothing-here"
	failed("no /licenses there", "a wrong url is named as such")
	p.WorkerUrl = url

	w.noToken = true
	failed("without INKAN_WORKER_TOKEN", "a worker without a token says so through /healthz")
	w.noToken = false

	w.pages[1].Next = new("s:sub_unpaid")
	failed("in a circle", "a broken worker must not keep the sync going forever")
	w.pages[1].Next = new("p:")

	w.pages[0].Product.PublicKey = hex.EncodeToString(make([]byte, ed25519.PublicKeySize))
	failed("another product", "its keys would not verify in the app")
}

// The plan and the name reach the worker through an export. One that was not
// deployed since runs with the old ones.
func TestSyncNoticesAnOldExport(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	q, closeDB, err := db.Open(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(closeDB)
	w, url := newWorker(t)
	p := newProduct(t, q, url)
	stale(t, q, p, "sub_stale", "a@x.io")
	notice := func() string {
		t.Helper()
		n, err := Sync(ctx, q, p)
		require.NoError(t, err, "the licenses are still what the worker hands out")
		assert.Equal(t, 3, n)
		stored, err := q.GetProduct(ctx, p.ID)
		require.NoError(t, err)
		assert.NotNil(t, stored.SyncedAt)
		return stored.SyncError
	}

	p.Plan = "team"
	assert.Contains(t, notice(), `plan "pro"`)
	assert.False(t, has(t, q, p, "sub_stale"), "a notice does not stop the sync")
	p.Plan = "pro"

	p.Name = "Renamed"
	assert.Contains(t, notice(), `calls the product "My <App>"`)
	p.Name = "My <App>"

	// What the worker sets by itself is no drift, it has no other home.
	for i := range w.pages {
		w.pages[i].Product.GraceDays = 7
		w.pages[i].Product.MaxKeyDays = 30
		w.pages[i].Product.ProviderProduct = "prod_other"
		w.pages[i].Product.Mail = nil
	}
	assert.Empty(t, notice())
	stored, err := q.GetProduct(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(30), LastReport(stored).MaxKeyDays)
	assert.Nil(t, LastReport(stored).Mail)
}

// After a rotation the worker keeps signing with the old key until its env is
// exported and deployed. Its licenses are still the product's.
func TestSyncAfterRotation(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	q, closeDB, err := db.Open(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(closeDB)
	w, url := newWorker(t)
	p := newProduct(t, q, url)
	sync := func() string {
		t.Helper()
		n, err := Sync(ctx, q, p)
		require.NoError(t, err)
		assert.Equal(t, 3, n)
		stored, err := q.GetProduct(ctx, p.ID)
		require.NoError(t, err)
		return stored.SyncError
	}

	fresh, freshPub, err := license.GenerateKey()
	require.NoError(t, err)
	workerPub := w.pages[0].Product.PublicKey
	p, err = q.SetProductKeys(ctx, &db.SetProductKeysParams{PrivateKey: fresh, OldPublicKeys: workerPub, ID: p.ID})
	require.NoError(t, err)
	assert.Contains(t, sync(), "still signs with the key from before the rotation")
	assert.True(t, has(t, q, p, "sub_ok_1"))

	// Deployed with the new key, but without the list of old ones.
	for i := range w.pages {
		w.pages[i].Product.PublicKey = freshPub
	}
	assert.Contains(t, sync(), "other old keys")

	for i := range w.pages {
		w.pages[i].Product.OldPublicKeys = []string{workerPub}
	}
	assert.Empty(t, sync())
}

// A worker owns the paid rows only. What was issued by hand stays as it is.
func TestSyncLeavesHandIssuedRowsAlone(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	q, closeDB, err := db.Open(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(closeDB)
	w, url := newWorker(t)
	p := newProduct(t, q, url)

	byHand, err := q.CreateLicense(ctx, &db.CreateLicenseParams{
		ID: "lic_manual", ProductID: p.ID, Email: "a@x.io", Customer: "Acme", Plan: "pro",
		ExpiresAt: 4_000_000_000, Meta: `{"seats":"2"}`, Fingerprint: "machine-9", Note: "by hand",
	})
	require.NoError(t, err)
	stale(t, q, p, "sub_kept", "a@x.io")
	unchanged := func() {
		t.Helper()
		now, err := q.GetLicense(ctx, &db.GetLicenseParams{ProductID: p.ID, ID: "lic_manual"})
		require.NoError(t, err)
		assert.Equal(t, byHand, now, "the hand issued row is as it was")
	}

	_, err = Sync(ctx, q, p)
	require.NoError(t, err)
	unchanged()

	refused := func(why string) {
		t.Helper()
		stale(t, q, p, "sub_kept", "a@x.io")
		_, err := Sync(ctx, q, p)
		require.ErrorContains(t, err, "hand issued", why)
		unchanged()
		assert.True(t, has(t, q, p, "sub_kept"), "a refused sync drops nothing")
		all, err := q.ListLicenses(ctx, &db.ListLicensesParams{Limit: -1})
		require.NoError(t, err)
		for _, l := range all {
			assert.True(
				t,
				l.Source != "manual" || l.ID == "lic_manual",
				"no hand issued row came from the worker: %s",
				l.ID,
			)
		}
	}

	// Whoever answers at the worker url must not get a key signed by the dashboard.
	for i := range w.pages {
		w.pages[i].Product.Provider = "manual"
	}
	refused("a worker that calls itself manual would make hand issued rows")
	for i := range w.pages {
		w.pages[i].Product.Provider = "stripe"
	}

	mine := w.pages[0].Licenses[0]
	w.pages[0].Licenses[0].ID = "lic_manual"
	w.pages[0].Licenses[0].Plan = "enterprise"
	w.pages[0].Licenses[0].ExpiresAt = 0
	refused("a worker that lists a hand issued id would rewrite its row")
	w.pages[0].Licenses[0] = mine
}

func TestCheckURL(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{
		"https://worker.example.com",
		"https://worker.example.com/myapp/",
		"http://localhost:4242",
		"http://127.0.0.1:4242",
		"http://[::1]:4242",
		"http://10.0.0.7",
		"http://192.168.1.20:8080",
		"http://worker:8080",
	} {
		require.NoError(t, CheckURL(ok), ok)
	}
	for _, bad := range []string{
		"http://worker.example.com",
		"http://8.8.8.8",
		"http://localhost.example.com",
		"http://host.docker.internal",
		"ftp://worker.example.com",
		"worker.example.com",
		"https://",
		"",
	} {
		assert.Error(t, CheckURL(bad), bad)
	}
}

// The token must not follow a redirect, it could lead to plain http or to
// another host.
func TestSyncFollowsNoRedirect(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	q, closeDB, err := db.Open(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(closeDB)

	reached := false
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	t.Cleanup(elsewhere.Close)
	moved := httptest.NewServer(http.RedirectHandler(elsewhere.URL, http.StatusFound))
	t.Cleanup(moved.Close)
	p := newProduct(t, q, moved.URL)

	_, err = Sync(ctx, q, p)
	require.ErrorContains(t, err, "redirects to "+elsewhere.URL)
	assert.False(t, reached, "the token stayed where it was sent")

	p.WorkerUrl = "http://worker.example.com"
	_, err = Sync(ctx, q, p)
	require.ErrorContains(t, err, "needs https", "a url from before the rule is not called either")
}
