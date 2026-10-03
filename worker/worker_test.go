package worker

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/mizuchilabs/inkan/internal/api"
	"github.com/mizuchilabs/inkan/internal/db"
	"github.com/mizuchilabs/inkan/internal/mirror"
	"github.com/mizuchilabs/inkan/internal/stripe"
	"github.com/mizuchilabs/inkan/license"
)

const (
	periodEnd = 1_800_000_000
	grace     = 14 * 86400
	secret    = "whsec_test"

	paidSub = `{"id": "sub_1", "status": "active", "created": 1700000000,
		"customer": {"email": "ops@acme.io", "name": "Acme"},
		"items": {"data": [{"price": {"product": "prod_app", "metadata": {"plan": "team"}},
			"current_period_start": 1797000000, "current_period_end": 1800000000}]}}`
)

// fakeStripe knows one paid subscription of the product and a few odd ones.
func fakeStripe(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/subscriptions":
			_, _ = w.Write([]byte(`{"has_more": false, "data": [` + paidSub + `]}`))
		case "/v1/subscriptions/sub_1":
			_, _ = w.Write([]byte(paidSub))
		case "/v1/subscriptions/sub_other":
			_, _ = w.Write([]byte(`{"id": "sub_other", "status": "active", "customer": {"email": "x@x.io"},
				"items": {"data": [{"price": {"product": "prod_other"}, "current_period_end": 1800000000}]}}`))
		case "/v1/subscriptions/sub_unpaid":
			_, _ = w.Write([]byte(`{"id": "sub_unpaid", "status": "incomplete", "customer": {"email": "x@x.io"},
				"items": {"data": [{"price": {"product": "prod_app"}, "current_period_end": 1800000000}]}}`))
		case "/v1/subscriptions/sub_down":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	live := stripe.APIURL
	stripe.APIURL = srv.URL                    //nolint:reassign // the var exists so tests can fake Stripe
	t.Cleanup(func() { stripe.APIURL = live }) //nolint:reassign // see above
}

func testConfig(t *testing.T) (Config, ed25519.PrivateKey) {
	t.Helper()
	privateKey, _, err := license.GenerateKey()
	require.NoError(t, err)
	priv, err := license.ParsePrivateKey(privateKey)
	require.NoError(t, err)
	return Config{
		Prefix:        "APP",
		PrivateKey:    privateKey,
		StripeKey:     "sk_test",
		StripeProduct: "prod_app",
		Plan:          "pro",
		GraceDays:     14,
	}, priv
}

func post(t *testing.T, h http.Handler, path, body string, header http.Header) (int, string) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(body))
	req.Header = header
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, strings.TrimSpace(rec.Body.String())
}

func TestNew(t *testing.T) {
	t.Parallel()
	cfg, _ := testConfig(t)

	h, err := New(cfg)
	require.NoError(t, err)
	status, _ := post(t, h, "/webhook", "{}", nil)
	assert.Equal(t, http.StatusNotFound, status, "without a secret there is no webhook")

	broken := cfg
	broken.PrivateKey = "nope"
	_, err = New(broken)
	require.Error(t, err)

	broken = cfg
	broken.StripeProduct = ""
	_, err = New(broken)
	require.Error(t, err)

	broken = cfg
	broken.WebhookSecret = secret
	_, err = New(broken)
	require.Error(t, err, "a webhook that cannot send keys would swallow paid invoices")
}

