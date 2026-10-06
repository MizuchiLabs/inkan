package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
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
	ID              int64      `json:"id"`
	Name            string     `json:"name"`
	Prefix          string     `json:"prefix"           doc:"Visible key prefix and the app name the product verifies against"`
	Plan            string     `json:"plan"             doc:"Default for manual licenses. The worker gives it to prices without plan metadata"`
	PublicKey       string     `json:"public_key"       doc:"Hex Ed25519 public key to build into the product"`
	OldPublicKeys   []string   `json:"old_public_keys"  doc:"Public keys from before a rotation, newest first. The product carries them next to public_key until their keys have run out" nullable:"false"`
	WorkerURL       string     `json:"worker_url"       doc:"Where the worker of the product runs. Its paid licenses are mirrored from there"`
	WorkerConnected bool       `json:"worker_connected" doc:"Whether a worker url is stored"`
	GraceDays       int64      `json:"grace_days"       doc:"Worker setting. Days a paid license stays valid past its paid period"`
	MaxKeyDays      int64      `json:"max_key_days"     doc:"Worker setting. Longest life of a paid key in days, 0 has no limit. Apps refresh for the next one"`
	ProviderProduct string     `json:"provider_product" doc:"Worker setting. Stripe product id, its subscriptions and purchases are licenses"`
	MailFrom        string     `json:"mail_from"        doc:"Worker setting. Sender of the key mails"`
	MailLogo        string     `json:"mail_logo"        doc:"Worker setting. https url of a logo for the dark header of the key mail"`
	MailAccent      string     `json:"mail_accent"      doc:"Worker setting. Color of the key mail, #rrggbb"`
	MailFooter      string     `json:"mail_footer"      doc:"Worker setting. One line under the key in the mail"`
	MailCompany     string     `json:"mail_company"     doc:"Worker setting. Last line of the key mail: legal name, address, register number"`
	SyncedAt        *time.Time `json:"synced_at"        doc:"Last time the worker was read"`
	SyncError       string     `json:"sync_error"       doc:"Why the worker could not be read, or what is off with it. Empty when all is well"`
	CreatedAt       time.Time  `json:"created_at"`
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
		MaxKeyDays int64  `json:"max_key_days,omitempty"    minimum:"0"           maximum:"3650"`
		PrivateKey string `json:"private_key,omitempty"                                       doc:"Base64 Ed25519 private key of an existing product. A fresh keypair is made when empty"`
	}
}

type UpdateProductInput struct {
	ProductPath

	Body struct {
		Name            string `json:"name"                       minLength:"1"                    maxLength:"64"`
		Plan            string `json:"plan"                       minLength:"1"                    maxLength:"32"`
		WorkerURL       string `json:"worker_url,omitempty"       pattern:"^(https?://.+)?$"       maxLength:"512" doc:"Where the worker runs. Empty disconnects it"`
		GraceDays       int64  `json:"grace_days"                 minimum:"0"                      maximum:"365"`
		MaxKeyDays      int64  `json:"max_key_days,omitempty"         minimum:"0"                      maximum:"3650"`
		ProviderProduct string `json:"provider_product,omitempty"                                  maxLength:"128" doc:"Stripe product id for the worker"`
		MailFrom        string `json:"mail_from,omitempty"                                         maxLength:"256" doc:"An address or \"Name <address>\""`
		MailLogo        string `json:"mail_logo,omitempty"        pattern:"^(https://.+)?$"        maxLength:"512" doc:"https url of a logo for the dark header of the key mail"`
		MailAccent      string `json:"mail_accent,omitempty"      pattern:"^(#[0-9a-fA-F]{6})?$"                   doc:"Color of the key mail, like #2f3e8f"`
		MailFooter      string `json:"mail_footer,omitempty"                                       maxLength:"256" doc:"One line under the key in the mail, like where to get help"`
		MailCompany     string `json:"mail_company,omitempty"                                      maxLength:"512" doc:"Last line of the key mail, for what a business has to state: legal name, address, register number"`
	}
}

