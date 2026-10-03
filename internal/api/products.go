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
	"github.com/mizuchilabs/inkan/internal/mirror"
)

// Product is one app that keys are issued for. Secrets never leave the server.
type Product struct {
	ID                int64      `json:"id"`
	Name              string     `json:"name"`
	Prefix            string     `json:"prefix"             doc:"Visible key prefix and the app name the product verifies against"`
	Plan              string     `json:"plan"               doc:"Plan claim for provider prices without plan metadata and the default for manual licenses"`
	GraceDays         int64      `json:"grace_days"         doc:"Days a provider license stays valid past its paid period"`
	PublicKey         string     `json:"public_key"         doc:"Hex Ed25519 public key to build into the product"`
	ProviderProduct   string     `json:"provider_product"   doc:"Payment provider product id. Its subscriptions are licenses"`
	ProviderConnected bool       `json:"provider_connected" doc:"Whether an API key and a provider product are stored"`
	SyncedAt          *time.Time `json:"synced_at"          doc:"Last successful provider sync"`
	SyncError         string     `json:"sync_error"         doc:"Why the last provider sync failed, empty when it worked"`
	CreatedAt         time.Time  `json:"created_at"`
}

type ProductsOutput struct {
	Body []Product
}

type ProductOutput struct {
	Body Product
}

type ProductPath struct {
	ID int64 `path:"id"`
}

type CreateProductInput struct {
	Body struct {
		Name       string `json:"name"                  minLength:"1"         maxLength:"64"`
		Prefix     string `json:"prefix"                pattern:"^[A-Z0-9]+$" maxLength:"16"  doc:"Uppercase letters and digits. It cannot change later, issued keys are bound to it"`
		Plan       string `json:"plan"                  minLength:"1"         maxLength:"32"`
		GraceDays  int64  `json:"grace_days,omitempty"  minimum:"0"           maximum:"365"   default:"14"`
		PrivateKey string `json:"private_key,omitempty"                                       doc:"Base64 Ed25519 private key of an existing product. A fresh keypair is made when empty"`
	}
}

type UpdateProductInput struct {
	ProductPath

	Body struct {
		Name            string `json:"name"                   minLength:"1" maxLength:"64"`
		Plan            string `json:"plan"                   minLength:"1" maxLength:"32"`
		GraceDays       int64  `json:"grace_days"             minimum:"0"   maximum:"365"`
		ProviderProduct string `json:"provider_product,omitempty"                            doc:"Empty disconnects the provider"`
		ProviderKey     string `json:"provider_key,omitempty"                                doc:"Empty keeps the stored key"`
	}
}

type SyncOutput struct {
	Body struct {
		Licenses int `json:"licenses" doc:"License subscriptions found at the provider"`
	}
}

type productHandlers struct {
	q *db.Queries
}

func registerProducts(api huma.API, q *db.Queries) {
	h := &productHandlers{q: q}
	huma.Register(api, huma.Operation{
		OperationID: "list-products",
		Method:      http.MethodGet,
		Path:        "/api/products",
		Summary:     "List products",
		Tags:        []string{"Products"},
	}, h.list)
	huma.Register(api, huma.Operation{
		OperationID:   "create-product",
		Method:        http.MethodPost,
		Path:          "/api/products",
		Summary:       "Create a product",
		Description:   "Makes a fresh signing keypair, or takes over the private key of a product that already ships.",
		Tags:          []string{"Products"},
		DefaultStatus: http.StatusCreated,
	}, h.create)
	huma.Register(api, huma.Operation{
		OperationID: "update-product",
		Method:      http.MethodPut,
		Path:        "/api/products/{id}",
		Summary:     "Update a product",
		Description: "A connected product is synced with its provider right away, the outcome is on the returned product.",
		Tags:        []string{"Products"},
	}, h.update)
	huma.Register(api, huma.Operation{
		OperationID:   "delete-product",
		Method:        http.MethodDelete,
		Path:          "/api/products/{id}",
		Summary:       "Delete a product",
		Description:   "Deletes the signing key and every license of the product.",
		Tags:          []string{"Products"},
		DefaultStatus: http.StatusNoContent,
	}, h.delete)
	huma.Register(api, huma.Operation{
		OperationID: "sync-product",
		Method:      http.MethodPost,
		Path:        "/api/products/{id}/sync",
		Summary:     "Sync a product with its provider",
		Tags:        []string{"Products"},
	}, h.sync)
}

