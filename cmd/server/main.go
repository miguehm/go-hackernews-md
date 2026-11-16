package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"hnmd/internal/api"
	"hnmd/internal/service"
	"hnmd/internal/store"
)

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

func main() {
	port := getenv("PORT", "8982")
	refreshStr := getenv("REFRESH_INTERVAL", "30m")
	refreshInterval, err := time.ParseDuration(refreshStr)
	if err != nil {
		log.Fatalf("invalid REFRESH_INTERVAL: %v", err)
	}
	scrapeTimeout, _ := time.ParseDuration(getenv("SCRAPE_TIMEOUT", "10s"))
	hnTimeout, _ := time.ParseDuration(getenv("HN_TIMEOUT", "10s"))

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

	svc.StartRefresher(ctx)

	apiKeys := parseAPIKeys(os.Getenv("API_KEYS"))
	protectHealth := parseBoolEnv("PROTECT_HEALTH", false)
	authEnabled := len(apiKeys) > 0
	if !authEnabled {
		log.Println("warning: API key auth disabled (API_KEYS not set); /v1 endpoints are unauthenticated")
	}

	h := api.New(st, int(refreshInterval.Seconds()))
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

	register("/v1/top", http.HandlerFunc(h.Index))
	register("/v1/articles/{itemId}", http.HandlerFunc(h.Article))
	register("/top", http.HandlerFunc(h.Top))

	if protectHealth && auth != nil {
		mux.Handle("/health", auth(healthHandler))
	} else {
		mux.Handle("/health", healthHandler)
	}

	srv := &http.Server{Addr: ":" + port, Handler: mux}

	go func() {
		log.Printf("listening on :%s (refresh every %s)", port, refreshInterval)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
