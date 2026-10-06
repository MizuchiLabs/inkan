package api

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mizuchilabs/inkan/license"

	"github.com/mizuchilabs/inkan/internal/db"
)

const (
	stateValid   = "valid"
	stateExpired = "expired"
	stateInvalid = "invalid"
)

type CheckInput struct {
	Body struct {
		Key string `json:"key" minLength:"1" maxLength:"4096"`
	}
}

// CheckResult is what a pasted key turns out to be.
type CheckResult struct {
	State     string            `json:"state"      enum:"valid,expired,invalid"`
	Reason    string            `json:"reason"                                  doc:"Why the key is invalid, empty otherwise"`
	ProductID *int64            `json:"product_id"                              doc:"Product the key prefix belongs to, null when unknown"`
	LicenseID string            `json:"license_id"                              doc:"Id signed into the key, empty for keys issued without one"`
	Known     bool              `json:"known"                                   doc:"Whether the license id is in the ledger"`
	Plan      string            `json:"plan"`
	Email     string            `json:"email"`
	ExpiresAt *time.Time        `json:"expires_at"                              doc:"Null for a perpetual key"`
	Meta      map[string]string `json:"meta"`
}

type CheckOutput struct {
	Body CheckResult
}

type checkHandlers struct {
	q *db.Queries
}

func registerCheck(api huma.API, q *db.Queries) {
	h := &checkHandlers{q: q}
	huma.Register(api, huma.Operation{
		OperationID: "check-key",
		Method:      http.MethodPost,
		Path:        "/api/check",
		Summary:     "Check a license key",
		Description: "Verifies a key the way the product would and shows the claims signed into it.",
		Tags:        []string{"Licenses"},
	}, h.check)
}

func (h *checkHandlers) check(ctx context.Context, in *CheckInput) (*CheckOutput, error) {
	key := license.Clean(in.Body.Key)
	out := &CheckOutput{Body: CheckResult{State: stateInvalid, Meta: map[string]string{}}}
	res := &out.Body

	prefix, _, _ := strings.Cut(key, "-")
	p, err := h.q.GetProductByPrefix(ctx, strings.ToUpper(prefix))
	if errors.Is(err, sql.ErrNoRows) {
		res.Reason = "No product uses this key prefix."
		return out, nil
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to load product")
	}
	res.ProductID = &p.ID

	priv, err := license.ParsePrivateKey(p.PrivateKey)
	if err != nil {
		return nil, huma.Error500InternalServerError("the product has a broken signing key")
	}
	// A key from before a rotation is still a key of the product.
	pubs := []ed25519.PublicKey{publicKey(priv)}
	for _, old := range p.OldKeys() {
		if pub, err := hex.DecodeString(old); err == nil {
			pubs = append(pubs, pub)
		}
	}
	claims, err := license.VerifyWith(key, p.Prefix, pubs...)
	switch {
	case err == nil:
		res.State = stateValid
	case errors.Is(err, license.ErrExpired):
		res.State = stateExpired
	case errors.Is(err, license.ErrInvalidSig):
		res.Reason = "The signature matches no signing key of the product."
		return out, nil
	default:
		res.Reason = "This is not a well formed license key."
		return out, nil
	}

	res.LicenseID = claims.ID
	res.Plan = claims.Plan
	res.Email = claims.Email
	if claims.Meta != nil {
		res.Meta = claims.Meta
	}
	if claims.ExpiresAt != 0 {
		res.ExpiresAt = new(time.Unix(claims.ExpiresAt, 0))
	}
	if claims.ID != "" {
		_, err := h.q.GetLicense(ctx, &db.GetLicenseParams{ProductID: p.ID, ID: claims.ID})
		res.Known = err == nil
	}
	return out, nil
}
