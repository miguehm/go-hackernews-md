package api

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"hnmd/internal/store"
	"hnmd/internal/todo"
)

func TestNewDefaultsRetryAfter(t *testing.T) {
	for _, seconds := range []int{0, -1} {
		h := New(store.New(), seconds)
		rr := httptest.NewRecorder()
		h.Index(rr, httptest.NewRequest(http.MethodGet, "/v1/top", nil))
		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", rr.Code)
		}
		if got := rr.Header().Get("Retry-After"); got != "15" {
			t.Fatalf("Retry-After = %q, want 15", got)
		}
		if !strings.Contains(rr.Body.String(), `"retry_after_seconds":15`) {
			t.Fatalf("body = %s", rr.Body.String())
		}
	}
}

func TestIndexRejectsNonGET(t *testing.T) {
	h := New(readyStore(nil, nil), 1800)
	rr := httptest.NewRecorder()
	h.Index(rr, httptest.NewRequest(http.MethodPost, "/v1/top", nil))
	if rr.Code != http.StatusMethodNotAllowed || rr.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("status = %d, Allow = %q", rr.Code, rr.Header().Get("Allow"))
	}
	if !strings.Contains(rr.Body.String(), `"message":"method not allowed"`) {
		t.Fatalf("body = %s", rr.Body.String())
	}
}

func TestIndexEmptiesItemsWhenStillOversized(t *testing.T) {
	st := store.New()
	st.Set(&store.Snapshot{
		Ready: true,
		Index: store.IndexResponse{
			SchemaVersion: 1,
			GeneratedAt:   strings.Repeat("x", indexMaxBytes+1),
			Items:         []store.IndexItem{{ID: "1", Rank: 1}},
		},
	})
	h := New(st, 1800)
	rr := httptest.NewRecorder()
	h.Index(rr, httptest.NewRequest(http.MethodGet, "/v1/top", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var resp store.IndexResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ReturnedCount != 0 || !resp.Truncated || len(resp.Items) != 0 {
		t.Fatalf("response = %+v", resp)
	}
}

func TestArticleMethodNotAllowedAndInvalidID(t *testing.T) {
	h := New(readyStore(nil, map[string]*store.Article{
		"1": {Markdown: []byte("# one"), ContentType: "text/markdown", SuggestedFilename: "1.md", ContentLength: 5},
	}), 1800)

	rr := httptest.NewRecorder()
	h.Article(rr, httptest.NewRequest(http.MethodPost, "/v1/articles/1.md", nil))
	if rr.Code != http.StatusMethodNotAllowed || rr.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("status = %d, Allow = %q", rr.Code, rr.Header().Get("Allow"))
	}

	for _, raw := range []string{"123", "", "bad!", strings.Repeat("a", 65) + ".md"} {
		t.Run("invalid_"+raw, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/articles/x.md", nil)
			req.SetPathValue("itemId", raw)
			rr := httptest.NewRecorder()
			h.Article(rr, req)
			if rr.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rr.Code)
			}
			if raw != "" && !strings.Contains(rr.Body.String(), `"item_id":"`+raw+`"`) {
				t.Fatalf("body = %s", rr.Body.String())
			}
		})
	}
}

func TestParseItemID(t *testing.T) {
	tests := []struct {
		raw string
		id  string
		ok  bool
	}{
		{raw: "123.md", id: "123", ok: true},
		{raw: "abc-def_1.md", id: "abc-def_1", ok: true},
		{raw: "123", ok: false},
		{raw: "bad!.md", ok: false},
		{raw: ".md", ok: false},
	}
	for _, tt := range tests {
		id, ok := parseItemID(tt.raw)
		if id != tt.id || ok != tt.ok {
			t.Fatalf("parseItemID(%q) = %q, %v; want %q, %v", tt.raw, id, ok, tt.id, tt.ok)
		}
	}
}

