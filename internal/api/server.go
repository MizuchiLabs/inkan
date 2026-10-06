// Package api serves the HTTP API and the embedded web UI.
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/go-chi/httplog/v3"

	"github.com/mizuchilabs/kata/logx"

	"github.com/mizuchilabs/inkan/internal/db"
	"github.com/mizuchilabs/inkan/web"
)

type Server struct {
	api  huma.API
	mux  *http.ServeMux
	auth *auth
}

// New builds the server. With a password the API needs a sign in, without
// one it is open and Start only listens on this machine.
func New(q *db.Queries, password, token string) (*Server, error) {
	a, err := newAuth(password, token)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()

	cfg := huma.DefaultConfig("Inkan API", "1.0.0")
	cfg.CreateHooks = nil
	api := humago.New(mux, cfg)
	api.UseMiddleware(a.require(api))
	registerSession(api, a)
	registerProducts(api, q)
	registerLicenses(api, q)
	registerCheck(api, q)
	registerStats(api, q)

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/", web.Handler())
	return &Server{api: api, mux: mux, auth: a}, nil
}

// Spec builds the OpenAPI description of the API without starting a server.
func Spec() *huma.OpenAPI {
	// Only deriving a session key can fail, and no password means no key.
	s, _ := New(nil, "", "")
	return s.api.OpenAPI()
}

func (s *Server) Handler() http.Handler {
	// Other sites must not write through the browser, signed in or not.
	guarded := http.NewCrossOriginProtection().Handler(s.mux)
	if s.auth.key != nil {
		return guarded
	}
	// A site can point its own name at this machine and is the same origin
	// then. Without a password only a local name is let in.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !local((&url.URL{Host: r.Host}).Hostname()) {
			http.Error(
				w,
				"inkan has no password and only answers to localhost, set INKAN_PASSWORD",
				http.StatusForbidden,
			)
			return
		}
		guarded.ServeHTTP(w, r)
	})
}

// Start serves on addr until ctx is done. Without a password it only takes
// an address on this machine.
func (s *Server) Start(ctx context.Context, addr string) error {
	if s.auth.key == nil && !loopback(addr) {
		return fmt.Errorf("refusing to listen on %s without a password, set INKAN_PASSWORD", addr)
	}
	return serve(ctx, addr, s.Handler())
}

// serve runs handler on addr with request logging until ctx is done.
func serve(ctx context.Context, addr string, handler http.Handler) error {
	logger := httplog.RequestLogger(slog.Default(), &httplog.Options{
		RecoverPanics: true,
		Schema:        httplog.SchemaOTEL.Concise(logx.IsTerminal()),
		Skip: func(req *http.Request, respStatus int) bool {
			return respStatus < http.StatusBadRequest && req.URL.Path == "/healthz"
		},
	})
	server := &http.Server{
		Addr:              addr,
		Handler:           logger(handler),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		slog.Info("Server listening on", "addr", addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case <-ctx.Done():
		slog.Info("Shutting down server...")
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)

	case err := <-serverErr:
		return fmt.Errorf("server error: %w", err)
	}
}
