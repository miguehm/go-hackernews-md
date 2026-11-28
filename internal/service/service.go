package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"hnmd/internal/hn"
	mdconv "hnmd/internal/markdown"
	"hnmd/internal/scraper"
	"hnmd/internal/store"

	"golang.org/x/sync/errgroup"
)

const maxArticleBytes = 512 * 1024

type Config struct {
	TopN            int
	RefreshInterval time.Duration
	MaxConcurrency  int
	ScrapeTimeout   time.Duration
	MaxBodySize     int64
	HNTimeout       time.Duration
}

type storyClient interface {
	TopStoryIDs(ctx context.Context) ([]int, error)
	Item(ctx context.Context, id int) (*hn.Item, error)
}

type htmlFetcher interface {
	FetchHTML(ctx context.Context, url string) (string, error)
}

type Service struct {
	cfg     Config
	hn      storyClient
	fetcher htmlFetcher
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

	prevIndex := make(map[string]store.IndexItem)
	prevArticles := make(map[string]*store.Article)
	if prev, ok := s.store.Current(); ok {
		for _, item := range prev.Index.Items {
			prevIndex[item.ID] = item
		}
		for id, article := range prev.Articles {
			prevArticles[id] = article
		}
	}

	items := make([]store.IndexItem, len(ids))
	articles := make(map[string]*store.Article, len(ids))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(s.cfg.MaxConcurrency)
	var mu sync.Mutex

	for i, id := range ids {
		i, id := i, id
		g.Go(func() error {
			idStr := strconv.Itoa(id)
			var prevItem *store.IndexItem
			if v, ok := prevIndex[idStr]; ok {
				copied := v
				prevItem = &copied
			}
			item, article := s.processOne(gctx, i+1, id, prevItem, prevArticles[idStr])
			mu.Lock()
			items[i] = item
			if article != nil {
				articles[idStr] = article
			}
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait()

	now := time.Now().UTC()
	nextRefresh := now.Add(s.cfg.RefreshInterval).Format(time.RFC3339)
	refreshSeconds := int(s.cfg.RefreshInterval.Seconds())
	snap := &store.Snapshot{
		Index: store.IndexResponse{
			SchemaVersion:          1,
			GeneratedAt:            now.Format(time.RFC3339),
			NextRefreshAt:          &nextRefresh,
			RefreshIntervalSeconds: &refreshSeconds,
			ReturnedCount:          len(items),
			Truncated:              false,
			Items:                  items,
		},
		Articles: articles,
		Ready:    true,
	}
	s.store.Set(snap)
	log.Printf("refresh completed: %d items in %s", len(items), time.Since(start))
	return nil
}

func (s *Service) processOne(ctx context.Context, rank int, id int, prevItem *store.IndexItem, prevArticle *store.Article) (store.IndexItem, *store.Article) {
	it, err := s.hn.Item(ctx, id)
	if err != nil {
		if prevItem != nil && prevArticle != nil {
			return retainedItem(rank, id, prevItem, prevArticle), prevArticle
		}
		return newErrorItem(rank, id, store.StatusError, err.Error()), nil
	}
	if it == nil || it.ID == 0 {
		if prevItem != nil && prevArticle != nil {
			return retainedItem(rank, id, prevItem, prevArticle), prevArticle
		}
		return newErrorItem(rank, id, store.StatusNoContent, "item unavailable"), nil
	}

	item := indexItemFromHN(rank, id, it)
	if it.Deleted || it.Dead {
		return s.finishArticle(item, "", nil)
	}

	var body string
	if it.URL != "" {
		htmlStr, fetchErr := s.fetcher.FetchHTML(ctx, it.URL)
		if fetchErr != nil {
			if prevArticle == nil {
				return errorWithMetadata(item, "fetch: "+fetchErr.Error()), nil
			}
			body = string(prevArticle.Content)
		} else {
			contentHTML, extractErr := scraper.ExtractContent(htmlStr, it.URL)
			if extractErr != nil {
				if prevArticle == nil {
					return errorWithMetadata(item, "extract: "+extractErr.Error()), nil
				}
				body = string(prevArticle.Content)
			} else {
				md, convertErr := mdconv.HTMLToMarkdown(contentHTML)
				if convertErr != nil {
					if prevArticle == nil {
						return errorWithMetadata(item, "convert: "+convertErr.Error()), nil
					}
					body = string(prevArticle.Content)
				} else {
					body = md
				}
			}
		}
	} else if it.Text != "" {
		md, convertErr := mdconv.HTMLToMarkdown(it.Text)
		if convertErr != nil {
			body = it.Text
		} else {
			body = md
		}
	}

	return s.finishArticle(item, body, prevArticle)
}

func (s *Service) finishArticle(item store.IndexItem, body string, prevArticle *store.Article) (store.IndexItem, *store.Article) {
	now := time.Now().UTC()
	meta := metaFromItem(item)
	contentBody := body
	md := mdconv.Render(meta, contentBody)
	if len(md) > maxArticleBytes {
		if prevArticle != nil {
			contentBody = string(prevArticle.Content)
			md = mdconv.Render(meta, contentBody)
		}
		if len(md) > maxArticleBytes {
			return errorWithMetadata(item, "too_large"), nil
		}
	}

	article := &store.Article{
		Markdown:          []byte(md),
		Content:           []byte(strings.TrimSpace(contentBody)),
		ContentType:       mdconv.ContentType,
		SuggestedFilename: mdconv.SuggestedFilename(item.ID, item.Title),
		ContentLength:     len(md),
		ETag:              quoteETag(md),
		LastModified:      now,
		UpdatedAt:         now,
	}
	setContentFields(&item, article)
	return item, article
}

// StartRefresher runs the refresh loop in the background until ctx is
// canceled. The returned channel is closed once the loop has fully stopped.
func (s *Service) StartRefresher(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		// initial warm-up
		s.refreshOnce(ctx, "initial refresh")

		ticker := time.NewTicker(s.cfg.RefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.refreshOnce(ctx, "refresh")
			}
		}
	}()
	return done
}

// refreshOnce runs a single refresh bounded by the refresh interval and
// derived from ctx, so shutdown cancels in-flight work.
func (s *Service) refreshOnce(ctx context.Context, label string) {
	rctx, cancel := context.WithTimeout(ctx, s.cfg.RefreshInterval)
	defer cancel()
	if err := s.Refresh(rctx); err != nil {
		if ctx.Err() != nil {
			return
		}
		log.Printf("%s error: %v", label, err)
		s.recordRefreshError(err)
	}
}

func (s *Service) recordRefreshError(err error) {
	prev, ok := s.store.Current()
	if !ok {
		return
	}
	next := *prev
	next.LastError = err.Error()
	s.store.Set(&next)
}

func indexItemFromHN(rank int, id int, it *hn.Item) store.IndexItem {
	item := store.IndexItem{
		ID:            strconv.Itoa(id),
		Rank:          rank,
		Type:          it.Type,
		Title:         it.Title,
		HNURL:         fmt.Sprintf("https://news.ycombinator.com/item?id=%d", id),
		ContentStatus: store.StatusOK,
	}
	if it.URL != "" {
		v := it.URL
		item.URL = &v
	}
	if it.By != "" {
		v := it.By
		item.By = &v
	}
	score := it.Score
	item.Score = &score
	descendants := it.Descendants
	item.Descendants = &descendants
	if it.Time != 0 {
		published := time.Unix(it.Time, 0).UTC().Format(time.RFC3339)
		item.PublishedAt = &published
	}
	if domain := mdconv.DomainFromURL(it.URL); domain != "" {
		item.Domain = &domain
	}
	return item
}

func metaFromItem(item store.IndexItem) mdconv.Meta {
	meta := mdconv.Meta{Title: item.Title, HNURL: item.HNURL}
	if item.URL != nil {
		meta.URL = *item.URL
	}
	if item.By != nil {
		meta.By = *item.By
	}
	if item.Score != nil {
		meta.Score = *item.Score
	}
	if item.Descendants != nil {
		meta.Descendants = *item.Descendants
	}
	if item.PublishedAt != nil {
		if published, err := time.Parse(time.RFC3339, *item.PublishedAt); err == nil {
			meta.PublishedDate = published.Format("2006-01-02")
		} else {
			meta.PublishedDate = *item.PublishedAt
		}
	}
	return meta
}

func retainedItem(rank int, id int, prevItem *store.IndexItem, prevArticle *store.Article) store.IndexItem {
	item := *prevItem
	item.ID = strconv.Itoa(id)
	item.Rank = rank
	setContentFields(&item, prevArticle)
	return item
}

func setContentFields(item *store.IndexItem, article *store.Article) {
	contentURL := "/v1/articles/" + item.ID + ".md"
	contentType := article.ContentType
	filename := article.SuggestedFilename
	length := article.ContentLength
	updatedAt := article.UpdatedAt.UTC().Format(time.RFC3339)
	item.ContentStatus = store.StatusOK
	item.ContentError = nil
	item.ContentURL = &contentURL
	item.ContentType = &contentType
	item.SuggestedFilename = &filename
	item.ContentLength = &length
	item.UpdatedAt = &updatedAt
}

func clearContentFields(item *store.IndexItem) {
	item.ContentURL = nil
	item.ContentType = nil
	item.SuggestedFilename = nil
	item.ContentLength = nil
}

func newErrorItem(rank int, id int, status store.ContentStatus, message string) store.IndexItem {
	now := time.Now().UTC().Format(time.RFC3339)
	return store.IndexItem{
		ID:            strconv.Itoa(id),
		Rank:          rank,
		HNURL:         fmt.Sprintf("https://news.ycombinator.com/item?id=%d", id),
		ContentStatus: status,
		ContentError:  &message,
		UpdatedAt:     &now,
	}
}

func errorWithMetadata(item store.IndexItem, message string) store.IndexItem {
	item.ContentStatus = store.StatusError
	item.ContentError = &message
	clearContentFields(&item)
	now := time.Now().UTC().Format(time.RFC3339)
	item.UpdatedAt = &now
	return item
}

func quoteETag(data string) string {
	sum := sha256.Sum256([]byte(data))
	return `"` + hex.EncodeToString(sum[:]) + `"`
}
