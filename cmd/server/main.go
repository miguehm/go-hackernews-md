package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
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

	h := api.New(st)
	mux := http.NewServeMux()
	mux.HandleFunc("/top", h.Top)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

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
