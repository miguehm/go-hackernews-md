package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"
	_ "time/tzdata"

	"hnmd/internal/api"
	"hnmd/internal/config"
	"hnmd/internal/service"
	"hnmd/internal/store"
	"hnmd/internal/todo"
)

var version = "dev"

const shutdownTimeout = 5 * time.Second

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck(os.Getenv, os.Stderr))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Getenv, os.Stderr)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// refresher is the background snapshot loop (service.Service in production).
type refresher interface {
	StartRefresher(ctx context.Context) <-chan struct{}
}

// run loads the configuration, wires the production dependencies and serves
// until ctx is canceled.
func run(ctx context.Context, getenv func(string) string, stderr io.Writer) error {
	cfg, err := config.Load(getenv)
	if err != nil {
		return fmt.Errorf("invalid configuration:\n%w", err)
	}
	logger := newLogger(stderr, cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(logger)
	for _, warning := range cfg.Warnings {
		logger.Warn("configuration", "warning", warning)
	}

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", ":"+cfg.Port)
	if err != nil {
		return fmt.Errorf("listen on :%s: %w", cfg.Port, err)
	}

	st := store.New()
	svc := service.New(service.Config{
		TopN:            cfg.TopN,
		RefreshInterval: cfg.RefreshInterval,
		MaxConcurrency:  cfg.MaxConcurrency,
		ScrapeTimeout:   cfg.ScrapeTimeout,
		MaxBodySize:     cfg.MaxBodySize,
		HNTimeout:       cfg.HNTimeout,
	}, st)
	return serve(ctx, cfg, logger, ln, st, svc)
}

// serve runs the HTTP server on ln and the refresher until ctx is canceled,
// then shuts both down gracefully.
func serve(ctx context.Context, cfg config.Config, logger *slog.Logger, ln net.Listener, st *store.Store, ref refresher) error {
	todoStore, err := todo.NewStore(cfg.TodoFile, cfg.TodoMaxTasks)
	if err != nil {
		_ = ln.Close()
		return fmt.Errorf("load todo store: %w", err)
	}
	logger.Info("todo store loaded", "file", cfg.TodoFile, "tasks", todoStore.Count())
	logger.Info("cors allowlist", "origins", cfg.CORSOrigins)
	if !cfg.AuthEnabled() {
		logger.Warn("API key auth disabled (API_KEYS not set); /v1 endpoints are unauthenticated")
	}

	handler := api.NewRouter(api.RouterDeps{
		HN:                      st,
		Todos:                   todoStore,
		APIKeys:                 cfg.APIKeys,
		ProtectHealth:           cfg.ProtectHealth,
		CORSOrigins:             cfg.CORSOrigins,
		PublicBaseURL:           cfg.PublicBaseURL,
		TodoAllowInsecureWrites: cfg.TodoAllowInsecureWrites,
		TodoLocation:            cfg.TodoLocation,
		Version:                 buildVersion(),
		Logger:                  logger,
	})
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	refreshCtx, stopRefresh := context.WithCancel(ctx)
	defer stopRefresh()
	refresherDone := ref.StartRefresher(refreshCtx)
	// The listener is already bound, so log before Serve: logging inside the
	// goroutine could lose the line if shutdown wins the race.
	logger.Info("listening", "addr", ln.Addr().String(), "refresh_interval", cfg.RefreshInterval.String(), "version", buildVersion())
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	var runErr error
	select {
	case <-ctx.Done():
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = fmt.Errorf("server error: %w", err)
		}
	}

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("http shutdown incomplete", "error", err)
	}
	stopRefresh()
	select {
	case <-refresherDone:
	case <-shutdownCtx.Done():
		logger.Warn("refresher did not stop before shutdown timeout")
	}
	return runErr
}

// healthcheck probes GET /health on the local server and returns the process
// exit code (0 healthy, 1 otherwise). It exists for Docker HEALTHCHECK in the
// distroless image, which has no curl or shell.
func healthcheck(getenv func(string) string, stderr io.Writer) int {
	cfg, err := config.Load(getenv)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+cfg.Port+"/health", nil)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if cfg.ProtectHealth && cfg.AuthEnabled() {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKeys[0])
	}
	// One-shot probe: no keep-alive, so no idle connection outlives it.
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(stderr, "unhealthy: status %d\n", resp.StatusCode)
		return 1
	}
	return 0
}

func newLogger(w io.Writer, level slog.Level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}
	if format == "text" {
		return slog.New(slog.NewTextHandler(w, opts))
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}

func buildVersion() string {
	if version != "" && version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			return info.Main.Version
		}
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && setting.Value != "" {
				return setting.Value
			}
		}
	}
	return "dev"
}
