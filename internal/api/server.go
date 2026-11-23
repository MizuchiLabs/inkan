package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/go-chi/httplog/v3"

	"github.com/mizuchilabs/kata/logx"
	"github.com/mizuchilabs/nushi"

	"github.com/mizuchilabs/inkan/internal/db"
	"github.com/mizuchilabs/inkan/internal/mirror"
	"github.com/mizuchilabs/inkan/web"
)

type Server struct {
	api   huma.API
	mux   *http.ServeMux
	guard *nushi.Guard
}

func New(q *db.Queries, m *mirror.Mirror, guard *nushi.Guard) *Server {
	routes := http.NewServeMux()

	cfg := huma.DefaultConfig("Inkan API", "1.0.0")
	cfg.CreateHooks = nil
	// Under /api, so the sign in covers them too.
	cfg.DocsPath, cfg.OpenAPIPath, cfg.SchemasPath = "/api/docs", "/api/openapi", "/api/schemas"
	api := humago.New(routes, cfg)
	registerProducts(api, q, m)
	registerLicenses(api, q)
	registerCheck(api, q)
	registerStats(api, q)

	mux := http.NewServeMux()
	mux.Handle("/api/", guard.Require(routes))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/", web.Handler())
	return &Server{api: api, mux: mux, guard: guard}
}

func Spec() *huma.OpenAPI {
	// Only the settings can fail, and there are none.
	guard, _ := nushi.New("inkan", nushi.Config{})
	return New(nil, mirror.New(nil), guard).api.OpenAPI()
}

func (s *Server) Handler() http.Handler {
	return s.guard.Protect(s.mux)
}

func (s *Server) Start(ctx context.Context, addr string) error {
	ln, err := s.guard.Listen(ctx, addr)
	if err != nil {
		return err
	}
	return serve(ctx, ln, s.Handler())
}

func serve(ctx context.Context, ln net.Listener, handler http.Handler) error {
	logger := httplog.RequestLogger(slog.Default(), &httplog.Options{
		RecoverPanics: true,
		Schema:        httplog.SchemaOTEL.Concise(logx.IsTerminal()),
		Skip: func(req *http.Request, respStatus int) bool {
			if respStatus >= http.StatusBadRequest {
				return false
			}
			p := req.URL.Path
			return p == "/healthz" || !strings.HasPrefix(p, "/api/")
		},
	})
	server := &http.Server{
		Handler:           logger(handler),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	slog.Info("server listening", "url", displayURL(ln.Addr()), "bind_address", ln.Addr().String())

	serverErr := make(chan error, 1)
	go func() {
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case <-ctx.Done():
		slog.Info("shutting down server")
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)

	case err := <-serverErr:
		return fmt.Errorf("server error: %w", err)
	}
}

func displayURL(a net.Addr) string {
	tcp, ok := a.(*net.TCPAddr)
	if !ok {
		return ""
	}
	host := tcp.IP.String()
	if tcp.IP.IsUnspecified() {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(tcp.Port))
}
