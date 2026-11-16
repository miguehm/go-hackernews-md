package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"hnmd/internal/store"
)

func strPtr(v string) *string { return &v }
func intPtr(v int) *int       { return &v }

func readyStore(items []store.IndexItem, articles map[string]*store.Article) *store.Store {
	st := store.New()
	now := time.Now().UTC()
	next := now.Add(30 * time.Minute).Format(time.RFC3339)
	interval := 1800
	st.Set(&store.Snapshot{
		Ready: true,
		Index: store.IndexResponse{
			SchemaVersion:          1,
			GeneratedAt:            now.Format(time.RFC3339),
			NextRefreshAt:          &next,
			RefreshIntervalSeconds: &interval,
			ReturnedCount:          len(items),
			Items:                  items,
		},
		Articles: articles,
	})
	return st
}

func TestIndexLimitTruncation(t *testing.T) {
	items := []store.IndexItem{
		{ID: "1", Rank: 1, Title: "one", HNURL: "https://news.ycombinator.com/item?id=1", ContentStatus: store.StatusOK},
		{ID: "2", Rank: 2, Title: "two", HNURL: "https://news.ycombinator.com/item?id=2", ContentStatus: store.StatusOK},
	}
	h := New(readyStore(items, map[string]*store.Article{}), 1800)

	req := httptest.NewRequest(http.MethodGet, "/v1/top?limit=1", nil)
	rr := httptest.NewRecorder()
	h.Index(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := rr.Header().Get("Content-Length"); got != strconv.Itoa(rr.Body.Len()) {
		t.Fatalf("Content-Length = %q, body = %d", got, rr.Body.Len())
	}
	if rr.Header().Get("ETag") == "" {
		t.Fatal("missing ETag")
	}

	var resp store.IndexResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp.ReturnedCount != 1 || !resp.Truncated || len(resp.Items) != 1 || resp.Items[0].ID != "1" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestIndexSizeCap(t *testing.T) {
	longTitle := strings.Repeat("x", 5000)
	items := make([]store.IndexItem, 30)
	for i := range items {
		id := strconv.Itoa(i + 1)
		items[i] = store.IndexItem{
			ID:            id,
			Rank:          i + 1,
			Title:         longTitle,
			HNURL:         "https://news.ycombinator.com/item?id=" + id,
			ContentStatus: store.StatusOK,
		}
	}
	h := New(readyStore(items, map[string]*store.Article{}), 1800)

	req := httptest.NewRequest(http.MethodGet, "/v1/top?limit=30", nil)
	rr := httptest.NewRecorder()
	h.Index(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if rr.Body.Len() > indexMaxBytes {
		t.Fatalf("body = %d bytes, want <= %d", rr.Body.Len(), indexMaxBytes)
	}
	var resp store.IndexResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !resp.Truncated {
		t.Fatal("expected truncated=true")
	}
	if resp.ReturnedCount >= len(items) {
		t.Fatalf("returned_count = %d, want < %d", resp.ReturnedCount, len(items))
	}
}

func TestParseLimit(t *testing.T) {
	tests := []struct {
		query string
		want  int
	}{
		{"", 20},
		{"?limit=", 20},
		{"?limit=abc", 20},
		{"?limit=0", 1},
		{"?limit=-3", 1},
		{"?limit=5", 5},
		{"?limit=30", 30},
		{"?limit=31", 30},
		{"?limit=5&limit=6", 20},
	}
	for _, tt := range tests {
		req := httptest.NewRequest(http.MethodGet, "/v1/top"+tt.query, nil)
		if got := parseLimit(req); got != tt.want {
			t.Fatalf("parseLimit(%q) = %d, want %d", tt.query, got, tt.want)
		}
	}
}

func TestArticleGetAndHead(t *testing.T) {
	markdownBody := []byte("# Title\n\n> alice · 1 points · 2 comments · 2026-11-16\n\nBody\n")
	article := &store.Article{
		Markdown:          markdownBody,
		Content:           []byte("Body"),
		ContentType:       "text/markdown; charset=utf-8",
		SuggestedFilename: "1-title.md",
		ContentLength:     len(markdownBody),
		ETag:              `"etag"`,
		LastModified:      time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	}
	items := []store.IndexItem{{
		ID:                "1",
		Rank:              1,
		Title:             "Title",
		HNURL:             "https://news.ycombinator.com/item?id=1",
		ContentStatus:     store.StatusOK,
		ContentURL:        strPtr("/v1/articles/1.md"),
		ContentType:       strPtr("text/markdown; charset=utf-8"),
		SuggestedFilename: strPtr("1-title.md"),
		ContentLength:     intPtr(len(markdownBody)),
	}}
	h := New(readyStore(items, map[string]*store.Article{"1": article}), 1800)

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		req := httptest.NewRequest(method, "/v1/articles/1.md", nil)
		req.SetPathValue("itemId", "1.md")
		rr := httptest.NewRecorder()
		h.Article(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", method, rr.Code)
		}
		if got := rr.Header().Get("Content-Type"); got != "text/markdown; charset=utf-8" {
			t.Fatalf("%s Content-Type = %q", method, got)
		}
		if got := rr.Header().Get("Content-Length"); got != strconv.Itoa(len(markdownBody)) {
			t.Fatalf("%s Content-Length = %q", method, got)
		}
		if got := rr.Header().Get("Content-Disposition"); got != `inline; filename="1-title.md"` {
			t.Fatalf("%s Content-Disposition = %q", method, got)
		}
		if got := rr.Header().Get("ETag"); got != `"etag"` {
			t.Fatalf("%s ETag = %q", method, got)
		}
		if got := rr.Header().Get("Last-Modified"); got == "" {
			t.Fatalf("%s missing Last-Modified", method)
		}
		if method == http.MethodHead {
			if rr.Body.Len() != 0 {
				t.Fatalf("HEAD body = %d bytes, want 0", rr.Body.Len())
			}
			continue
		}
		if rr.Body.String() != string(markdownBody) {
			t.Fatalf("body = %q", rr.Body.String())
		}
	}
}

func TestArticleNotFound(t *testing.T) {
	h := New(readyStore(nil, map[string]*store.Article{}), 1800)
	req := httptest.NewRequest(http.MethodGet, "/v1/articles/2.md", nil)
	req.SetPathValue("itemId", "2.md")
	rr := httptest.NewRecorder()
	h.Article(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
	var body errorBody
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Error != "not_found" || body.ItemID == nil || *body.ItemID != "2" {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func TestArticleInvalidID(t *testing.T) {
	h := New(readyStore(nil, map[string]*store.Article{}), 1800)
	req := httptest.NewRequest(http.MethodGet, "/v1/articles/bad%20id.md", nil)
	req.SetPathValue("itemId", "bad id.md")
	rr := httptest.NewRecorder()
	h.Article(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestServeMuxArticleRoute(t *testing.T) {
	markdownBody := []byte("# Title\n")
	article := &store.Article{
		Markdown:          markdownBody,
		ContentType:       "text/markdown; charset=utf-8",
		SuggestedFilename: "1-title.md",
		ContentLength:     len(markdownBody),
		ETag:              `"etag"`,
		LastModified:      time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	}
	h := New(readyStore(nil, map[string]*store.Article{"1": article}), 1800)

	mux := http.NewServeMux()
	mux.Handle("/v1/articles/{itemId}", http.HandlerFunc(h.Article))
	mux.Handle("/v1/top", http.HandlerFunc(h.Index))

	req := httptest.NewRequest(http.MethodGet, "/v1/articles/1.md", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("article status = %d, want 200", rr.Code)
	}
	if rr.Body.String() != string(markdownBody) {
		t.Fatalf("article body = %q", rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/top", nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("top status = %d, want 200", rr.Code)
	}
}

func TestArticleWarmingUp(t *testing.T) {
	h := New(store.New(), 1800)
	req := httptest.NewRequest(http.MethodGet, "/v1/articles/1.md", nil)
	req.SetPathValue("itemId", "1.md")
	rr := httptest.NewRecorder()
	h.Article(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	if got := rr.Header().Get("Retry-After"); got != "1800" {
		t.Fatalf("Retry-After = %q, want 1800", got)
	}
}
