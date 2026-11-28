package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hnmd/internal/hn"
	mdconv "hnmd/internal/markdown"
	"hnmd/internal/store"
)

type fakeStoryClient struct {
	ids      []int
	items    map[int]*hn.Item
	topErr   error
	itemErrs map[int]error
	mu       sync.Mutex
	calls    int
}

func (f *fakeStoryClient) TopStoryIDs(ctx context.Context) ([]int, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return f.ids, f.topErr
}

func (f *fakeStoryClient) Item(ctx context.Context, id int) (*hn.Item, error) {
	if err, ok := f.itemErrs[id]; ok {
		return nil, err
	}
	return f.items[id], nil
}

func (f *fakeStoryClient) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type fakeFetcher struct {
	html  string
	err   error
	calls int
	mu    sync.Mutex
}

func (f *fakeFetcher) FetchHTML(ctx context.Context, url string) (string, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return f.html, f.err
}

func testConfig() Config {
	return Config{
		TopN:            20,
		RefreshInterval: 30 * time.Minute,
		MaxConcurrency:  2,
		ScrapeTimeout:   time.Second,
		MaxBodySize:     1 << 20,
		HNTimeout:       time.Second,
	}
}

const articleHTML = `<html><head><title>Example</title></head><body>` +
	`<article><h1>Hello</h1><p>This is the article body with enough text to be extracted properly by the readability algorithm.</p></article>` +
	`</body></html>`

func prevItemFixture() *store.IndexItem {
	return &store.IndexItem{
		ID: "1", Rank: 1, Title: "Old",
		HNURL:         "https://news.ycombinator.com/item?id=1",
		ContentStatus: store.StatusOK,
	}
}

func prevArticleFixture() *store.Article {
	return &store.Article{
		Markdown:          []byte("old body"),
		Content:           []byte("old body"),
		ContentType:       mdconv.ContentType,
		SuggestedFilename: "1-old.md",
		ContentLength:     8,
		UpdatedAt:         time.Now().UTC(),
	}
}

func TestNew(t *testing.T) {
	st := store.New()
	s := New(testConfig(), st)
	if s == nil || s.hn == nil || s.fetcher == nil || s.store != st {
		t.Fatalf("New() = %+v, want populated service using given store", s)
	}
	if s.cfg.TopN != 20 {
		t.Fatalf("cfg = %+v", s.cfg)
	}
}

func TestRefreshBuildsSnapshot(t *testing.T) {
	st := store.New()
	sc := &fakeStoryClient{
		ids: []int{3, 1, 2},
		items: map[int]*hn.Item{
			3: {ID: 3, Type: "story", Title: "Three"},
			1: {ID: 1, Type: "story", Title: "One"},
			2: {ID: 2, Type: "story", Title: "Two"},
		},
	}
	svc := &Service{cfg: testConfig(), hn: sc, fetcher: &fakeFetcher{}, store: st}
	if err := svc.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	snap, ok := st.Current()
	if !ok || snap == nil {
		t.Fatal("expected a ready snapshot")
	}
	if snap.Index.SchemaVersion != 1 || !snap.Ready || snap.Index.Truncated || snap.Index.ReturnedCount != 3 {
		t.Fatalf("index = %+v", snap.Index)
	}
	if len(snap.Index.Items) != 3 {
		t.Fatalf("items = %+v", snap.Index.Items)
	}
	for i, wantID := range []string{"3", "1", "2"} {
		if snap.Index.Items[i].ID != wantID || snap.Index.Items[i].Rank != i+1 {
			t.Fatalf("item %d = %+v, want ID %s rank %d", i, snap.Index.Items[i], wantID, i+1)
		}
	}
	if _, err := time.Parse(time.RFC3339, snap.Index.GeneratedAt); err != nil {
		t.Fatalf("GeneratedAt = %q: %v", snap.Index.GeneratedAt, err)
	}
	if snap.Index.NextRefreshAt == nil || snap.Index.RefreshIntervalSeconds == nil || *snap.Index.RefreshIntervalSeconds != 1800 {
		t.Fatalf("refresh metadata = %v, %v", snap.Index.NextRefreshAt, snap.Index.RefreshIntervalSeconds)
	}
	for _, id := range []string{"1", "2", "3"} {
		article := snap.Articles[id]
		if article == nil || len(article.Markdown) == 0 {
			t.Fatalf("article %s = %+v", id, article)
		}
	}
}

