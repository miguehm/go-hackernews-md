package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"hnmd/internal/api"
	"hnmd/internal/service"
	"hnmd/internal/store"
	"hnmd/internal/todo"
)

var version = "dev"

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseBoolEnv(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

func parseIntEnv(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		slog.Warn("invalid integer env var, using default", "key", key, "value", v, "error", err, "default", def)
		return def
	}
	return n
}

// parseDurationEnv reads a strictly positive duration. A zero or negative
// value is rejected: it would disable HTTP client timeouts or panic the ticker.
func parseDurationEnv(key, def string) (time.Duration, error) {
	raw := strings.TrimSpace(getenv(key, def))
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", key, raw, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("invalid %s %q: must be greater than zero", key, raw)
	}
	return d, nil
}

func loadLocationEnv(key string, def *time.Location) *time.Location {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	location, err := time.LoadLocation(raw)
	if err != nil {
		slog.Warn("invalid timezone env var, using UTC", "key", key, "value", raw, "error", err)
		return time.UTC
	}
	return location
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

func parseAPIKeys(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	keys := make([]string, 0, len(parts))
	for _, p := range parts {
		k := strings.TrimSpace(p)
		if k != "" {
			keys = append(keys, k)
		}
	}
	return keys
}

func parseCORSOrigins(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return append([]string(nil), api.DefaultCORSOrigins...)
	}
	parts := strings.Split(raw, ",")
	origins := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, part := range parts {
		origin := strings.TrimSpace(part)
		if origin == "" || origin == "*" || seen[origin] {
			continue
		}
		seen[origin] = true
		origins = append(origins, origin)
	}
	if len(origins) == 0 {
		return append([]string(nil), api.DefaultCORSOrigins...)
	}
	return origins
}

// newLogger builds the process logger from LOG_LEVEL (debug, info, warn,
// error; default info) and LOG_FORMAT (json or text; default json).
func newLogger(w io.Writer, level, format string) (*slog.Logger, error) {
	var lvl slog.Level
	if strings.TrimSpace(level) != "" {
		if err := lvl.UnmarshalText([]byte(strings.TrimSpace(level))); err != nil {
			return nil, fmt.Errorf("invalid LOG_LEVEL %q: %w", level, err)
		}
	}
	opts := &slog.HandlerOptions{Level: lvl}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "json":
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	case "text":
		return slog.New(slog.NewTextHandler(w, opts)), nil
	default:
		return nil, fmt.Errorf("invalid LOG_FORMAT %q: want json or text", format)
	}
}

func fatal(msg string, err error) {
	slog.Error(msg, "error", err)
	os.Exit(1)
}

func main() {
	logger, err := newLogger(os.Stderr, os.Getenv("LOG_LEVEL"), os.Getenv("LOG_FORMAT"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	slog.SetDefault(logger)

	port := getenv("PORT", "8982")
	refreshInterval, err := parseDurationEnv("REFRESH_INTERVAL", "30m")
	if err != nil {
		fatal("invalid configuration", err)
	}
	scrapeTimeout, err := parseDurationEnv("SCRAPE_TIMEOUT", "10s")
	if err != nil {
		fatal("invalid configuration", err)
	}
	hnTimeout, err := parseDurationEnv("HN_TIMEOUT", "10s")
	if err != nil {
		fatal("invalid configuration", err)
	}

	cfg := service.Config{
		TopN:            20,
		RefreshInterval: refreshInterval,
		MaxConcurrency:  6,
		ScrapeTimeout:   scrapeTimeout,
		MaxBodySize:     5 * 1024 * 1024,
		HNTimeout:       hnTimeout,
	}

	st := store.New()
	svc := service.New(cfg, st)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	refresherDone := svc.StartRefresher(ctx)

	apiKeys := parseAPIKeys(os.Getenv("API_KEYS"))
	protectHealth := parseBoolEnv("PROTECT_HEALTH", false)
	corsOrigins := parseCORSOrigins(os.Getenv("CORS_ORIGINS"))
	slog.Info("cors allowlist", "origins", corsOrigins)
	authEnabled := len(apiKeys) > 0
	if !authEnabled {
		slog.Warn("API key auth disabled (API_KEYS not set); /v1 endpoints are unauthenticated")
	}

	dataDir := getenv("DATA_DIR", "./data")
	todoFile := getenv("TODO_FILE", filepath.Join(dataDir, "todos.json"))
	todoMaxTasks := parseIntEnv("TODO_MAX_TASKS", 1000)
	todoAllowInsecureWrites := parseBoolEnv("TODO_ALLOW_INSECURE_WRITES", false)
	todoStore, err := todo.NewStore(todoFile, todoMaxTasks)
	if err != nil {
		fatal("failed to load todo store", err)
	}
	slog.Info("todo store loaded", "file", todoFile, "tasks", todoStore.Count())
	todoLocation := loadLocationEnv("TODO_TIMEZONE", time.UTC)
	todoHandler := api.NewTodoHandlerWithLocation(todoStore, os.Getenv("PUBLIC_BASE_URL"), authEnabled || todoAllowInsecureWrites, todoLocation)

	h := api.New(st, api.DefaultWarmupRetryAfter)
	statusHandler := api.NewStatusHandler(st, todoStore, authEnabled, buildVersion())
	mux := http.NewServeMux()

	healthHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	var auth func(http.Handler) http.Handler
	if authEnabled {
		auth = api.BearerAPIKeyAuth(apiKeys)
	}
	register := func(pattern string, handler http.Handler) {
		if auth != nil {
			mux.Handle(pattern, auth(handler))
			return
		}
		mux.Handle(pattern, handler)
	}

	register("/v1/status", statusHandler)
	register("/v1/top", http.HandlerFunc(h.Index))
	register("/v1/articles/{itemId}", http.HandlerFunc(h.Article))
	register("/top", http.HandlerFunc(h.Top))

	register("/v1/todos", http.HandlerFunc(todoHandler.Index))
	register("/v1/todos.md", http.HandlerFunc(todoHandler.Markdown))
	register("/v1/todos/tags", http.HandlerFunc(todoHandler.Tags))
	register("/v1/todos/tags/{tag}", http.HandlerFunc(todoHandler.TagsMarkdown))
	register("/v1/todos/top", http.HandlerFunc(todoHandler.Top))
	register("/v1/todos/tasks", http.HandlerFunc(todoHandler.Tasks))
	register("/v1/todos/tasks/{id}", http.HandlerFunc(todoHandler.Task))

	if protectHealth && auth != nil {
		mux.Handle("/health", auth(healthHandler))
	} else {
		mux.Handle("/health", healthHandler)
	}

	// Order matters: Recover wraps Gzip so a panic never leaves a partial
	// response; AccessLog sits outside Recover to log the resulting 500.
	handler := api.RequestID(
		api.AccessLog(logger)(
			api.Recover(logger)(
				api.GzipMiddleware(api.CORS(corsOrigins)(mux)))))
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		slog.Info("listening", "addr", srv.Addr, "refresh_interval", refreshInterval.String(), "version", buildVersion())
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fatal("server error", err)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("http shutdown incomplete", "error", err)
	}
	select {
	case <-refresherDone:
	case <-shutdownCtx.Done():
		slog.Warn("refresher did not stop before shutdown timeout")
	}
}