func (h *productHandlers) list(ctx context.Context, _ *struct{}) (*ProductsOutput, error) {
	products, err := h.q.ListProducts(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list products")
	}
	out := make([]Product, len(products))
	for i, p := range products {
		out[i] = toProduct(p)
	}
	return &ProductsOutput{Body: out}, nil
}

func (h *productHandlers) create(ctx context.Context, in *CreateProductInput) (*ProductOutput, error) {
	privateKey := strings.TrimSpace(in.Body.PrivateKey)
	if privateKey == "" {
		var err error
		if privateKey, _, err = license.GenerateKey(); err != nil {
			return nil, huma.Error500InternalServerError("failed to generate a keypair")
		}
	} else if _, err := license.ParsePrivateKey(privateKey); err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}

	p, err := h.q.CreateProduct(ctx, &db.CreateProductParams{
		Name:       in.Body.Name,
		Prefix:     in.Body.Prefix,
		Plan:       in.Body.Plan,
		GraceDays:  in.Body.GraceDays,
		PrivateKey: privateKey,
	})
	if isUnique(err) {
		return nil, huma.Error409Conflict("a product with this name or prefix already exists")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to create product")
	}
	return &ProductOutput{Body: toProduct(p)}, nil
}

func (h *productHandlers) update(ctx context.Context, in *UpdateProductInput) (*ProductOutput, error) {
	current, err := h.get(ctx, in.ID)
	if err != nil {
		return nil, err
	}

	providerKey := strings.TrimSpace(in.Body.ProviderKey)
	if providerKey == "" {
		providerKey = current.ProviderKey
	}
	p, err := h.q.UpdateProduct(ctx, &db.UpdateProductParams{
		Name:            in.Body.Name,
		Plan:            in.Body.Plan,
		GraceDays:       in.Body.GraceDays,
		ProviderKey:     providerKey,
		ProviderProduct: strings.TrimSpace(in.Body.ProviderProduct),
		ID:              in.ID,
	})
	if isUnique(err) {
		return nil, huma.Error409Conflict("a product with this name already exists")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to update product")
	}

	// Plan and grace shape the provider licenses, so they are rebuilt at once.
	// A failed sync is not a failed update, it shows up as sync_error.
	if mirror.Connected(p) {
		_, _ = mirror.Sync(ctx, h.q, p)
		if p, err = h.get(ctx, in.ID); err != nil {
			return nil, err
		}
	}
	return &ProductOutput{Body: toProduct(p)}, nil
}

func (h *productHandlers) delete(ctx context.Context, in *ProductPath) (*struct{}, error) {
	if err := h.q.DeleteProduct(ctx, in.ID); err != nil {
		return nil, huma.Error500InternalServerError("failed to delete product")
	}
	return nil, nil
}

func (h *productHandlers) sync(ctx context.Context, in *ProductPath) (*SyncOutput, error) {
	p, err := h.get(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if !mirror.Connected(p) {
		return nil, huma.Error409Conflict("the product is not connected to a payment provider")
	}
	n, err := mirror.Sync(ctx, h.q, p)
	if err != nil {
		return nil, huma.Error502BadGateway(err.Error())
	}
	out := &SyncOutput{}
	out.Body.Licenses = n
	return out, nil
}

func (h *productHandlers) get(ctx context.Context, id int64) (*db.Product, error) {
	p, err := h.q.GetProduct(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.Error404NotFound("product not found")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to load product")
	}
	return p, nil
}

func toProduct(p *db.Product) Product {
	out := Product{
		ID:                p.ID,
		Name:              p.Name,
		Prefix:            p.Prefix,
		Plan:              p.Plan,
		GraceDays:         p.GraceDays,
		ProviderProduct:   p.ProviderProduct,
		ProviderConnected: mirror.Connected(p),
		SyncError:         p.SyncError,
		CreatedAt:         time.Unix(p.CreatedAt, 0),
	}
	if priv, err := license.ParsePrivateKey(p.PrivateKey); err == nil {
		out.PublicKey = hex.EncodeToString(publicKey(priv))
	}
	if p.SyncedAt != nil {
		out.SyncedAt = new(time.Unix(*p.SyncedAt, 0))
	}
	return out
}

func publicKey(priv ed25519.PrivateKey) ed25519.PublicKey {
	pub, _ := priv.Public().(ed25519.PublicKey)
	return pub
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