func TestRefreshRespectsTopN(t *testing.T) {
	st := store.New()
	sc := &fakeStoryClient{ids: []int{1, 2, 3, 4, 5}}
	cfg := testConfig()
	cfg.TopN = 2
	svc := &Service{cfg: cfg, hn: sc, fetcher: &fakeFetcher{}, store: st}
	if err := svc.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	snap, _ := st.Current()
	if len(snap.Index.Items) != 2 || snap.Index.Items[0].ID != "1" || snap.Index.Items[1].ID != "2" {
		t.Fatalf("items = %+v", snap.Index.Items)
	}
}

func TestRefreshTopStoryIDsError(t *testing.T) {
	st := store.New()
	sc := &fakeStoryClient{topErr: errors.New("offline")}
	svc := &Service{cfg: testConfig(), hn: sc, fetcher: &fakeFetcher{}, store: st}
	err := svc.Refresh(context.Background())
	if err == nil || !strings.Contains(err.Error(), "fetch top stories: offline") {
		t.Fatalf("err = %v", err)
	}
	if _, ok := st.Current(); ok {
		t.Fatal("snapshot should not be set when top stories fail")
	}
}

func TestRefreshRetainsPreviousArticle(t *testing.T) {
	st := store.New()
	st.Set(&store.Snapshot{
		Ready:    true,
		Index:    store.IndexResponse{SchemaVersion: 1, Items: []store.IndexItem{*prevItemFixture()}},
		Articles: map[string]*store.Article{"1": prevArticleFixture()},
	})
	sc := &fakeStoryClient{ids: []int{1}, itemErrs: map[int]error{1: errors.New("boom")}}
	svc := &Service{cfg: testConfig(), hn: sc, fetcher: &fakeFetcher{}, store: st}
	if err := svc.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	snap, _ := st.Current()
	if snap.Index.Items[0].ContentStatus != store.StatusOK {
		t.Fatalf("item = %+v, want retained ok status", snap.Index.Items[0])
	}
	if article := snap.Articles["1"]; article == nil || string(article.Content) != "old body" {
		t.Fatalf("retained article = %+v", article)
	}
}

func TestRefreshWithoutPreviousSnapshot(t *testing.T) {
	st := store.New()
	sc := &fakeStoryClient{ids: []int{1}, itemErrs: map[int]error{1: errors.New("boom")}}
	svc := &Service{cfg: testConfig(), hn: sc, fetcher: &fakeFetcher{}, store: st}
	if err := svc.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	snap, _ := st.Current()
	if snap.Index.Items[0].ContentStatus != store.StatusError {
		t.Fatalf("item = %+v, want error status", snap.Index.Items[0])
	}
	if len(snap.Articles) != 0 {
		t.Fatalf("articles = %+v, want none", snap.Articles)
	}
}