func TestSuccessHeadersIncludeNoCache(t *testing.T) {
	h := New(readyStore(nil, map[string]*store.Article{
		"1": {Markdown: []byte("# one"), ContentType: "text/markdown", SuggestedFilename: "1.md", ContentLength: 5},
	}), 1800)

	rr := httptest.NewRecorder()
	h.Index(rr, httptest.NewRequest(http.MethodGet, "/v1/top", nil))
	if got := rr.Header().Get("Cache-Control"); got != cacheControlRevalidate {
		t.Fatalf("Index Cache-Control = %q", got)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/articles/1.md", nil)
	req.SetPathValue("itemId", "1.md")
	rr = httptest.NewRecorder()
	h.Article(rr, req)
	if got := rr.Header().Get("Cache-Control"); got != cacheControlRevalidate {
		t.Fatalf("Article Cache-Control = %q", got)
	}
}

func TestAbsoluteURL(t *testing.T) {
	h := newTodoTestHandler(t, true)
	req := httptest.NewRequest(http.MethodGet, "http://device.local:8982/v1/todos/top", nil)
	if got := h.absoluteURL(req, "/v1/todos.md"); got != "https://hnmd.example.com/v1/todos.md" {
		t.Fatalf("configured base = %q", got)
	}

	h.publicBaseURL = ""
	if got := h.absoluteURL(req, "/v1/todos.md"); got != "http://device.local:8982/v1/todos.md" {
		t.Fatalf("http derived base = %q", got)
	}
	req.TLS = &tls.ConnectionState{}
	if got := h.absoluteURL(req, "/v1/todos.md"); got != "https://device.local:8982/v1/todos.md" {
		t.Fatalf("https derived base = %q", got)
	}
}

func TestNewTodoHandlerTrimsTrailingSlash(t *testing.T) {
	st, err := todo.NewStore(filepath.Join(t.TempDir(), "todos.json"), 10)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	h := NewTodoHandler(st, "https://hnmd.example.com///", false)
	req := httptest.NewRequest(http.MethodGet, "http://device.local/v1/todos/top", nil)
	if got := h.absoluteURL(req, "/x"); got != "https://hnmd.example.com/x" {
		t.Fatalf("absoluteURL = %q", got)
	}
}

func TestTodoRoutesRejectWrongMethods(t *testing.T) {
	h := newTodoTestHandler(t, true)
	mux := todoTestMux(h)

	tests := []struct {
		path, allow string
	}{
		{path: "/v1/todos/tasks", allow: http.MethodPost},
		{path: "/v1/todos/tags", allow: http.MethodGet},
		{path: "/v1/todos/top", allow: http.MethodGet},
		{path: "/v1/todos.md", allow: "GET, HEAD"},
		{path: "/v1/todos/tasks/a1", allow: "GET, PATCH, DELETE"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, tt.path, nil))
			if rr.Code != http.StatusMethodNotAllowed || rr.Header().Get("Allow") != tt.allow {
				t.Fatalf("status = %d, Allow = %q, want 405/%q", rr.Code, rr.Header().Get("Allow"), tt.allow)
			}
		})
	}
}