type SyncOutput struct {
	Body struct {
		Licenses int `json:"licenses" doc:"Paid licenses the worker hands out"`
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
		Description: "A product with a worker is synced with it right away, the outcome is on the returned product. The worker settings only reach the worker once its env is exported and deployed.",
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
		OperationID: "rotate-product-key",
		Method:      http.MethodPost,
		Path:        "/api/products/{id}/rotate-key",
		Summary:     "Rotate the signing key of a product",
		Description: "Makes a fresh keypair and destroys the old private key. Its public half moves to old_public_keys, so keys that are out still verify in a product that carries both. Manual licenses get new keys, the worker signs with the new key once its env is exported and deployed.",
		Tags:        []string{"Products"},
	}, h.rotate)
	huma.Register(api, huma.Operation{
		OperationID: "drop-old-product-keys",
		Method:      http.MethodDelete,
		Path:        "/api/products/{id}/old-keys",
		Summary:     "Drop the old public keys of a product",
		Description: "For when no key signed before the rotation is still in use. The worker turns such keys down once its env is exported and deployed.",
		Tags:        []string{"Products"},
	}, h.dropOldKeys)
	huma.Register(api, huma.Operation{
		OperationID: "sync-product",
		Method:      http.MethodPost,
		Path:        "/api/products/{id}/sync",
		Summary:     "Sync a product with its worker",
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
		MaxKeyDays: in.Body.MaxKeyDays,
		PrivateKey: privateKey,
		// Two of them, a worker wants at least 32 characters.
		WorkerToken: rand.Text() + rand.Text(),
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
	p, err := h.q.UpdateProduct(ctx, &db.UpdateProductParams{
		Name:            in.Body.Name,
		Plan:            in.Body.Plan,
		GraceDays:       in.Body.GraceDays,
		MaxKeyDays:      in.Body.MaxKeyDays,
		ProviderProduct: strings.TrimSpace(in.Body.ProviderProduct),
		WorkerUrl:       strings.TrimSpace(in.Body.WorkerURL),
		MailFrom:        strings.TrimSpace(in.Body.MailFrom),
		MailLogo:        strings.TrimSpace(in.Body.MailLogo),
		MailAccent:      in.Body.MailAccent,
		MailFooter:      strings.TrimSpace(in.Body.MailFooter),
		MailCompany:     strings.TrimSpace(in.Body.MailCompany),
		ID:              in.ID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.Error404NotFound("product not found")
	}
	if isUnique(err) {
		return nil, huma.Error409Conflict("a product with this name already exists")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to update product")
	}

	return h.synced(ctx, p)
}

func (h *productHandlers) rotate(ctx context.Context, in *ProductPath) (*ProductOutput, error) {
	p, err := h.get(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	priv, err := license.ParsePrivateKey(p.PrivateKey)
	if err != nil {
		return nil, huma.Error500InternalServerError("the product has a broken signing key")
	}
	fresh, _, err := license.GenerateKey()
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to generate a keypair")
	}
	old := append([]string{hex.EncodeToString(publicKey(priv))}, p.OldKeys()...)
	return h.setKeys(ctx, p.ID, fresh, old)
}

func (h *productHandlers) dropOldKeys(ctx context.Context, in *ProductPath) (*ProductOutput, error) {
	p, err := h.get(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	return h.setKeys(ctx, p.ID, p.PrivateKey, nil)
}

func (h *productHandlers) setKeys(
	ctx context.Context,
	id int64,
	privateKey string,
	old []string,
) (*ProductOutput, error) {
	p, err := h.q.SetProductKeys(ctx, &db.SetProductKeysParams{
		PrivateKey:    privateKey,
		OldPublicKeys: strings.Join(old, ","),
		ID:            id,
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to store the keys")
	}
	return h.synced(ctx, p)
}

// synced reads the worker of a product that was just changed, so the answer
// says what the worker makes of it. A failed sync is not a failed change, it
// shows up as sync_error.
func (h *productHandlers) synced(ctx context.Context, p *db.Product) (*ProductOutput, error) {
	if mirror.Connected(p) {
		_, _ = mirror.Sync(ctx, h.q, p)
		var err error
		if p, err = h.get(ctx, p.ID); err != nil {
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
		return nil, huma.Error409Conflict("the product has no worker")
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
		ID:              p.ID,
		Name:            p.Name,
		Prefix:          p.Prefix,
		Plan:            p.Plan,
		WorkerURL:       p.WorkerUrl,
		WorkerConnected: mirror.Connected(p),
		GraceDays:       p.GraceDays,
		MaxKeyDays:      p.MaxKeyDays,
		ProviderProduct: p.ProviderProduct,
		MailFrom:        p.MailFrom,
		MailLogo:        p.MailLogo,
		MailAccent:      p.MailAccent,
		MailFooter:      p.MailFooter,
		MailCompany:     p.MailCompany,
		SyncError:       p.SyncError,
		CreatedAt:       time.Unix(p.CreatedAt, 0),
		// Never null, the UI joins it with the current key.
		OldPublicKeys: append([]string{}, p.OldKeys()...),
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
