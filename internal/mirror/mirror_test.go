package mirror

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json/v2"
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
	pages  []page
	asked  []string // the cursors, in the order they came
	status int      // when set, every answer is this and said
	said   string
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
	case r.URL.Path != "/licenses":
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

// newProduct is set up the way the worker of the pages runs.
func newProduct(t *testing.T, q *db.Queries, workerURL string) *db.Product {
	t.Helper()
	ctx := t.Context()
	p, err := q.CreateProduct(ctx, &db.CreateProductParams{
		Name: "App", Prefix: "MYAPP", Plan: "pro", GraceDays: 14, PrivateKey: privateKey(t), WorkerToken: token,
	})
	require.NoError(t, err)
	assert.False(t, Connected(p))
	p, err = q.UpdateProduct(ctx, &db.UpdateProductParams{
		Name: p.Name, Plan: p.Plan, GraceDays: p.GraceDays, ProviderProduct: "prod_app", WorkerUrl: workerURL, ID: p.ID,
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

	w.pages[1].Next = new("s:sub_unpaid")
	failed("in a circle", "a broken worker must not keep the sync going forever")
	w.pages[1].Next = new("p:")

	w.pages[0].Product.PublicKey = hex.EncodeToString(make([]byte, ed25519.PublicKeySize))
	failed("another product", "its keys would not verify in the app")
}

// The worker only gets its settings when its env is exported and deployed, so
// it can run with other ones than the product shows.
func TestSyncNoticesOtherSettings(t *testing.T) {
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

	for i := range w.pages {
		w.pages[i].Product.ProviderProduct = "prod_typo"
	}
	assert.Contains(t, notice(), "stripe product prod_typo")
	assert.True(t, has(t, q, p, "sub_stale"), "a worker on another Stripe product drops nothing here")

	for i := range w.pages {
		w.pages[i].Product.ProviderProduct = "prod_app"
		w.pages[i].Product.GraceDays = 7
		w.pages[i].Product.MaxKeyDays = 30
	}
	said := notice()
	assert.Contains(t, said, "7 grace days")
	assert.Contains(t, said, "30 max key days")
	assert.False(t, has(t, q, p, "sub_stale"))

	for i := range w.pages {
		w.pages[i].Product.GraceDays = 14
		w.pages[i].Product.MaxKeyDays = 0
	}
	assert.Empty(t, notice())
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
