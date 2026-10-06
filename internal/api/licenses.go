package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mizuchilabs/inkan/license"

	"github.com/mizuchilabs/inkan/internal/db"
)

const (
	sourceManual = "manual"
	// Every byte of metadata makes the key longer, and keys get pasted by hand.
	maxMetaBytes = 512
)

// License is one issued license with its key.
type License struct {
	ID           string            `json:"id"            doc:"Subscription or checkout session id at the provider, or lic_... for a manual license"`
	ProductID    int64             `json:"product_id"`
	Source       string            `json:"source"        doc:"manual, or the payment provider the worker reads, like stripe"`
	Email        string            `json:"email"`
	Customer     string            `json:"customer"      doc:"Display name, not part of the key"`
	CustomerGone bool              `json:"customer_gone" doc:"The customer was deleted at the provider, email and customer are the last known ones"`
	Plan         string            `json:"plan"`
	ExpiresAt    *time.Time        `json:"expires_at"    doc:"Null for a perpetual license"`
	Meta         map[string]string `json:"meta"          doc:"Extra claims signed into the key"`
	Note         string            `json:"note"`
	Status       string            `json:"status"        doc:"Subscription status at the provider, paid or refunded for a one time purchase, empty for a manual license"`
	Renews       bool              `json:"renews"        doc:"Whether the provider will bill another period"`
	Key          string            `json:"key"           doc:"The signed license key. A paid license has the one its worker hands out"`
	CreatedAt    time.Time         `json:"created_at"`
}

type ListLicensesInput struct {
	Limit  int64 `query:"limit"  minimum:"0" maximum:"1000" default:"0" doc:"Most licenses to return, 0 returns all of them"`
	Offset int64 `query:"offset" minimum:"0"                default:"0" doc:"How many licenses to skip"`
}

type LicensesOutput struct {
	Body []License
}

type LicenseOutput struct {
	Body License
}

// LicensePath names one license. A subscription can hold several products,
// so the id alone is not enough.
type LicensePath struct {
	ProductID int64  `path:"product_id"`
	ID        string `path:"id"`
}

// LicenseFields are the parts of a manual license a user sets.
type LicenseFields struct {
	Email     string            `json:"email"                maxLength:"254"`
	Customer  string            `json:"customer,omitempty"   maxLength:"128"`
	Plan      string            `json:"plan,omitempty"       maxLength:"32"   doc:"Defaults to the product plan"`
	ExpiresAt *time.Time        `json:"expires_at,omitempty"                  doc:"Leave out for a perpetual license"`
	Meta      map[string]string `json:"meta,omitempty"                        doc:"Extra claims signed into the key, keep them short"`
	Note      string            `json:"note,omitempty"       maxLength:"2000"`
}

type CreateLicenseInput struct {
	Body struct {
		LicenseFields

		ProductID int64 `json:"product_id"`
	}
}

type UpdateLicenseInput struct {
	LicensePath

	Body LicenseFields
}

type licenseHandlers struct {
	q *db.Queries
}

func registerLicenses(api huma.API, q *db.Queries) {
	h := &licenseHandlers{q: q}
	huma.Register(api, huma.Operation{
		OperationID: "list-licenses",
		Method:      http.MethodGet,
		Path:        "/api/licenses",
		Summary:     "List licenses",
		Description: "Every license with its key, newest first. Page through a long ledger with limit and offset, a page shorter than the limit is the last one.",
		Tags:        []string{"Licenses"},
	}, h.list)
	huma.Register(api, huma.Operation{
		OperationID:   "create-license",
		Method:        http.MethodPost,
		Path:          "/api/licenses",
		Summary:       "Issue a manual license",
		Tags:          []string{"Licenses"},
		DefaultStatus: http.StatusCreated,
	}, h.create)
	huma.Register(api, huma.Operation{
		OperationID: "update-license",
		Method:      http.MethodPut,
		Path:        "/api/products/{product_id}/licenses/{id}",
		Summary:     "Update a license",
		Description: "Changing a signed field of a manual license changes its key. The worker owns the paid licenses, only their note is taken.",
		Tags:        []string{"Licenses"},
	}, h.update)
	huma.Register(api, huma.Operation{
		OperationID:   "delete-license",
		Method:        http.MethodDelete,
		Path:          "/api/products/{product_id}/licenses/{id}",
		Summary:       "Delete a manual license",
		Description:   "Only forgets the license here. A key that is already out keeps working until it expires.",
		Tags:          []string{"Licenses"},
		DefaultStatus: http.StatusNoContent,
	}, h.delete)
}

func (h *licenseHandlers) list(ctx context.Context, in *ListLicensesInput) (*LicensesOutput, error) {
	products, err := h.q.ListProducts(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list products")
	}
	byID := make(map[int64]*db.Product, len(products))
	for _, p := range products {
		byID[p.ID] = p
	}

	limit := in.Limit
	if limit == 0 {
		limit = -1
	}
	rows, err := h.q.ListLicenses(ctx, &db.ListLicensesParams{Limit: limit, Offset: in.Offset})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list licenses")
	}
	out := make([]License, len(rows))
	for i, l := range rows {
		if out[i], err = toLicense(byID[l.ProductID], l); err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}
	}
	return &LicensesOutput{Body: out}, nil
}

