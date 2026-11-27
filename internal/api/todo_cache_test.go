package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hnmd/internal/todo"
)

func todoCacheGet(mux *http.ServeMux, target, ifNoneMatch string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func checkIndexNotModified(t *testing.T, rr *httptest.ResponseRecorder, etag string) {
	t.Helper()
	if rr.Code != http.StatusNotModified {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if rr.Body.Len() != 0 {
		t.Fatalf("304 body = %q", rr.Body.String())
	}
	if got := rr.Header().Get("ETag"); got != etag {
		t.Fatalf("304 ETag = %q, want %q", got, etag)
	}
	if got := rr.Header().Get("Cache-Control"); got != cacheControlRevalidate {
		t.Fatalf("304 Cache-Control = %q", got)
	}
	if rr.Header().Get("Last-Modified") == "" {
		t.Fatal("304 missing Last-Modified")
	}
	if !strings.Contains(rr.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatalf("304 Vary = %q", rr.Header().Get("Vary"))
	}
	if rr.Header().Get("Content-Length") != "" {
		t.Fatalf("304 Content-Length = %q", rr.Header().Get("Content-Length"))
	}
}

func TestTodoIndexConditionalRequest(t *testing.T) {
	h := newTodoTestHandler(t, true)
	seedTodoTasks(t, h)
	mux := todoTestMux(h)
	target := "/v1/todos?status=all"

	first := todoCacheGet(mux, target, "")
	if first.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", first.Code, first.Body.String())
	}
	etag := first.Header().Get("ETag")
	if !strings.HasPrefix(etag, `W/"`) {
		t.Fatalf("ETag = %q, want weak", etag)
	}
	if got := first.Header().Get("Cache-Control"); got != cacheControlRevalidate {
		t.Fatalf("Cache-Control = %q", got)
	}
	lastModified := first.Header().Get("Last-Modified")
	if lastModified == "" {
		t.Fatal("200 missing Last-Modified")
	}

	var payload struct {
		GeneratedAt string `json:"generated_at"`
	}
	firstBody := first.Body.String()
	if err := json.Unmarshal([]byte(firstBody), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if want := h.indexGeneratedAt(h.indexModified()); payload.GeneratedAt != want {
		t.Fatalf("generated_at = %q, want %q", payload.GeneratedAt, want)
	}

	second := todoCacheGet(mux, target, "")
	if second.Header().Get("ETag") != etag {
		t.Fatalf("second ETag = %q, want %q", second.Header().Get("ETag"), etag)
	}
	if got := second.Body.String(); got != firstBody {
		t.Fatalf("body changed between identical reads: %q vs %q", got, firstBody)
	}

	checkIndexNotModified(t, todoCacheGet(mux, target, etag), etag)

	if _, err := h.store.Create(todo.Task{
		ID: "new1", Title: "New task", Tags: []string{}, CreatedAt: "2026-11-27T10:00:00Z",
		UpdatedAt: "2026-11-27T10:00:00Z", Revision: 1,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	afterWrite := todoCacheGet(mux, target, "")
	if got := afterWrite.Header().Get("ETag"); got == etag {
		t.Fatalf("ETag did not change after write: %q", got)
	}
	if got := afterWrite.Header().Get("Last-Modified"); got == "" {
		t.Fatal("Last-Modified missing after write")
	}
	if want := h.store.LastModified().UTC().Format(http.TimeFormat); afterWrite.Header().Get("Last-Modified") != want {
		t.Fatalf("Last-Modified = %q, want %q", afterWrite.Header().Get("Last-Modified"), want)
	}
}

func TestTodoTagsConditionalRequest(t *testing.T) {
	h := newTodoTestHandler(t, true)
	seedTodoTasks(t, h)
	mux := todoTestMux(h)
	target := "/v1/todos/tags?due_days=3"

	first := todoCacheGet(mux, target, "")
	if first.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", first.Code, first.Body.String())
	}
	etag := first.Header().Get("ETag")
	if !strings.HasPrefix(etag, `W/"`) {
		t.Fatalf("ETag = %q, want weak", etag)
	}
	if got := first.Header().Get("Cache-Control"); got != cacheControlRevalidate {
		t.Fatalf("Cache-Control = %q", got)
	}
	if first.Header().Get("Last-Modified") == "" {
		t.Fatal("200 missing Last-Modified")
	}
	if second := todoCacheGet(mux, target, ""); second.Header().Get("ETag") != etag {
		t.Fatalf("second ETag = %q, want %q", second.Header().Get("ETag"), etag)
	}
	checkIndexNotModified(t, todoCacheGet(mux, target, etag), etag)

	if _, err := h.store.Create(todo.Task{
		ID: "new1", Title: "New task", Tags: []string{"casa"}, CreatedAt: "2026-11-27T10:00:00Z",
		UpdatedAt: "2026-11-27T10:00:00Z", Revision: 1,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if after := todoCacheGet(mux, target, ""); after.Header().Get("ETag") == etag {
		t.Fatal("ETag did not change after write")
	}
}

func TestTodoIndexETagVariesByPagination(t *testing.T) {
	h := newTodoTestHandler(t, true)
	seedTodoTasks(t, h)
	mux := todoTestMux(h)

	first := todoCacheGet(mux, "/v1/todos?status=all&limit=1", "")
	second := todoCacheGet(mux, "/v1/todos?status=all&limit=2", "")
	if first.Header().Get("ETag") == second.Header().Get("ETag") {
		t.Fatal("different pages must have different ETags")
	}
	checkIndexNotModified(t, todoCacheGet(mux, "/v1/todos?status=all&limit=1", first.Header().Get("ETag")), first.Header().Get("ETag"))
}

func TestTodoMutableResponsesKeepNoStore(t *testing.T) {
	h := newTodoTestHandler(t, true)
	seedTodoTasks(t, h)
	mux := todoTestMux(h)
	for _, target := range []string{
		"/v1/todos/tasks/a1",
		"/v1/todos.md",
		"/v1/todos/tags/casa.md",
		"/v1/todos/top",
	} {
		rr := todoCacheGet(mux, target, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("%s status = %d, body = %s", target, rr.Code, rr.Body.String())
		}
		if got := rr.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("%s Cache-Control = %q", target, got)
		}
	}
}