func TestRefresh(t *testing.T) {
	fakeStripe(t)
	cfg, priv := testConfig(t)
	h, err := New(cfg)
	require.NoError(t, err)
	pub, _ := priv.Public().(ed25519.PublicKey)

	issue := func(claims license.Claims, with ed25519.PrivateKey) string {
		key, err := license.Issue("APP", claims, with)
		require.NoError(t, err)
		return key
	}

	// The key of last period, already run out.
	old := issue(license.Claims{Plan: "pro", Email: "old@acme.io", ExpiresAt: 1_700_000_000, ID: "sub_1"}, priv)
	status, key := post(t, h, "/refresh", old+"\n", nil)
	require.Equal(t, http.StatusOK, status, "an expired key still refreshes, the renewal may come late")
	claims, err := license.VerifyWith(key, "APP", pub)
	require.NoError(t, err)
	assert.Equal(t, int64(periodEnd+grace), claims.ExpiresAt)
	assert.Equal(t, "team", claims.Plan, "the plan follows the price")
	assert.Equal(t, "ops@acme.io", claims.Email)
	assert.Equal(t, "sub_1", claims.ID)

	status, again := post(t, h, "/refresh", key, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, key, again, "nothing new was paid, so the key stays the same")

	manual := issue(license.Claims{Plan: "pro", Email: "a@x.io", ID: "lic_abc"}, priv)
	status, key = post(t, h, "/refresh", manual, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, manual, key, "a hand issued key comes back as it is")

	_, otherPriv := testConfig(t)
	forged := issue(license.Claims{Plan: "pro", ID: "sub_1"}, otherPriv)
	status, _ = post(t, h, "/refresh", forged, nil)
	assert.Equal(t, http.StatusBadRequest, status, "only keys signed by the product reach Stripe")
	status, _ = post(t, h, "/refresh", "APP-AAAAA", nil)
	assert.Equal(t, http.StatusBadRequest, status)

	for id, want := range map[string]int{
		"sub_gone":   http.StatusNotFound,
		"sub_other":  http.StatusNotFound,
		"sub_unpaid": http.StatusNotFound,
		"sub_down":   http.StatusBadGateway,
	} {
		status, body := post(t, h, "/refresh", issue(license.Claims{Plan: "pro", ID: id}, priv), nil)
		assert.Equal(t, want, status, id)
		assert.NotContains(t, body, "APP-", "no key goes out for %s", id)
	}
}

func signature(body string, at time.Time) http.Header {
	stamp := strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(stamp + "." + body))
	return http.Header{"Stripe-Signature": {"t=" + stamp + ",v1=" + hex.EncodeToString(mac.Sum(nil)) + ",v0=old"}}
}

func TestWebhook(t *testing.T) {
	fakeStripe(t)
	cfg, priv := testConfig(t)
	cfg.WebhookSecret = secret
	var sent []string
	var sendErr error
	cfg.Send = func(_ context.Context, key string, claims license.Claims) error {
		sent = append(sent, claims.Email+" "+key)
		return sendErr
	}
	h, err := New(cfg)
	require.NoError(t, err)

	want, err := license.Issue("APP", license.Claims{
		Plan: "team", Email: "ops@acme.io", ExpiresAt: periodEnd + grace, ID: "sub_1",
	}, priv)
	require.NoError(t, err)
	paid := func(sub string) string {
		return `{"type": "invoice.paid", "data": {"object": {
			"parent": {"subscription_details": {"subscription": "` + sub + `"}}}}}`
	}
	now := time.Now()

	body := paid("sub_1")
	status, _ := post(t, h, "/webhook", body, signature(body, now))
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, []string{"ops@acme.io " + want}, sent, "the customer gets the key the ledger shows")

	body = `{"type": "invoice.paid", "data": {"object": {"subscription": "sub_1", "parent": null}}}`
	status, _ = post(t, h, "/webhook", body, signature(body, now))
	require.Equal(t, http.StatusOK, status)
	require.Len(t, sent, 2, "older API versions name the subscription on the invoice")

	// None of these owe a mail.
	for name, body := range map[string]string{
		"another event":          `{"type": "invoice.created", "data": {"object": {"subscription": "sub_1"}}}`,
		"a one off invoice":      `{"type": "invoice.paid", "data": {"object": {"parent": null}}}`,
		"another product's sub.": paid("sub_other"),
	} {
		status, _ = post(t, h, "/webhook", body, signature(body, now))
		assert.Equal(t, http.StatusOK, status, name)
	}
	assert.Len(t, sent, 2)

	body = paid("sub_1")
	status, _ = post(t, h, "/webhook", body, signature(paid("sub_other"), now))
	assert.Equal(t, http.StatusBadRequest, status, "the signature covers the body")
	status, _ = post(t, h, "/webhook", body, signature(body, now.Add(-time.Hour)))
	assert.Equal(t, http.StatusBadRequest, status, "an old event cannot be replayed")
	status, _ = post(t, h, "/webhook", body, nil)
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Len(t, sent, 2)

	// Stripe retries on an error, so these keys are not lost.
	body = paid("sub_unpaid")
	status, _ = post(t, h, "/webhook", body, signature(body, now))
	assert.Equal(t, http.StatusInternalServerError, status, "paid invoice, subscription not active yet")
	sendErr = errors.New("mail server is down")
	body = paid("sub_1")
	status, _ = post(t, h, "/webhook", body, signature(body, now))
	assert.Equal(t, http.StatusInternalServerError, status)
}

