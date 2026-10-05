package worker

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/textproto"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

	paidSession = `{"id": "cs_1", "mode": "payment", "payment_status": "paid", "created": 1780000000,
		"customer_details": {"email": "buyer@acme.io", "name": "Buyer"},
		"payment_intent": {"latest_charge": {"refunded": false}},
		"line_items": {"data": [{"quantity": 3,
			"price": {"product": "prod_app", "metadata": {"plan": "lifetime", "quantity": "seats"}}}]}}`
)

// fakeStripe knows one paid subscription and one paid purchase of the product
// and a few odd ones. It returns how often Stripe was asked.
func fakeStripe(t *testing.T) *atomic.Int64 {
	t.Helper()
	var asked atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		switch r.URL.Path {
		case "/v1/checkout/sessions":
			_, _ = w.Write([]byte(`{"has_more": false, "data": [` + paidSession + `]}`))
		case "/v1/checkout/sessions/cs_1":
			_, _ = w.Write([]byte(paidSession))
		case "/v1/checkout/sessions/cs_pending":
			_, _ = w.Write([]byte(`{"id": "cs_pending", "mode": "payment", "payment_status": "unpaid",
				"line_items": {"data": [{"price": {"product": "prod_app"}}]}}`))
		case "/v1/subscriptions":
			_, _ = w.Write([]byte(`{"has_more": false, "data": [` + paidSub + `]}`))
		case "/v1/subscriptions/sub_1":
			_, _ = w.Write([]byte(paidSub))
		case "/v1/subscriptions/sub_monthly":
			_, _ = w.Write([]byte(`{"id": "sub_monthly", "status": "active", "customer": {"email": "m@acme.io"},
				"items": {"data": [{"price": {"product": "prod_app"},
					"current_period_start": 1797408000, "current_period_end": 1800000000}]}}`))
		case "/v1/subscriptions/sub_deleted":
			_, _ = w.Write([]byte(`{"id": "sub_deleted", "status": "canceled", "ended_at": 1799000000,
				"customer": {"id": "cus_1", "deleted": true},
				"items": {"data": [{"price": {"product": "prod_app"},
					"current_period_start": 1797000000, "current_period_end": 1800000000}]}}`))
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
	return &asked
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
	require.ErrorContains(t, err, "Stripe product", "the error names what is missing")

	broken = cfg
	broken.WebhookSecret = secret
	_, err = New(broken)
	require.Error(t, err, "a webhook that cannot send keys would swallow paid invoices")
}

func TestRefresh(t *testing.T) {
	asked := fakeStripe(t)
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
	assert.Equal(t, int64(1), asked.Load(), "a key that keeps asking does not keep asking Stripe")

	bought := issue(license.Claims{Plan: "pro", ExpiresAt: 1_700_000_000, ID: "cs_1"}, priv)
	status, key = post(t, h, "/refresh", bought, nil)
	require.Equal(t, http.StatusOK, status)
	claims, err = license.VerifyWith(key, "APP", pub)
	require.NoError(t, err)
	assert.Zero(t, claims.ExpiresAt, "a one time purchase never expires")
	assert.Equal(t, "lifetime", claims.Plan)
	assert.Equal(t, map[string]string{"seats": "3"}, claims.Meta, "the quantity becomes the seats")

	orphan := issue(license.Claims{Plan: "pro", Email: "left@acme.io", ExpiresAt: periodEnd, ID: "sub_deleted"}, priv)
	status, key = post(t, h, "/refresh", orphan, nil)
	require.Equal(t, http.StatusOK, status)
	claims, err = license.VerifyWith(key, "APP", pub)
	require.NoError(t, err)
	assert.Equal(t, "left@acme.io", claims.Email, "Stripe forgot the deleted customer, the key did not")
	assert.Equal(t, int64(1_799_000_000+grace), claims.ExpiresAt)

	manual := issue(license.Claims{Plan: "pro", Email: "a@x.io", ID: "lic_abc"}, priv)
	status, key = post(t, h, "/refresh", manual, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, manual, key, "a hand issued key comes back as it is")
	status, key = post(t, h, "/refresh", `"`+manual[:40]+"\r\n"+manual[40:]+`"`, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, manual, key, "a key mangled on its way comes back clean")

	_, otherPriv := testConfig(t)
	forged := issue(license.Claims{Plan: "pro", ID: "sub_1"}, otherPriv)
	status, _ = post(t, h, "/refresh", forged, nil)
	assert.Equal(t, http.StatusBadRequest, status, "only keys signed by the product reach Stripe")
	status, _ = post(t, h, "/refresh", "APP-AAAAA", nil)
	assert.Equal(t, http.StatusBadRequest, status)

	noID := issue(license.Claims{Plan: "pro", Email: "a@x.io"}, priv)
	status, key = post(t, h, "/refresh", noID, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, noID, key, "so does a key from before keys carried an id")

	for id, want := range map[string]int{
		"I-FOREIGN":  http.StatusNotFound, // only lic_ ids are hand issued, any other is the provider's
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
	paid := func(sub, reason string) string {
		return `{"type": "invoice.paid", "data": {"object": {"billing_reason": "` + reason + `",
			"parent": {"subscription_details": {"subscription": "` + sub + `"}}}}}`
	}
	now := time.Now()

	body := paid("sub_1", "subscription_create")
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
		"another product's sub.": paid("sub_other", "subscription_create"),
		"a monthly renewal":      paid("sub_monthly", "subscription_cycle"),
	} {
		status, _ = post(t, h, "/webhook", body, signature(body, now))
		assert.Equal(t, http.StatusOK, status, name)
	}
	assert.Len(t, sent, 2)

	// A monthly subscription still gets its first key, a longer period every key.
	for _, body := range []string{
		paid("sub_monthly", "subscription_create"),
		paid("sub_1", "subscription_cycle"),
	} {
		status, _ = post(t, h, "/webhook", body, signature(body, now))
		assert.Equal(t, http.StatusOK, status)
	}
	require.Len(t, sent, 4)
	assert.Contains(t, sent[2], "m@acme.io")
	assert.Contains(t, sent[3], "ops@acme.io")

	body = paid("sub_1", "subscription_cycle")
	status, _ = post(t, h, "/webhook", body, signature(paid("sub_other", ""), now))
	assert.Equal(t, http.StatusBadRequest, status, "the signature covers the body")
	status, _ = post(t, h, "/webhook", body, signature(body, now.Add(-time.Hour)))
	assert.Equal(t, http.StatusBadRequest, status, "an old event cannot be replayed")
	status, _ = post(t, h, "/webhook", body, nil)
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Len(t, sent, 4)

	// A one time purchase sends its key once, however often Stripe repeats the event.
	bought := func(event, session string) string {
		return `{"id": "` + event + `", "type": "checkout.session.completed",
			"data": {"object": {"id": "` + session + `", "mode": "payment"}}}`
	}
	for range 2 {
		body = bought("evt_1", "cs_1")
		status, _ = post(t, h, "/webhook", body, signature(body, now))
		assert.Equal(t, http.StatusOK, status)
	}
	require.Len(t, sent, 5)
	assert.Contains(t, sent[4], "buyer@acme.io")
	body = bought("evt_2", "cs_pending")
	status, _ = post(t, h, "/webhook", body, signature(body, now))
	assert.Equal(t, http.StatusOK, status, "the payment is still open, a later event brings the key")
	body = `{"id": "evt_3", "type": "checkout.session.completed",
		"data": {"object": {"id": "cs_1", "mode": "subscription"}}}`
	status, _ = post(t, h, "/webhook", body, signature(body, now))
	assert.Equal(t, http.StatusOK, status)
	assert.Len(t, sent, 5, "the invoice of a subscription brings its key, not the session")

	// Stripe retries on an error, so these keys are not lost.
	body = paid("sub_unpaid", "subscription_create")
	status, _ = post(t, h, "/webhook", body, signature(body, now))
	assert.Equal(t, http.StatusInternalServerError, status, "paid invoice, subscription not active yet")
	sendErr = errors.New("mail server is down")
	body = paid("sub_1", "subscription_create")
	status, _ = post(t, h, "/webhook", body, signature(body, now))
	assert.Equal(t, http.StatusInternalServerError, status)
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

// read takes a sent mail apart into its headers and its decoded bodies by type.
func read(t *testing.T, lines []string) (mail.Header, map[string]string) {
	t.Helper()
	start := slices.Index(lines, "DATA") + 1
	require.Positive(t, start)
	msg, err := mail.ReadMessage(strings.NewReader(strings.Join(lines[start:len(lines)-1], "\r\n")))
	require.NoError(t, err)
	kind, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	require.NoError(t, err)

	decode := func(r io.Reader) string {
		b, err := io.ReadAll(quotedprintable.NewReader(r))
		require.NoError(t, err)
		return string(b)
	}
	if kind == "text/plain" {
		return msg.Header, map[string]string{kind: decode(msg.Body)}
	}
	bodies := map[string]string{}
	parts := multipart.NewReader(msg.Body, params["boundary"])
	for {
		// NextRawPart leaves the quoted-printable decoding to decode.
		part, err := parts.NextRawPart()
		if errors.Is(err, io.EOF) {
			return msg.Header, bodies
		}
		require.NoError(t, err)
		partKind, _, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
		require.NoError(t, err)
		bodies[partKind] = decode(part)
	}
}

func TestSMTPSend(t *testing.T) {
	t.Parallel()
	srv := fakeSMTP(t)
	sender := SMTP{Addr: srv.addr, From: "Acme Keys <keys@acme.io>"}
	claims := license.Claims{App: "APP", Plan: "team", Email: "ops@acme.io", ExpiresAt: periodEnd}
	// Longer than a line of mail may be, 998 bytes.
	key := "APP-" + strings.Repeat("AAAAA-", 200) + "ZZZZZ"

	require.NoError(t, sender.Send(t.Context(), key, claims))
	lines := <-srv.lines
	assert.Contains(t, lines, "MAIL FROM:<keys@acme.io>")
	assert.Contains(t, lines, "RCPT TO:<ops@acme.io>")
	for _, line := range lines {
		assert.LessOrEqual(t, len(line), 998)
	}
	header, bodies := read(t, lines)
	assert.Equal(t, "Your APP license key", header.Get("Subject"))
	assert.Contains(t, bodies["text/plain"], key)
	assert.Contains(t, bodies["text/plain"], "Valid until: 15 January 2027")
	assert.Contains(t, bodies["text/html"], key)
	assert.Contains(t, bodies["text/html"], defaultAccent)

	claims.Email = "ops@acme.io\r\nBcc: thief@evil.io"
	err := sender.Send(t.Context(), key, claims)
	require.Error(t, err, "an address from Stripe must not be able to add headers")
}

// What the brand does to the markup is in TestMailCases.
func TestSMTPBrand(t *testing.T) {
	t.Parallel()
	srv := fakeSMTP(t)
	sender := SMTP{Addr: srv.addr, From: "keys@acme.io", Brand: Brand{
		Name:    "Acme Pro\r\nBcc: thief@evil.io",
		Company: "Acme GmbH, Street 1, 12345 Berlin",
	}}
	claims := license.Claims{App: "APP", Plan: "team", Email: "ops@acme.io"}

	require.NoError(t, sender.Send(t.Context(), "APP-AAAAA", claims))
	header, bodies := read(t, <-srv.lines)
	assert.Equal(t, "Your Acme Pro Bcc: thief@evil.io license key", header.Get("Subject"))
	assert.Empty(t, header.Get("Bcc"), "a product name must not be able to add headers")
	assert.Contains(t, bodies["text/html"], "Acme GmbH, Street 1, 12345 Berlin")
	assert.Contains(t, bodies["text/plain"], "Acme GmbH, Street 1, 12345 Berlin")
}

func TestLoadTemplates(t *testing.T) {
	t.Parallel()
	claims := license.Claims{App: "APP", Plan: "team", Email: "ops@acme.io", Meta: map[string]string{"seats": "5"}}
	send := func(dir string) (mail.Header, map[string]string) {
		templates, err := LoadTemplates(dir)
		require.NoError(t, err)
		srv := fakeSMTP(t)
		sender := SMTP{Addr: srv.addr, From: "keys@acme.io", Templates: templates}
		require.NoError(t, sender.Send(t.Context(), "APP-AAAAA", claims))
		return read(t, <-srv.lines)
	}
	write := func(dir, name, body string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600))
	}

	own := t.TempDir()
	write(own, "subject.txt", "{{.Name}}: {{.Meta.seats}} seats\n")
	write(own, "body.html", `<p>{{.Key}} for {{.Email}}, {{linked "ask help@acme.io" .Shades.Text}}</p>`)
	header, bodies := send(own)
	assert.Equal(t, "APP: 5 seats", header.Get("Subject"))
	assert.Equal(t, `<p>APP-AAAAA for ops@acme.io, ask <a href="mailto:help@acme.io" class="accent" `+
		`style="color:#2f3e8f;">help@acme.io</a></p>`, bodies["text/html"])
	assert.Contains(t, bodies["text/plain"], "Here is the APP license", "the missing file is the built in one")

	plain := t.TempDir()
	write(plain, "body.txt", "Key: {{.Key}}")
	_, bodies = send(plain)
	assert.Equal(t, map[string]string{"text/plain": "Key: APP-AAAAA"}, bodies, "a text of your own goes out alone")

	_, err := LoadTemplates(t.TempDir())
	require.Error(t, err, "an empty directory is a wrong path")
	broken := t.TempDir()
	write(broken, "body.txt", "{{.Key")
	_, err = LoadTemplates(broken)
	require.Error(t, err)
}
