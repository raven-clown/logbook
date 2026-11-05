package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/raven-clown/logbook"
	"github.com/raven-clown/logbook/admin"
	"github.com/raven-clown/logbook/internal/config"
	"github.com/raven-clown/logbook/internal/httpx"
	"github.com/raven-clown/logbook/internal/identity"
	"github.com/raven-clown/logbook/internal/mcpserver"
	"github.com/raven-clown/logbook/internal/obs"
	"github.com/raven-clown/logbook/internal/rest"
	"github.com/raven-clown/logbook/memory"
)

type deps struct {
	cfg     config.Config
	store   memory.Store
	access  admin.Store
	svc     *memory.Service
	metrics *obs.Metrics
	logger  *slog.Logger
	now     func() time.Time
}

type pinger interface {
	Ping(ctx context.Context) error
}

// newHandler builds every route of logbook serve.
func newHandler(d deps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		body := map[string]string{"status": "ready", "database": "ok", "transponder": "not configured"}
		status := http.StatusOK
		if p, ok := d.store.(pinger); ok {
			if err := p.Ping(r.Context()); err != nil {
				d.logger.WarnContext(r.Context(), "database ping failed", "err", err)
				body["status"], body["database"], status = "not ready", "unavailable", http.StatusServiceUnavailable
			}
		}
		httpx.WriteJSON(w, status, body)
	})
	mux.Handle("GET /metrics", d.metrics.Handler())

	challenge := "Bearer"
	if d.cfg.PublicURL != "" && d.cfg.Auth.OIDC.Issuer != "" {
		resource := strings.TrimSuffix(d.cfg.PublicURL, "/") + "/mcp"
		path := server.ProtectedResourceMetadataPath(resource)
		mux.Handle("GET "+path, server.NewProtectedResourceMetadataHandler(server.ProtectedResourceMetadataConfig{
			Resource: resource, AuthorizationServers: []string{d.cfg.Auth.OIDC.Issuer},
			BearerMethodsSupported: []string{"header"}, ResourceName: "logbook",
		}))
		challenge = fmt.Sprintf(`Bearer resource_metadata=%q`, strings.TrimSuffix(d.cfg.PublicURL, "/")+path)
	}

	mcp := mcpserver.New(d.svc, mcpserver.HTTPPrincipal, logbook.Version)
	mux.Handle("/mcp", mcpserver.HTTPHandler(mcp, mcpserver.HTTPOptions{DefaultProject: d.cfg.DefaultProject}))
	rest.Register(mux, rest.Options{Service: d.svc, DefaultProject: d.cfg.DefaultProject})

	res := identity.New(identity.Options{
		Store: d.access, OIDC: d.cfg.Auth.OIDC, ProjectMode: d.cfg.Auth.ProjectMode,
		BootstrapAdmins: d.cfg.Auth.BootstrapAdmins, Now: d.now,
	})
	authed := identity.Middleware(res, func(w http.ResponseWriter, r *http.Request, err error) {
		if memory.CodeOf(err) == memory.CodeUnauthenticated {
			w.Header().Set("WWW-Authenticate", challenge)
		}
		httpx.WriteError(w, r, err)
	})(obs.Routes(mux))
	open := obs.Routes(mux)
	protect := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mcp" || strings.HasPrefix(r.URL.Path, "/v1/") {
			authed.ServeHTTP(w, r)
			return
		}
		open.ServeHTTP(w, r)
	})
	return obs.RequestID(obs.Trace(obs.AccessLog(d.logger, d.metrics.Wrap(protect))))
}

func (a *app) serve(args []string) error {
	fs, c := a.flags("serve")
	listen := fs.String("listen", "", "address to listen on, overrides listen_addr")
	if _, err := parse(fs, args, 0, "serve [--config file] [--listen addr]"); err != nil {
		return err
	}
	s, err := a.open(c)
	if err != nil {
		return err
	}
	defer func() { _ = s.store.Close() }()
	access, ok := s.store.(admin.Store)
	if !ok {
		return fmt.Errorf("the %s storage driver does not store API keys and bindings", s.cfg.Database.Driver)
	}
	logger := obs.NewLogger(a.stdout, slog.LevelInfo)
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(a.ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdownTracing, err := obs.SetupTracing(ctx, s.cfg.Telemetry.OTLPEndpoint, logbook.Version)
	if err != nil {
		return err
	}
	defer func() { _ = shutdownTracing(context.Background()) }()

	addr := s.cfg.ListenAddr
	if *listen != "" {
		addr = *listen
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", addr, err)
	}
	srv := &http.Server{
		Handler:           newHandler(deps{cfg: s.cfg, store: s.store, access: access, svc: s.svc, metrics: obs.NewMetrics(), logger: logger, now: s.now}),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
	logger.Info("listening", "addr", ln.Addr().String(), "version", logbook.Version)
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	logger.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