// The promise of the whole design: for the same subscription the worker hands
// out the very key the ledger shows.
func TestWorkerAndLedgerAgree(t *testing.T) {
	fakeStripe(t)
	cfg, _ := testConfig(t)
	ctx := t.Context()

	q, err := db.Open(ctx, t.TempDir())
	require.NoError(t, err)
	p, err := q.CreateProduct(ctx, &db.CreateProductParams{
		Name: "App", Prefix: cfg.Prefix, Plan: cfg.Plan, GraceDays: cfg.GraceDays, PrivateKey: cfg.PrivateKey,
	})
	require.NoError(t, err)
	p, err = q.UpdateProduct(ctx, &db.UpdateProductParams{
		Name: p.Name, Plan: p.Plan, GraceDays: p.GraceDays,
		StripeKey: cfg.StripeKey, StripeProduct: cfg.StripeProduct, ID: p.ID,
	})
	require.NoError(t, err)
	_, err = mirror.Sync(ctx, q, p)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	api.New(q).Handler().ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/licenses", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var ledger []api.License
	require.NoError(t, json.UnmarshalRead(rec.Body, &ledger))
	require.Len(t, ledger, 1)

	h, err := New(cfg)
	require.NoError(t, err)
	status, key := post(t, h, "/refresh", ledger[0].Key, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, ledger[0].Key, key)
}

// The client an app calls and the handler have to speak the same language.
func TestRefreshClient(t *testing.T) {
	fakeStripe(t)
	cfg, priv := testConfig(t)
	h, err := New(cfg)
	require.NoError(t, err)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	pub, _ := priv.Public().(ed25519.PublicKey)
	require.NoError(t, license.SetPublicKey(hex.EncodeToString(pub)))
	t.Cleanup(func() { _ = license.SetPublicKey("") })

	old, err := license.Issue("APP", license.Claims{Plan: "pro", ExpiresAt: 1_700_000_000, ID: "sub_1"}, priv)
	require.NoError(t, err)
	key, err := license.Refresh(t.Context(), srv.URL+"/refresh", old, "APP")
	require.NoError(t, err)
	claims, err := license.Verify(key, "APP")
	require.NoError(t, err, "the app ends up with a key that is good again")
	assert.Equal(t, int64(periodEnd+grace), claims.ExpiresAt)

	gone, err := license.Issue("APP", license.Claims{Plan: "pro", ID: "sub_gone"}, priv)
	require.NoError(t, err)
	_, err = license.Refresh(t.Context(), srv.URL+"/refresh", gone, "APP")
	require.ErrorContains(t, err, "no license behind this key", "the worker's own words reach the app")
}

type smtpServer struct {
	addr  string
	lines chan []string
}

// fakeSMTP speaks just enough SMTP for one plain mail.
func fakeSMTP(t *testing.T) smtpServer {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	srv := smtpServer{addr: ln.Addr().String(), lines: make(chan []string, 1)}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		text := textproto.NewConn(conn)
		_ = text.PrintfLine("220 fake")
		var got []string
		for {
			line, err := text.ReadLine()
			if err != nil {
				return
			}
			got = append(got, line)
			switch line {
			case "DATA":
				_ = text.PrintfLine("354 go ahead")
				data, _ := text.ReadDotLines()
				got = append(got, data...)
				_ = text.PrintfLine("250 ok")
			case "QUIT":
				_ = text.PrintfLine("221 bye")
				srv.lines <- got
				return
			default:
				_ = text.PrintfLine("250 ok")
			}
		}
	}()
	return srv
}

func TestSMTPSend(t *testing.T) {
	t.Parallel()
	srv := fakeSMTP(t)
	sender := SMTP{Addr: srv.addr, From: "Acme Keys <keys@acme.io>"}
	claims := license.Claims{App: "APP", Plan: "team", Email: "ops@acme.io", ExpiresAt: periodEnd}

	require.NoError(t, sender.Send(t.Context(), "APP-AAAAA-BBBBB", claims))
	lines := <-srv.lines
	assert.Contains(t, lines, "MAIL FROM:<keys@acme.io>")
	assert.Contains(t, lines, "RCPT TO:<ops@acme.io>")
	assert.Contains(t, lines, "Subject: Your APP license key")
	assert.Contains(t, lines, "APP-AAAAA-BBBBB")
	assert.Contains(t, lines, "It is valid until 15 January 2027. A new key is sent after every payment.")

	claims.Email = "ops@acme.io\r\nBcc: thief@evil.io"
	err := sender.Send(t.Context(), "APP-AAAAA-BBBBB", claims)
	require.Error(t, err, "an address from Stripe must not be able to add headers")
}
