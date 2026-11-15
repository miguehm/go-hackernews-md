package service

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"hnmd/internal/hn"
	mdconv "hnmd/internal/markdown"
	"hnmd/internal/scraper"
	"hnmd/internal/store"

	"golang.org/x/sync/errgroup"
)

type Config struct {
	TopN            int
	RefreshInterval time.Duration
	MaxConcurrency  int
	ScrapeTimeout   time.Duration
	MaxBodySize     int64
	HNTimeout       time.Duration
}

type Service struct {
	cfg     Config
	hn      *hn.Client
	fetcher *scraper.Fetcher
	store   *store.Store
}

func New(cfg Config, st *store.Store) *Service {
	return &Service{
		cfg:     cfg,
		hn:      hn.NewClient(cfg.HNTimeout),
		fetcher: scraper.NewFetcher(cfg.ScrapeTimeout, cfg.MaxBodySize),
		store:   st,
	}
}

func (s *Service) Refresh(ctx context.Context) error {
	start := time.Now()
	ids, err := s.hn.TopStoryIDs(ctx)
	if err != nil {
		return fmt.Errorf("fetch top stories: %w", err)
	}
	if len(ids) > s.cfg.TopN {
		ids = ids[:s.cfg.TopN]
	}

	items := make([]store.NewsItem, len(ids))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(s.cfg.MaxConcurrency)
	var mu sync.Mutex

	for i, id := range ids {
		i, id := i, id
		g.Go(func() error {
			item := s.processOne(gctx, i+1, id)
			mu.Lock()
			items[i] = item
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait()

	now := time.Now().UTC()
	snap := &store.Snapshot{
		GeneratedAt:     now.Format(time.RFC3339),
		NextRefreshAt:   now.Add(s.cfg.RefreshInterval).Format(time.RFC3339),
		RefreshInterval: s.cfg.RefreshInterval.String(),
		Count:           len(items),
		Items:           items,
		Ready:           true,
	}
	s.store.Set(snap)
	log.Printf("refresh completed: %d items in %s", len(items), time.Since(start))
	return nil
}

func (s *Service) processOne(ctx context.Context, rank int, id int) store.NewsItem {
	it, err := s.hn.Item(ctx, id)
	if err != nil {
		return mdconv.ErrorItem(rank, id, "story", "", "", "", 0, 0, 0, err.Error())
	}
	if it == nil {
		return mdconv.ErrorItem(rank, id, "story", "", "", "", 0, 0, 0, "nil item")
	}
	if it.Deleted || it.Dead {
		msg := "item deleted or dead"
		return mdconv.ErrorItem(rank, id, it.Type, it.Title, it.By, it.URL, it.Score, it.Descendants, it.Time, msg)
	}

	var bodyMD string
	if it.URL != "" {
		htmlStr, err := s.fetcher.FetchHTML(ctx, it.URL)
		if err != nil {
			return mdconv.ErrorItem(rank, id, it.Type, it.Title, it.By, it.URL, it.Score, it.Descendants, it.Time, "fetch: "+err.Error())
		}
		contentHTML, err := scraper.ExtractContent(htmlStr, it.URL)
		if err != nil {
			return mdconv.ErrorItem(rank, id, it.Type, it.Title, it.By, it.URL, it.Score, it.Descendants, it.Time, "extract: "+err.Error())
		}
		bodyMD, err = mdconv.HTMLToMarkdown(contentHTML)
		if err != nil {
			return mdconv.ErrorItem(rank, id, it.Type, it.Title, it.By, it.URL, it.Score, it.Descendants, it.Time, "convert: "+err.Error())
		}
	} else if it.Text != "" {
		md, err := mdconv.HTMLToMarkdown(it.Text)
		if err != nil {
			bodyMD = it.Text
		} else {
			bodyMD = md
		}
	}

	return mdconv.BuildNewsItem(rank, it.ID, it.Type, it.Title, it.By, it.URL, it.Score, it.Descendants, it.Time, bodyMD, "ok", nil)
}

func (s *Service) StartRefresher(ctx context.Context) {
	go func() {
		// initial warm-up
		if err := s.Refresh(ctx); err != nil {
			log.Printf("initial refresh error: %v", err)
			s.recordRefreshError(err)
		}

		ticker := time.NewTicker(s.cfg.RefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				rctx, cancel := context.WithTimeout(context.Background(), s.cfg.RefreshInterval)
				if err := s.Refresh(rctx); err != nil {
					log.Printf("refresh error: %v", err)
					s.recordRefreshError(err)
				}
				cancel()
			}
		}
	}()
}

func (s *Service) recordRefreshError(err error) {
	snap, ok := s.store.Get()
	if ok {
		snap.LastError = err.Error()
		snap.Ready = true
		s.store.Set(snap)
		return
	}
	// no snapshot yet, store minimal not-ready state is kept as nil; handler will 503
}
