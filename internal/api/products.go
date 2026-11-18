package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"modernc.org/sqlite"

	"github.com/mizuchilabs/inkan/license"

	"github.com/mizuchilabs/inkan/internal/db"
	"github.com/mizuchilabs/inkan/internal/mirror"
)

// Product is one app that keys are issued for. Secrets never leave the server.
type Product struct {
	ID              int64          `json:"id"`
	Name            string         `json:"name"`
	Prefix          string         `json:"prefix"                  doc:"Visible key prefix and the app name the product verifies against"`
	Plan            string         `json:"plan"                    doc:"Default for manual licenses. The worker gives it to Stripe products without a plan of their own"`
	PublicKey       string         `json:"public_key"              doc:"Hex Ed25519 public key to build into the product"`
	OldPublicKeys   []string       `json:"old_public_keys"         doc:"Public keys from before a rotation, newest first. The product carries them next to public_key until their keys have run out" nullable:"false"`
	WorkerURL       string         `json:"worker_url"              doc:"Where the worker of the product runs. Its paid licenses are mirrored from there"`
	WorkerConnected bool           `json:"worker_connected"        doc:"Whether a worker url is stored"`
	WorkerReport    *mirror.Report `json:"worker_report,omitempty" doc:"What the worker said it runs with at the last sync, null before the first one. Its settings live in its env"`
	Syncing         bool           `json:"syncing"                 doc:"A read of the worker is asked for or running. Its outcome shows in the fields below once it is done"`
	SyncedAt        *time.Time     `json:"synced_at"               doc:"Last time the worker was read"`
	SyncError       string         `json:"sync_error"              doc:"Why the worker could not be read at the last try. Empty when it could"`
	SyncNotice      string         `json:"sync_notice"             doc:"What is off with the worker going by its last report, like an export that was not deployed yet"`
	CreatedAt       time.Time      `json:"created_at"`
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
		PrivateKey string `json:"private_key,omitempty"                                       doc:"Base64 Ed25519 private key of an existing product. A fresh keypair is made when empty"`
	}
}

type UpdateProductInput struct {
	ProductPath

	Body struct {
		Name      string `json:"name"                       minLength:"1"                    maxLength:"64"`
		Plan      string `json:"plan"                       minLength:"1"                    maxLength:"32"`
		WorkerURL string `json:"worker_url,omitempty"       pattern:"^(https?://.+)?$"       maxLength:"512" doc:"Where the worker runs. Empty disconnects it"`
	}
}

// SQLITE_CONSTRAINT_UNIQUE, the driver exports no name for it.
const sqliteUnique = 2067

type productHandlers struct {
	q      *db.Queries
	mirror *mirror.Mirror
}

func registerProducts(api huma.API, q *db.Queries, m *mirror.Mirror) {
	h := &productHandlers{q: q, mirror: m}
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
		Description: "The name and the plan only reach the worker once inkan worker export ran and it is deployed, sync_notice says so until then. A new worker url is read in the background, the product says syncing meanwhile.",
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
		OperationID:   "sync-product",
		Method:        http.MethodPost,
		Path:          "/api/products/{id}/sync",
		Summary:       "Sync a product with its worker",
		Description:   "Asks for a read of the paid licenses and the settings of the worker and answers right away, a big Stripe account takes a while. The product says syncing until the read is done. A failed read is stored on it as sync_error.",
		Tags:          []string{"Products"},
		DefaultStatus: http.StatusAccepted,
	}, h.sync)
}

func (h *productHandlers) list(ctx context.Context, _ *struct{}) (*ProductsOutput, error) {
	products, err := h.q.ListProducts(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list products")
	}
	out := make([]Product, len(products))
	for i, p := range products {
		out[i] = h.output(p)
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
	} else if priv, err := license.ParsePrivateKey(privateKey); err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	} else if !priv.Equal(ed25519.NewKeyFromSeed(priv.Seed())) {
		// The second half is the public key of the first. A key that does not fit signs what nothing verifies.
		return nil, huma.Error422UnprocessableEntity(
			"the halves of the private key do not fit, it was damaged on the way",
		)
	}

	p, err := h.q.CreateProduct(ctx, &db.CreateProductParams{
		Name:       in.Body.Name,
		Prefix:     in.Body.Prefix,
		Plan:       in.Body.Plan,
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
	return &ProductOutput{Body: h.output(p)}, nil
}

func (h *productHandlers) update(ctx context.Context, in *UpdateProductInput) (*ProductOutput, error) {
	workerURL := strings.TrimSpace(in.Body.WorkerURL)
	if workerURL != "" {
		if err := mirror.CheckURL(workerURL); err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
	}
	before, err := h.get(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	p, err := h.q.UpdateProduct(ctx, &db.UpdateProductParams{
		Name:      in.Body.Name,
		Plan:      in.Body.Plan,
		WorkerUrl: workerURL,
		ID:        in.ID,
	})
	if isUnique(err) {
		return nil, huma.Error409Conflict("a product with this name already exists")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to update product")
	}
	if !mirror.Connected(p) {
		if err := h.q.DeleteProviderLicenses(ctx, p.ID); err != nil {
			return nil, huma.Error500InternalServerError("failed to drop the licenses of the worker")
		}
	} else if p.WorkerUrl != before.WorkerUrl {
		h.mirror.SyncSoon(p.ID)
	}
	return &ProductOutput{Body: h.output(p)}, nil
}

func (h *productHandlers) rotate(ctx context.Context, in *ProductPath) (*ProductOutput, error) {
	p, err := h.get(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	current, err := p.PublicKey()
	if err != nil {
		return nil, huma.Error500InternalServerError("the product has a broken signing key")
	}
	fresh, _, err := license.GenerateKey()
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to generate a keypair")
	}
	return h.setKeys(ctx, p.ID, fresh, append([]string{current}, p.OldKeys()...))
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
	return &ProductOutput{Body: h.output(p)}, nil
}

func (h *productHandlers) delete(ctx context.Context, in *ProductPath) (*struct{}, error) {
	if err := h.q.DeleteProduct(ctx, in.ID); err != nil {
		return nil, huma.Error500InternalServerError("failed to delete product")
	}
	return nil, nil
}

func (h *productHandlers) sync(ctx context.Context, in *ProductPath) (*struct{}, error) {
	p, err := h.get(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if !mirror.Connected(p) {
		return nil, huma.Error409Conflict("the product has no worker")
	}
	h.mirror.SyncSoon(p.ID)
	return nil, nil
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

func (h *productHandlers) output(p *db.Product) Product {
	report := mirror.LastReport(p)
	out := Product{
		ID:              p.ID,
		Name:            p.Name,
		Prefix:          p.Prefix,
		Plan:            p.Plan,
		WorkerURL:       p.WorkerUrl,
		WorkerConnected: mirror.Connected(p),
		WorkerReport:    report,
		Syncing:         h.mirror.Syncing(p.ID),
		SyncError:       p.SyncError,
		// Worked out here, so it is right the moment the product changes.
		SyncNotice: mirror.Drift(p, report),
		CreatedAt:  time.Unix(p.CreatedAt, 0),
		// Never null, the UI joins it with the current key.
		OldPublicKeys: append([]string{}, p.OldKeys()...),
	}
	// A broken signing key shows as no public key.
	out.PublicKey, _ = p.PublicKey()
	if p.SyncedAt != nil {
		out.SyncedAt = new(time.Unix(*p.SyncedAt, 0))
	}
	return out
}

func isUnique(err error) bool {
	e, ok := errors.AsType[*sqlite.Error](err)
	return ok && e.Code() == sqliteUnique
}
