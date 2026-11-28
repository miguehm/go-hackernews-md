package api

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	"hnmd/internal/store"
	"hnmd/internal/todo"
)

func TestIndexPaginationAndConditionalRequest(t *testing.T) {
	items := make([]store.IndexItem, 5)
	for i := range items {
		id := string(rune('1' + i))
		items[i] = store.IndexItem{ID: id, Rank: i + 1, Title: "item " + id, HNURL: "https://news.ycombinator.com/item?id=" + id, ContentStatus: store.StatusOK}
	}
	h := New(readyStore(items, map[string]*store.Article{}), 1800)

	req := httptest.NewRequest(http.MethodGet, "/v1/top?limit=2&offset=1", nil)
	rr := httptest.NewRecorder()
	h.Index(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var resp indexPageResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Offset != 1 || resp.Limit != 2 || resp.ReturnedCount != 2 || !resp.HasMore || resp.NextOffset == nil || *resp.NextOffset != 3 {
		t.Fatalf("pagination = %+v", resp)
	}
	if !resp.Truncated || len(resp.Items) != 2 || resp.Items[0].ID != "2" || resp.Items[1].ID != "3" {
		t.Fatalf("items = %+v, truncated = %v", resp.Items, resp.Truncated)
	}
	etag := rr.Header().Get("ETag")
	if !strings.HasPrefix(etag, `W/"`) {
		t.Fatalf("ETag = %q, want weak", etag)
	}
	if got := rr.Header().Get("Cache-Control"); got != cacheControlRevalidate {
		t.Fatalf("Cache-Control = %q", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/top?limit=2&offset=1", nil)
	req.Header.Set("If-None-Match", etag)
	rr = httptest.NewRecorder()
	h.Index(rr, req)
	if rr.Code != http.StatusNotModified {
		t.Fatalf("conditional status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if rr.Body.Len() != 0 {
		t.Fatalf("304 body = %q", rr.Body.String())
	}
	if got := rr.Header().Get("ETag"); got != etag {
		t.Fatalf("304 ETag = %q, want %q", got, etag)
	}
	if rr.Header().Get("Last-Modified") == "" {
		t.Fatal("304 missing Last-Modified")
	}
	if got := rr.Header().Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
		t.Fatalf("Vary = %q", got)
	}
}

func TestArticleConditionalRequest(t *testing.T) {
	markdown := []byte("# Title\n\nBody\n")
	modified := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	article := &store.Article{
		Markdown:          markdown,
		ContentType:       "text/markdown; charset=utf-8",
		SuggestedFilename: "1-title.md",
		ContentLength:     len(markdown),
		ETag:              `"etag"`,
		LastModified:      modified,
		UpdatedAt:         modified,
	}
	h := New(readyStore(nil, map[string]*store.Article{"1": article}), 1800)

	for _, header := range []string{`W/"etag"`, `"etag"`} {
		req := httptest.NewRequest(http.MethodGet, "/v1/articles/1.md", nil)
		req.SetPathValue("itemId", "1.md")
		req.Header.Set("If-None-Match", header)
		rr := httptest.NewRecorder()
		h.Article(rr, req)
		if rr.Code != http.StatusNotModified {
			t.Fatalf("If-None-Match %q status = %d, body = %s", header, rr.Code, rr.Body.String())
		}
		if rr.Body.Len() != 0 {
			t.Fatalf("304 body = %q", rr.Body.String())
		}
		if got := rr.Header().Get("ETag"); got != `W/"etag"` {
			t.Fatalf("ETag = %q", got)
		}
		if got := rr.Header().Get("Cache-Control"); got != cacheControlRevalidate {
			t.Fatalf("Cache-Control = %q", got)
		}
	}
}

func TestGzipMiddleware(t *testing.T) {
	body := `{"message":"` + strings.Repeat("x", 1024) + `"}`
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("ETag", `"etag"`)
		_, _ = w.Write([]byte(body))
	})
	wrapped := GzipMiddleware(handler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if got := rr.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q", got)
	}
	if got := rr.Header().Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
		t.Fatalf("Vary = %q", got)
	}
	if got := rr.Header().Get("ETag"); got != `W/"etag"` {
		t.Fatalf("ETag = %q", got)
	}
	reader, err := gzip.NewReader(rr.Body)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if string(decoded) != body {
		t.Fatalf("decoded = %q", decoded)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip;q=0")
	rr = httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)
	if got := rr.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("q=0 Content-Encoding = %q", got)
	}
	if rr.Body.String() != body {
		t.Fatalf("q=0 body = %q", rr.Body.String())
	}
}

