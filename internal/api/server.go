// Package api serves the HTTP API and the embedded web UI.
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/go-chi/httplog/v3"

	"github.com/mizuchilabs/kata/logx"

	"github.com/mizuchilabs/inkan/internal/db"
	"github.com/mizuchilabs/inkan/web"
)

type Server struct {
	api huma.API
	mux *http.ServeMux
}

func New(q *db.Queries) *Server {
	mux := http.NewServeMux()

	cfg := huma.DefaultConfig("Inkan API", "1.0.0")
	cfg.CreateHooks = nil
	api := humago.New(mux, cfg)
	registerProducts(api, q)
	registerLicenses(api, q)
	registerCheck(api, q)

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/", web.Handler())
	return &Server{api: api, mux: mux}
}

// Spec builds the OpenAPI description of the API without starting a server.
func Spec() *huma.OpenAPI {
	return New(nil).api.OpenAPI()
}

func (s *Server) Handler() http.Handler {
	// There is no login yet, so at least other sites cannot write through the browser.
	return http.NewCrossOriginProtection().Handler(s.mux)
}

func (s *Server) Start(ctx context.Context, addr string) error {
	return Serve(ctx, addr, s.Handler())
}

// Serve runs handler on addr with request logging until ctx is done. The
// worker command serves through it too.
func Serve(ctx context.Context, addr string, handler http.Handler) error {
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