func TestProcessOne(t *testing.T) {
	tests := []struct {
		name        string
		item        *hn.Item
		itemErr     error
		fetchHTML   string
		fetchErr    error
		withPrev    bool
		wantStatus  store.ContentStatus
		wantErr     string
		wantContent string
		wantArticle bool
	}{
		{
			name: "item error without previous", itemErr: errors.New("boom"),
			wantStatus: store.StatusError, wantErr: codeHNUnavailable,
		},
		{
			name: "item error with previous", itemErr: errors.New("boom"), withPrev: true,
			wantStatus: store.StatusOK, wantContent: "old body", wantArticle: true,
		},
		{
			name:       "nil item without previous",
			wantStatus: store.StatusNoContent, wantErr: codeItemUnavailable,
		},
		{
			name: "nil item with previous", withPrev: true,
			wantStatus: store.StatusOK, wantContent: "old body", wantArticle: true,
		},
		{
			name: "deleted item", item: &hn.Item{ID: 1, Title: "Gone", Deleted: true},
			wantStatus: store.StatusOK, wantArticle: true,
		},
		{
			name: "dead item", item: &hn.Item{ID: 1, Title: "Dead", Dead: true},
			wantStatus: store.StatusOK, wantArticle: true,
		},
		{
			name: "fetch error without previous", item: &hn.Item{ID: 1, Title: "T", URL: "https://example.com"},
			fetchErr: errors.New("boom"), wantStatus: store.StatusError, wantErr: codeFetchFailed,
		},
		{
			name: "fetch error with previous", item: &hn.Item{ID: 1, Title: "T", URL: "https://example.com"},
			fetchErr: errors.New("boom"), withPrev: true,
			wantStatus: store.StatusOK, wantContent: "old body", wantArticle: true,
		},
		{
			name: "url success", item: &hn.Item{ID: 1, Title: "T", URL: "https://example.com"},
			fetchHTML: articleHTML, wantStatus: store.StatusOK, wantContent: "Hello", wantArticle: true,
		},
		{
			name: "text only item", item: &hn.Item{ID: 1, Title: "T", Text: "<p>Hello <b>world</b></p>"},
			wantStatus: store.StatusOK, wantContent: "Hello **world**", wantArticle: true,
		},
		{
			name: "header only item", item: &hn.Item{ID: 1, Title: "T"},
			wantStatus: store.StatusOK, wantArticle: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := &fakeStoryClient{items: map[int]*hn.Item{1: tt.item}}
			if tt.itemErr != nil {
				sc.itemErrs = map[int]error{1: tt.itemErr}
			}
			fetcher := &fakeFetcher{html: tt.fetchHTML, err: tt.fetchErr}
			svc := &Service{cfg: testConfig(), hn: sc, fetcher: fetcher, store: store.New()}

			var prevItem *store.IndexItem
			var prevArticle *store.Article
			if tt.withPrev {
				prevItem, prevArticle = prevItemFixture(), prevArticleFixture()
			}
			item, article := svc.processOne(context.Background(), 1, 1, prevItem, prevArticle)

			if item.ContentStatus != tt.wantStatus {
				t.Fatalf("status = %q, want %q", item.ContentStatus, tt.wantStatus)
			}
			if tt.wantErr != "" {
				if item.ContentError == nil || !strings.Contains(*item.ContentError, tt.wantErr) {
					t.Fatalf("content_error = %v, want containing %q", item.ContentError, tt.wantErr)
				}
			}
			if tt.wantArticle {
				if article == nil {
					t.Fatal("expected article")
				}
				if tt.wantContent != "" && !strings.Contains(string(article.Content), tt.wantContent) {
					t.Fatalf("content = %q, want containing %q", article.Content, tt.wantContent)
				}
			} else if article != nil {
				t.Fatalf("article = %+v, want nil", article)
			}
		})
	}
}

func TestRecordRefreshError(t *testing.T) {
	st := store.New()
	svc := &Service{store: st}
	svc.recordRefreshError(errors.New("fetch top stories: dial udp 10.0.0.2:53: refused"))
	if got, ok := st.LastRefreshError(); !ok || got.Message != codeHNUnavailable || got.At.IsZero() {
		t.Fatalf("LastRefreshError = %+v, %v; want %q before first snapshot", got, ok, codeHNUnavailable)
	}

	st.Set(&store.Snapshot{Ready: true, Index: store.IndexResponse{SchemaVersion: 1, GeneratedAt: "when"}})
	if _, ok := st.LastRefreshError(); ok {
		t.Fatal("a successful snapshot must clear the refresh error")
	}
	svc.recordRefreshError(fmt.Errorf("fetch top stories: %w", context.DeadlineExceeded))
	snap, ok := st.Current()
	if !ok || snap.Index.GeneratedAt != "when" {
		t.Fatalf("snapshot = %+v, %v; want previous snapshot kept", snap, ok)
	}
	if got, ok := st.LastRefreshError(); !ok || got.Message != codeRefreshTimeout {
		t.Fatalf("LastRefreshError = %+v, %v", got, ok)
	}
}

func TestIndexItemFromHN(t *testing.T) {
	item := indexItemFromHN(2, 42, &hn.Item{
		ID: 42, Type: "story", Title: "Title", By: "alice", URL: "https://example.com/a",
		Time: 1763282400, Score: 231, Descendants: 87,
	})
	if item.ID != "42" || item.Rank != 2 || item.Type != "story" || item.Title != "Title" {
		t.Fatalf("item = %+v", item)
	}
	if item.HNURL != "https://news.ycombinator.com/item?id=42" || item.ContentStatus != store.StatusOK {
		t.Fatalf("item = %+v", item)
	}
	if item.URL == nil || *item.URL != "https://example.com/a" || item.By == nil || *item.By != "alice" {
		t.Fatalf("optional fields = %+v", item)
	}
	if item.Score == nil || *item.Score != 231 || item.Descendants == nil || *item.Descendants != 87 {
		t.Fatalf("score/descendants = %+v", item)
	}
	if item.PublishedAt == nil || *item.PublishedAt != "2025-11-16T08:40:00Z" {
		t.Fatalf("published_at = %v", item.PublishedAt)
	}
	if item.Domain == nil || *item.Domain != "example.com" {
		t.Fatalf("domain = %v", item.Domain)
	}

	empty := indexItemFromHN(1, 7, &hn.Item{ID: 7})
	if empty.URL != nil || empty.By != nil || empty.PublishedAt != nil || empty.Domain != nil {
		t.Fatalf("empty optional fields = %+v", empty)
	}
	if empty.Score == nil || *empty.Score != 0 || empty.Descendants == nil || *empty.Descendants != 0 {
		t.Fatalf("zero score/descendants = %+v", empty)
	}
}