func TestStatusHandler(t *testing.T) {
	todoStore, err := todo.NewStore(filepath.Join(t.TempDir(), "todos.json"), 10)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := todoStore.Create(todo.Task{ID: "1", Title: "one", Tags: []string{}, CreatedAt: "2026-11-22T10:00:00Z", UpdatedAt: "2026-11-22T10:00:00Z", Revision: 1}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	h := NewStatusHandler(store.New(), todoStore, true, "1.5.0")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/status", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Version     string `json:"version"`
		AuthEnabled bool   `json:"auth_enabled"`
		Ready       bool   `json:"ready"`
		TodoCount   int    `json:"todo_count"`
		ServerTime  string `json:"server_time"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Version != "1.5.0" || !resp.AuthEnabled || resp.Ready || resp.TodoCount != 1 || resp.ServerTime == "" {
		t.Fatalf("status = %+v", resp)
	}
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}

	ready := NewStatusHandler(readyStore(nil, nil), todoStore, false, "")
	rr = httptest.NewRecorder()
	ready.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/status", nil))
	var readyResp struct {
		Ready                  bool    `json:"ready"`
		SnapshotGeneratedAt    *string `json:"snapshot_generated_at"`
		NextRefreshAt          *string `json:"next_refresh_at"`
		RefreshIntervalSeconds *int    `json:"refresh_interval_seconds"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&readyResp); err != nil {
		t.Fatalf("decode ready: %v", err)
	}
	if !readyResp.Ready || readyResp.SnapshotGeneratedAt == nil || readyResp.NextRefreshAt == nil || readyResp.RefreshIntervalSeconds == nil {
		t.Fatalf("ready status = %+v", readyResp)
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/status", nil))
	if rr.Code != http.StatusMethodNotAllowed || !strings.Contains(rr.Body.String(), `"error":"method_not_allowed"`) {
		t.Fatalf("method status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestTodoPagination(t *testing.T) {
	h := newTodoTestHandler(t, true)
	seedTodoTasks(t, h)
	mux := todoTestMux(h)

	req := httptest.NewRequest(http.MethodGet, "/v1/todos?status=all&limit=2&offset=1", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Offset        int         `json:"offset"`
		Limit         int         `json:"limit"`
		ReturnedCount int         `json:"returned_count"`
		HasMore       bool        `json:"has_more"`
		NextOffset    *int        `json:"next_offset"`
		Truncated     bool        `json:"truncated"`
		Items         []todo.Task `json:"items"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Offset != 1 || resp.Limit != 2 || resp.ReturnedCount != 2 || !resp.HasMore || resp.NextOffset == nil || *resp.NextOffset != 3 {
		t.Fatalf("pagination = %+v", resp)
	}
	if !resp.Truncated || len(resp.Items) != 2 || resp.Items[0].ID != "b2" || resp.Items[1].ID != "c3" {
		t.Fatalf("items = %+v", resp.Items)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/todos/tags?limit=1", nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("tags status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var tagsResp struct {
		ReturnedCount int             `json:"returned_count"`
		HasMore       bool            `json:"has_more"`
		NextOffset    *int            `json:"next_offset"`
		Tags          []todo.TagCount `json:"tags"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&tagsResp); err != nil {
		t.Fatalf("decode tags: %v", err)
	}
	if tagsResp.ReturnedCount != 1 || !tagsResp.HasMore || tagsResp.NextOffset == nil || *tagsResp.NextOffset != 1 || len(tagsResp.Tags) != 1 || tagsResp.Tags[0].Tag != "casa" {
		t.Fatalf("tags pagination = %+v", tagsResp)
	}
}

func TestTodoCreateIdempotent(t *testing.T) {
	h := newTodoTestHandler(t, true)
	mux := todoTestMux(h)

	body := `{"id":"client1","text":"Comprar pan #casa"}`
	create := func(payload string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/todos/tasks", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		return rr
	}

	rr := create(body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("first create status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var first todo.Task
	if err := json.NewDecoder(rr.Body).Decode(&first); err != nil {
		t.Fatalf("decode first: %v", err)
	}
	if first.ID != "client1" || first.Revision != 1 {
		t.Fatalf("first = %+v", first)
	}

	rr = create(body)
	if rr.Code != http.StatusOK {
		t.Fatalf("second create status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var second todo.Task
	if err := json.NewDecoder(rr.Body).Decode(&second); err != nil {
		t.Fatalf("decode second: %v", err)
	}
	if second.ID != first.ID || second.Revision != first.Revision {
		t.Fatalf("second = %+v, first = %+v", second, first)
	}
	if got := h.store.Count(); got != 1 {
		t.Fatalf("task count = %d, want 1", got)
	}

	rr = create(`{"id":"bad id","text":"x"}`)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"field":"id"`) {
		t.Fatalf("invalid id status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestTodoIfMatch(t *testing.T) {
	h := newTodoTestHandler(t, true)
	seedTodoTasks(t, h)
	mux := todoTestMux(h)

	patch := func(body, ifMatch string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPatch, "/v1/todos/tasks/a1", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if ifMatch != "" {
			req.Header.Set("If-Match", ifMatch)
		}
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		return rr
	}

	rr := patch(`{"done":true}`, "")
	if rr.Code != http.StatusPreconditionRequired || !strings.Contains(rr.Body.String(), `"error":"precondition_required"`) {
		t.Fatalf("missing If-Match status = %d, body = %s", rr.Code, rr.Body.String())
	}

	rr = patch(`{"done":true}`, `"1"`)
	if rr.Code != http.StatusOK {
		t.Fatalf("first patch status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var updated todo.Task
	if err := json.NewDecoder(rr.Body).Decode(&updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if updated.Revision != 2 {
		t.Fatalf("revision = %d", updated.Revision)
	}

	rr = patch(`{"done":false}`, `"1"`)
	if rr.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale patch status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var conflict struct {
		Error           string `json:"error"`
		CurrentRevision int    `json:"current_revision"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&conflict); err != nil {
		t.Fatalf("decode conflict: %v", err)
	}
	if conflict.Error != "revision_conflict" || conflict.CurrentRevision != 2 {
		t.Fatalf("conflict = %+v", conflict)
	}
	if got := rr.Header().Get("ETag"); got != `"2"` {
		t.Fatalf("conflict ETag = %q", got)
	}

	rr = patch(`{"done":false}`, `W/"2"`)
	if rr.Code != http.StatusOK {
		t.Fatalf("second patch status = %d, body = %s", rr.Code, rr.Body.String())
	}

	req := httptest.NewRequest(http.MethodDelete, "/v1/todos/tasks/a1", nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusPreconditionRequired {
		t.Fatalf("delete without If-Match status = %d", rr.Code)
	}

	req = httptest.NewRequest(http.MethodDelete, "/v1/todos/tasks/a1", nil)
	req.Header.Set("If-Match", `"2"`)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale delete status = %d, body = %s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, "/v1/todos/tasks/a1", nil)
	req.Header.Set("If-Match", "*")
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("forced delete status = %d", rr.Code)
	}
}

func TestTodoTimezone(t *testing.T) {
	h := newTodoTestHandler(t, true)
	fixed := time.Date(2026, 11, 24, 1, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return fixed }
	due := "2026-11-23"
	if _, err := h.store.Create(todo.Task{ID: "due", Title: "due", Tags: []string{"casa"}, DueDate: &due, CreatedAt: "2026-11-22T10:00:00Z", UpdatedAt: "2026-11-22T10:00:00Z", Revision: 1}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	count := func(query string) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/v1/todos?"+query, nil)
		rr := httptest.NewRecorder()
		h.Index(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s status = %d, body = %s", query, rr.Code, rr.Body.String())
		}
		var resp struct {
			ReturnedCount int `json:"returned_count"`
		}
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return resp.ReturnedCount
	}

	if got := count("status=overdue&tz=UTC"); got != 1 {
		t.Fatalf("UTC overdue = %d, want 1", got)
	}
	if got := count("status=overdue&tz=Pacific%2FHonolulu"); got != 0 {
		t.Fatalf("Honolulu overdue = %d, want 0", got)
	}
	if got := count("status=due_soon&tz=Pacific%2FHonolulu"); got != 1 {
		t.Fatalf("Honolulu due_soon = %d, want 1", got)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/todos?tz=NotARealZone", nil)
	rr := httptest.NewRecorder()
	h.Index(rr, req)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"field":"tz"`) {
		t.Fatalf("invalid tz status = %d, body = %s", rr.Code, rr.Body.String())
	}
}
