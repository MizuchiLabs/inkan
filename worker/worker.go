// Package worker is the public side of a product: a stateless HTTP handler
// that turns paid Stripe subscriptions into license keys.
//
//	POST /webhook  Stripe reports a paid invoice, the key goes to the customer
//	POST /refresh  an app sends its key and gets the current one back
//
// Nothing is stored. Stripe says what is paid and the private key signs it,
// so every answer is the key the inkan ledger shows for that subscription.
package worker

import (
	"cmp"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mizuchilabs/inkan/internal/stripe"
	"github.com/mizuchilabs/inkan/license"
)

// Stripe rejects events older than this in its own libraries too.
const webhookTolerance = 5 * time.Minute

// Config is everything a worker knows about its product. The values are the
// ones on the product in inkan, they have to match for the keys to match.
type Config struct {
	// Prefix is the key prefix of the product, the app name its keys are bound to.
	Prefix string
	// PrivateKey is the base64 Ed25519 key of the product, see [license.GenerateKey].
	PrivateKey string
	// StripeKey reads subscriptions and customers. A restricted key is enough.
	StripeKey string
	// StripeProduct is the Stripe product id. Its subscriptions are licenses.
	StripeProduct string
	// Plan is the plan claim for prices without plan metadata.
	Plan string
	// GraceDays is how long a key outlives its paid period.
	GraceDays int64

	// WebhookSecret is the signing secret of the Stripe webhook endpoint.
	// Leave it and Send empty to serve refresh only.
	WebhookSecret string
	// Send delivers a freshly paid key to its owner. [SMTP.Send] mails it.
	Send func(ctx context.Context, key string, claims license.Claims) error
}

type handler struct {
	cfg  Config
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

// New checks the config and returns the handler.
func New(cfg Config) (http.Handler, error) {
	if cfg.Prefix == "" || cfg.StripeKey == "" || cfg.StripeProduct == "" {
		return nil, errors.New("worker: prefix, Stripe key and Stripe product are required")
	}
	if (cfg.WebhookSecret == "") != (cfg.Send == nil) {
		return nil, errors.New("worker: the webhook needs both a secret and a way to send keys")
	}
	priv, err := license.ParsePrivateKey(cfg.PrivateKey)
	if err != nil {
		return nil, err
	}
	pub, _ := priv.Public().(ed25519.PublicKey)
	h := &handler{cfg: cfg, priv: priv, pub: pub}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /refresh", h.refresh)
	if cfg.Send != nil {
		mux.HandleFunc("POST /webhook", h.webhook)
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	return mux, nil
}

// refresh trades a key for the one its subscription is owed now. A canceled
// or unpaid subscription gets its last key back, so the expiry never moves
// further than what was paid.
func (h *handler) refresh(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<10))
	if err != nil {
		http.Error(w, "the key is too long", http.StatusRequestEntityTooLarge)
		return
	}
	claims, err := license.VerifyWith(string(body), h.cfg.Prefix, h.pub)
	if err != nil && !errors.Is(err, license.ErrExpired) {
		http.Error(w, "not a license key of this product", http.StatusBadRequest)
		return
	}

	// A hand issued license is not in Stripe, the key it has is all there is.
	key := strings.TrimSpace(string(body))
	if strings.HasPrefix(claims.ID, "sub_") {
		key, _, err = h.issue(r.Context(), claims.ID)
		if errors.Is(err, stripe.ErrNotFound) || errors.Is(err, stripe.ErrOtherProduct) ||
			errors.Is(err, stripe.ErrNotOwed) {
			http.Error(w, "no license behind this key", http.StatusNotFound)
			return
		}
		if err != nil {
			slog.Error("refresh failed", "license", claims.ID, "error", err)
			http.Error(w, "the license could not be looked up", http.StatusBadGateway)
			return
		}
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, key)
}

// webhook sends the key once an invoice of the product is paid. That covers
// the first payment, the start of a trial and every renewal. Anything it
// cannot finish is answered with an error, so Stripe tries again later.
func (h *handler) webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "the event is too large", http.StatusRequestEntityTooLarge)
		return
	}
	if !signed(body, r.Header.Get("Stripe-Signature"), h.cfg.WebhookSecret) {
		http.Error(w, "the signature does not match", http.StatusBadRequest)
		return
	}

	var event struct {
		Type string `json:"type"`
		Data struct {
			Object struct {
				Subscription string `json:"subscription"` // API versions before 2025-03-31
				Parent       struct {
					SubscriptionDetails struct {
						Subscription string `json:"subscription"`
					} `json:"subscription_details"`
				} `json:"parent"`
			} `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, "the event cannot be read", http.StatusBadRequest)
		return
	}
	invoice := event.Data.Object
	subID := cmp.Or(invoice.Parent.SubscriptionDetails.Subscription, invoice.Subscription)
	if event.Type != "invoice.paid" || subID == "" {
		return
	}

	key, claims, err := h.issue(r.Context(), subID)
	if errors.Is(err, stripe.ErrOtherProduct) {
		return
	}
	if err != nil {
		slog.Error("webhook: no key for a paid invoice", "license", subID, "error", err)
		http.Error(w, "the key could not be issued", http.StatusInternalServerError)
		return
	}
	if claims.Email == "" {
		slog.Warn("webhook: the customer has no email, the key was not sent", "license", subID)
		return
	}
	if err := h.cfg.Send(r.Context(), key, claims); err != nil {
		slog.Error("webhook: sending the key failed", "license", subID, "error", err)
		http.Error(w, "the key could not be sent", http.StatusInternalServerError)
		return
	}
	slog.Info("license key sent", "license", subID, "plan", claims.Plan)
}

// issue signs the key a subscription is owed right now.
func (h *handler) issue(ctx context.Context, subID string) (string, license.Claims, error) {
	sub, err := stripe.Get(ctx, h.cfg.StripeKey, subID)
	if err != nil {
		return "", license.Claims{}, err
	}
	l, err := sub.License(h.cfg.StripeProduct, h.cfg.Plan, h.cfg.GraceDays)
	if err != nil {
		return "", license.Claims{}, err
	}
	// The ledger signs the same four claims from its row, see toLicense in internal/api.
	claims := license.Claims{App: h.cfg.Prefix, Plan: l.Plan, Email: l.Email, ExpiresAt: l.ExpiresAt, ID: l.ID}
	key, err := license.Issue(h.cfg.Prefix, claims, h.priv)
	return key, claims, err
}

// signed checks a Stripe-Signature header, t=<unix>,v1=<hex>. The signature
// is an HMAC-SHA256 of "<t>.<body>" with the endpoint secret.
func signed(body []byte, header, secret string) bool {
	var stamp string
	var sigs [][]byte
	for part := range strings.SplitSeq(header, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch k {
		case "t":
			stamp = v
		case "v1":
			if sig, err := hex.DecodeString(v); err == nil {
				sigs = append(sigs, sig)
			}
		}
	}
	sent, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil || time.Since(time.Unix(sent, 0)).Abs() > webhookTolerance {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(stamp + "."))
	mac.Write(body)
	want := mac.Sum(nil)
	return slices.ContainsFunc(sigs, func(sig []byte) bool { return hmac.Equal(sig, want) })
}