func TestNewErrorItem(t *testing.T) {
	item := newErrorItem(3, 42, store.StatusNoContent, "item unavailable")
	if item.ID != "42" || item.Rank != 3 || item.ContentStatus != store.StatusNoContent {
		t.Fatalf("item = %+v", item)
	}
	if item.HNURL != "https://news.ycombinator.com/item?id=42" {
		t.Fatalf("HNURL = %q", item.HNURL)
	}
	if item.ContentError == nil || *item.ContentError != "item unavailable" || item.UpdatedAt == nil {
		t.Fatalf("error fields = %+v", item)
	}
	if _, err := time.Parse(time.RFC3339, *item.UpdatedAt); err != nil {
		t.Fatalf("UpdatedAt = %q: %v", *item.UpdatedAt, err)
	}
}

func TestRetainedItem(t *testing.T) {
	prev := *prevItemFixture()
	article := prevArticleFixture()
	got := retainedItem(5, 42, &prev, article)
	if got.ID != "42" || got.Rank != 5 || got.Title != "Old" {
		t.Fatalf("item = %+v", got)
	}
	if got.ContentStatus != store.StatusOK || got.ContentURL == nil || *got.ContentURL != "/v1/articles/42.md" {
		t.Fatalf("content fields = %+v", got)
	}
	if prev.ID != "1" || prev.Rank != 1 {
		t.Fatalf("previous item was mutated: %+v", prev)
	}
}

func TestMetaFromItemInvalidDate(t *testing.T) {
	item := store.IndexItem{PublishedAt: strPtr("not-a-date")}
	if got := metaFromItem(item).PublishedDate; got != "not-a-date" {
		t.Fatalf("PublishedDate = %q, want raw value", got)
	}
}

func TestQuoteETag(t *testing.T) {
	want := `"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"`
	if got := quoteETag("abc"); got != want {
		t.Fatalf("quoteETag = %q, want %q", got, want)
	}
}

func TestFinishArticleRetainedContentStillTooLarge(t *testing.T) {
	s := &Service{}
	prev := &store.Article{Content: []byte(strings.Repeat("x", maxArticleBytes))}
	item, article := s.finishArticle(store.IndexItem{ID: "1", Title: "Big"}, strings.Repeat("y", maxArticleBytes), prev)
	if article != nil || item.ContentStatus != store.StatusError || item.ContentError == nil || *item.ContentError != "too_large" {
		t.Fatalf("item = %+v, article = %+v", item, article)
	}
}

func TestStartRefresher(t *testing.T) {
	st := store.New()
	sc := &fakeStoryClient{}
	cfg := testConfig()
	cfg.RefreshInterval = 20 * time.Millisecond
	svc := &Service{cfg: cfg, hn: sc, fetcher: &fakeFetcher{}, store: st}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.StartRefresher(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for sc.callCount() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if sc.callCount() < 2 {
		t.Fatalf("refresher calls = %d, want at least 2 (warm-up plus tick)", sc.callCount())
	}
	if _, ok := st.Current(); !ok {
		t.Fatal("expected ready snapshot after warm-up")
	}
}

func TestServiceConcurrentRefresh(t *testing.T) {
	st := store.New()
	sc := &fakeStoryClient{ids: []int{1, 2, 3, 4}}
	svc := &Service{cfg: testConfig(), hn: sc, fetcher: &fakeFetcher{}, store: st}
	var wg sync.WaitGroup
	var failures atomic.Int32
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := svc.Refresh(context.Background()); err != nil {
				failures.Add(1)
			}
		}()
	}
	wg.Wait()
	if failures.Load() != 0 {
		t.Fatalf("%d concurrent refreshes failed", failures.Load())
	}
	snap, ok := st.Current()
	if !ok || len(snap.Index.Items) != 4 {
		t.Fatalf("snapshot = %+v, %v", snap, ok)
	}
}