func TestTagsMarkdownRejectsPathWithoutSuffix(t *testing.T) {
	h := newTodoTestHandler(t, true)
	req := httptest.NewRequest(http.MethodGet, "/v1/todos/tags/casa.md", nil)
	req.SetPathValue("tag", "casa")
	rr := httptest.NewRecorder()
	h.TagsMarkdown(rr, req)
	if rr.Code != http.StatusNotFound || !strings.Contains(rr.Body.String(), `"task_id":"casa"`) {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestDecodeJSONTrailingContent(t *testing.T) {
	h := newTodoTestHandler(t, true)
	req := httptest.NewRequest(http.MethodPost, "/v1/todos/tasks", strings.NewReader(`{"text":"one"}{"text":"two"}`))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	rr := httptest.NewRecorder()
	h.CreateTask(rr, req)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "unexpected trailing JSON") {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestTodoInvalidTaskIDs(t *testing.T) {
	h := newTodoTestHandler(t, true)
	req := httptest.NewRequest(http.MethodGet, "/v1/todos/tasks/bad!.md", nil)
	req.SetPathValue("id", "bad!")
	rr := httptest.NewRecorder()
	h.GetTask(rr, req)
	if rr.Code != http.StatusNotFound || !strings.Contains(rr.Body.String(), `"task_id":"bad!"`) {
		t.Fatalf("GetTask status = %d, body = %s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, "/v1/todos/tasks/bad!", nil)
	req.SetPathValue("id", "bad!")
	rr = httptest.NewRecorder()
	h.DeleteTask(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("DeleteTask status = %d, want 204", rr.Code)
	}
}

func TestTodoCreateWithDoneAndTaskLimit(t *testing.T) {
	h := newTodoTestHandler(t, true)
	body := `{"text":"Tarea hecha #casa","done":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/todos/tasks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.CreateTask(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var created todo.Task
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !created.Done || created.DoneAt == nil {
		t.Fatalf("created task = %+v, want done with DoneAt", created)
	}
	if got := rr.Header().Get("Location"); !strings.HasPrefix(got, "/v1/todos/tasks/") {
		t.Fatalf("Location = %q", got)
	}

	st, err := todo.NewStore(filepath.Join(t.TempDir(), "todos.json"), 1)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	limited := NewTodoHandler(st, "", true)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/todos/tasks", strings.NewReader(`{"text":"tarea"}`))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		limited.CreateTask(rr, req)
		if i == 0 && rr.Code != http.StatusCreated {
			t.Fatalf("first create status = %d", rr.Code)
		}
		if i == 1 && (rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"field":"text"`)) {
			t.Fatalf("limit status = %d, body = %s", rr.Code, rr.Body.String())
		}
	}
}

func TestTodoUpdateTaskFields(t *testing.T) {
	h := newTodoTestHandler(t, true)
	seedTodoTasks(t, h)
	mux := todoTestMux(h)

	patch := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPatch, "/v1/todos/tasks/a1", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("If-Match", "*")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		return rr
	}

	rr := patch(`{"tags":["CASA","trabajo"]}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("tags patch status = %d, body = %s", rr.Code, rr.Body.String())
	}
	rr = patch(`{"text":"Nueva tarea #urgente"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("text patch status = %d, body = %s", rr.Code, rr.Body.String())
	}
	task, ok := h.store.Get("a1")
	if !ok || task.Title != "Nueva tarea" || strings.Join(task.Tags, ",") != "casa,trabajo,urgente" {
		t.Fatalf("task = %+v", task)
	}

	rr = patch(`{"done":true}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("done patch status = %d", rr.Code)
	}
	task, _ = h.store.Get("a1")
	if !task.Done || task.DoneAt == nil {
		t.Fatalf("done task = %+v", task)
	}
	rr = patch(`{"done":false}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("unmark patch status = %d", rr.Code)
	}
	task, _ = h.store.Get("a1")
	if task.Done || task.DoneAt != nil {
		t.Fatalf("unmarked task = %+v", task)
	}

	rr = patch(`{"text":""}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("empty text patch status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestTodoIndexEmptyAndSizeCap(t *testing.T) {
	h := newTodoTestHandler(t, true)
	rr := httptest.NewRecorder()
	h.Index(rr, httptest.NewRequest(http.MethodGet, "/v1/todos", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"items":[]`) {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	seedTodoTasks(t, h)
	h.maxIndexBytes = 200
	rr = httptest.NewRecorder()
	h.Index(rr, httptest.NewRequest(http.MethodGet, "/v1/todos?status=all", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"truncated":true`) {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestTodoRenderMarkdownTruncation(t *testing.T) {
	h := newTodoTestHandler(t, true)
	tasks := seedTodoTasks(t, h)

	h.maxMarkdown = 100
	markdown, truncated := h.renderMarkdown(tasks, todo.Filter{Status: "all"}, todo.ComputeCounts(tasks))
	if !truncated || !strings.Contains(markdown, "> Lista truncada.") {
		t.Fatalf("truncated = %v, markdown = %s", truncated, markdown)
	}

	h.maxMarkdown = 10
	markdown, truncated = h.renderMarkdown(nil, todo.Filter{}, todo.Counts{})
	if !truncated || !strings.Contains(markdown, "> Lista truncada.") {
		t.Fatalf("fallback truncated = %v, markdown = %s", truncated, markdown)
	}
}

func TestTodoTopTagFiltering(t *testing.T) {
	h := newTodoTestHandler(t, true)
	seedTodoTasks(t, h)

	rr := httptest.NewRecorder()
	h.Top(rr, httptest.NewRequest(http.MethodGet, "/v1/todos/top", nil))
	var resp store.IndexResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ReturnedCount != 1 || resp.Items[0].ID != "todos" {
		t.Fatalf("response = %+v", resp)
	}

	rr = httptest.NewRecorder()
	h.Top(rr, httptest.NewRequest(http.MethodGet, "/v1/todos/top?tags=true", nil))
	resp = store.IndexResponse{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ReturnedCount < 2 {
		t.Fatalf("response = %+v, want tag entries", resp)
	}
}

func TestServeMarkdownRejectsInvalidQueryTag(t *testing.T) {
	h := newTodoTestHandler(t, true)
	rr := httptest.NewRecorder()
	h.Markdown(rr, httptest.NewRequest(http.MethodGet, "/v1/todos.md?tag=BAD%20TAG", nil))
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"field":"tag"`) {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestTodoMarkdownEmptyStoreLastModified(t *testing.T) {
	h := newTodoTestHandler(t, true)
	rr := httptest.NewRecorder()
	h.Markdown(rr, httptest.NewRequest(http.MethodGet, "/v1/todos.md", nil))
	if rr.Code != http.StatusOK || rr.Header().Get("Last-Modified") == "" {
		t.Fatalf("status = %d, Last-Modified = %q", rr.Code, rr.Header().Get("Last-Modified"))
	}
}

func TestTodoTopSizeCap(t *testing.T) {
	h := newTodoTestHandler(t, true)
	seedTodoTasks(t, h)
	h.maxIndexBytes = 200
	rr := httptest.NewRecorder()
	h.Top(rr, httptest.NewRequest(http.MethodGet, "/v1/todos/top?tags=true", nil))
	var resp store.IndexResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Truncated || resp.ReturnedCount != len(resp.Items) {
		t.Fatalf("response = %+v", resp)
	}
}

func TestHTTPTimeFromRFC3339Invalid(t *testing.T) {
	if got := httpTimeFromRFC3339("not-a-date"); got == "" {
		t.Fatal("expected current time fallback")
	}
}

func TestWriteTodoValidationErrorNonValidation(t *testing.T) {
	rr := httptest.NewRecorder()
	writeTodoValidationError(rr, errors.New("boom"))
	if rr.Code != http.StatusInternalServerError || !strings.Contains(rr.Body.String(), `"error":"internal_error"`) {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
}