func (h *licenseHandlers) create(ctx context.Context, in *CreateLicenseInput) (*LicenseOutput, error) {
	p, err := h.q.GetProduct(ctx, in.Body.ProductID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.Error422UnprocessableEntity("product not found")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to load product")
	}
	meta, err := encodeMeta(in.Body.Meta)
	if err != nil {
		return nil, err
	}

	l, err := h.q.CreateLicense(ctx, &db.CreateLicenseParams{
		ID:        "lic_" + strings.ToLower(rand.Text()[:12]),
		ProductID: p.ID,
		Email:     strings.TrimSpace(in.Body.Email),
		Customer:  strings.TrimSpace(in.Body.Customer),
		Plan:      planOr(in.Body.Plan, p),
		ExpiresAt: unixOrZero(in.Body.ExpiresAt),
		Meta:      meta,
		Note:      in.Body.Note,
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to store license")
	}
	return h.output(p, l)
}

func (h *licenseHandlers) update(ctx context.Context, in *UpdateLicenseInput) (*LicenseOutput, error) {
	l, err := h.q.GetLicense(ctx, &db.GetLicenseParams{ProductID: in.ProductID, ID: in.ID})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.Error404NotFound("license not found")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to load license")
	}
	p, err := h.q.GetProduct(ctx, l.ProductID)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to load product")
	}

	if l.Source != sourceManual {
		l, err = h.q.SetLicenseNote(ctx, &db.SetLicenseNoteParams{
			Note:      in.Body.Note,
			ProductID: in.ProductID,
			ID:        in.ID,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to store note")
		}
		return h.output(p, l)
	}

	meta, err := encodeMeta(in.Body.Meta)
	if err != nil {
		return nil, err
	}
	l, err = h.q.UpdateLicense(ctx, &db.UpdateLicenseParams{
		Email:     strings.TrimSpace(in.Body.Email),
		Customer:  strings.TrimSpace(in.Body.Customer),
		Plan:      planOr(in.Body.Plan, p),
		ExpiresAt: unixOrZero(in.Body.ExpiresAt),
		Meta:      meta,
		Note:      in.Body.Note,
		ProductID: in.ProductID,
		ID:        in.ID,
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to store license")
	}
	return h.output(p, l)
}

func (h *licenseHandlers) delete(ctx context.Context, in *LicensePath) (*struct{}, error) {
	n, err := h.q.DeleteLicense(ctx, &db.DeleteLicenseParams{ProductID: in.ProductID, ID: in.ID})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to delete license")
	}
	if n == 1 {
		return nil, nil
	}
	_, err = h.q.GetLicense(ctx, &db.GetLicenseParams{ProductID: in.ProductID, ID: in.ID})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.Error404NotFound("license not found")
	}
	return nil, huma.Error409Conflict(
		"only manual licenses can be deleted, paid ones come back with the next sync",
	)
}

func (h *licenseHandlers) output(p *db.Product, l *db.License) (*LicenseOutput, error) {
	out, err := toLicense(p, l)
	if err != nil {
		return nil, huma.Error500InternalServerError(err.Error())
	}
	return &LicenseOutput{Body: out}, nil
}

// toLicense adds the key to the row. A manual one is signed here. Signing is
// deterministic, so it is the same key every time until one of its claims
// changes. A paid license has the key its worker hands out.
func toLicense(p *db.Product, l *db.License) (License, error) {
	if p == nil {
		return License{}, fmt.Errorf("license %s has no product", l.ID)
	}
	meta := map[string]string{}
	if err := json.Unmarshal([]byte(l.Meta), &meta); err != nil {
		return License{}, fmt.Errorf("license %s has broken metadata: %w", l.ID, err)
	}
	key := l.Key
	if l.Source == sourceManual {
		priv, err := license.ParsePrivateKey(p.PrivateKey)
		if err != nil {
			return License{}, fmt.Errorf("product %s: %w", p.Name, err)
		}
		key, err = license.Issue(p.Prefix, license.Claims{
			Plan:      l.Plan,
			Email:     l.Email,
			ExpiresAt: l.ExpiresAt,
			ID:        l.ID,
			Meta:      meta,
		}, priv)
		if err != nil {
			return License{}, fmt.Errorf("signing license %s: %w", l.ID, err)
		}
	}

	out := License{
		ID:           l.ID,
		ProductID:    l.ProductID,
		Source:       l.Source,
		Email:        l.Email,
		Customer:     l.Customer,
		CustomerGone: l.CustomerGone,
		Plan:         l.Plan,
		Meta:         meta,
		Note:         l.Note,
		Status:       l.Status,
		Renews:       l.Renews,
		Key:          key,
		CreatedAt:    time.Unix(l.CreatedAt, 0),
	}
	if l.ExpiresAt != 0 {
		out.ExpiresAt = new(time.Unix(l.ExpiresAt, 0))
	}
	return out, nil
}

func encodeMeta(meta map[string]string) (string, error) {
	if len(meta) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(meta)
	if err != nil {
		return "", huma.Error422UnprocessableEntity("metadata cannot be encoded")
	}
	if len(b) > maxMetaBytes {
		return "", huma.Error422UnprocessableEntity(
			fmt.Sprintf("metadata is %d bytes, the limit is %d", len(b), maxMetaBytes),
		)
	}
	return string(b), nil
}

func planOr(plan string, p *db.Product) string {
	if plan = strings.TrimSpace(plan); plan != "" {
		return plan
	}
	return p.Plan
}

func unixOrZero(t *time.Time) int64 {
	if t == nil {
		return 0
	}
	return t.Unix()
}
