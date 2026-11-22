package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"hnmd/internal/todo"
)

func newTodoTestHandler(t *testing.T, writesAllowed bool) *TodoHandler {
	t.Helper()
	st, err := todo.NewStore(filepath.Join(t.TempDir(), "todos.json"), 100)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return NewTodoHandler(st, "https://hnmd.example.com", writesAllowed)
}

func todoTestMux(h *TodoHandler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/v1/todos", http.HandlerFunc(h.Index))
	mux.Handle("/v1/todos.md", http.HandlerFunc(h.Markdown))
	mux.Handle("/v1/todos/tags", http.HandlerFunc(h.Tags))
	mux.Handle("/v1/todos/tags/{tag}", http.HandlerFunc(h.TagsMarkdown))
	mux.Handle("/v1/todos/top", http.HandlerFunc(h.Top))
	mux.Handle("/v1/todos/tasks", http.HandlerFunc(h.Tasks))
	mux.Handle("/v1/todos/tasks/{id}", http.HandlerFunc(h.Task))
	return mux
}

func seedTodoTasks(t *testing.T, h *TodoHandler) []todo.Task {
	t.Helper()
	doneAt := "2026-11-22T14:00:00Z"
	tasks := []todo.Task{
		{ID: "a1", Title: "Comprar leche", Tags: []string{"casa", "urgente"}, CreatedAt: "2026-11-22T10:00:00Z", UpdatedAt: "2026-11-22T10:00:00Z", Revision: 1},
		{ID: "b2", Title: "Enviar informe", Tags: []string{"trabajo"}, CreatedAt: "2026-11-22T11:00:00Z", UpdatedAt: "2026-11-22T11:00:00Z", Revision: 1},
		{ID: "c3", Title: "Llamar al médico", Tags: []string{}, CreatedAt: "2026-11-22T12:00:00Z", UpdatedAt: "2026-11-22T12:00:00Z", Revision: 1},
		{ID: "d4", Title: "Revisar correo", Tags: []string{"trabajo"}, Done: true, DoneAt: &doneAt, CreatedAt: "2026-11-22T09:00:00Z", UpdatedAt: "2026-11-22T14:00:00Z", Revision: 2},
	}
	for _, task := range tasks {
		if _, err := h.store.Create(task); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	return tasks
}

func TestTodoIndex(t *testing.T) {
	h := newTodoTestHandler(t, true)
	seedTodoTasks(t, h)

	req := httptest.NewRequest(http.MethodGet, "/v1/todos?status=all", nil)
	rr := httptest.NewRecorder()
	h.Index(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := rr.Header().Get("Content-Length"); got != strconv.Itoa(rr.Body.Len()) {
		t.Fatalf("Content-Length = %q, body = %d", got, rr.Body.Len())
	}
	if rr.Header().Get("ETag") == "" {
		t.Fatal("missing ETag")
	}

	var resp struct {
		SchemaVersion int    `json:"schema_version"`
		GeneratedAt   string `json:"generated_at"`
		Filter        struct {
			Tag    *string `json:"tag"`
			Status string  `json:"status"`
		} `json:"filter"`
		Counts        todo.Counts     `json:"counts"`
		Tags          []todo.TagCount `json:"tags"`
		ReturnedCount int             `json:"returned_count"`
		Truncated     bool            `json:"truncated"`
		Items         []todo.Task     `json:"items"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.SchemaVersion != 1 || resp.GeneratedAt == "" {
		t.Fatalf("unexpected envelope: %+v", resp)
	}
	if resp.Filter.Tag != nil || resp.Filter.Status != "all" {
		t.Fatalf("filter = %+v", resp.Filter)
	}
	if resp.Counts.Open != 3 || resp.Counts.Done != 1 || resp.Counts.Total != 4 {
		t.Fatalf("counts = %+v", resp.Counts)
	}
	if resp.ReturnedCount != 4 || len(resp.Items) != 4 || resp.Truncated {
		t.Fatalf("items = %d, returned = %d, truncated = %v", len(resp.Items), resp.ReturnedCount, resp.Truncated)
	}
	if len(resp.Tags) != 3 || resp.Tags[0].Tag != "casa" || resp.Tags[1].Tag != "trabajo" || resp.Tags[2].Tag != "urgente" {
		t.Fatalf("tags = %+v", resp.Tags)
	}
}

func TestTodoIndexFiltered(t *testing.T) {
	h := newTodoTestHandler(t, true)
	seedTodoTasks(t, h)

	req := httptest.NewRequest(http.MethodGet, "/v1/todos?tag=TRABAJO&status=done", nil)
	rr := httptest.NewRecorder()
	h.Index(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var resp struct {
		Filter struct {
			Tag    *string `json:"tag"`
			Status string  `json:"status"`
		} `json:"filter"`
		ReturnedCount int         `json:"returned_count"`
		Items         []todo.Task `json:"items"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Filter.Tag == nil || *resp.Filter.Tag != "trabajo" || resp.Filter.Status != "done" {
		t.Fatalf("filter = %+v", resp.Filter)
	}
	if resp.ReturnedCount != 1 || len(resp.Items) != 1 || resp.Items[0].ID != "d4" {
		t.Fatalf("items = %+v", resp.Items)
	}
}

func TestTodoMarkdownGetAndHead(t *testing.T) {
	h := newTodoTestHandler(t, true)
	seedTodoTasks(t, h)

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		req := httptest.NewRequest(method, "/v1/todos.md?status=all", nil)
		rr := httptest.NewRecorder()
		h.Markdown(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s status = %d", method, rr.Code)
		}
		if got := rr.Header().Get("Content-Type"); got != "text/markdown; charset=utf-8" {
			t.Fatalf("%s Content-Type = %q", method, got)
		}
		contentLength := rr.Header().Get("Content-Length")
		if contentLength == "" || contentLength == "0" {
			t.Fatalf("%s Content-Length = %q", method, contentLength)
		}
		if method == http.MethodGet && contentLength != strconv.Itoa(rr.Body.Len()) {
			t.Fatalf("GET Content-Length = %q, body = %d", contentLength, rr.Body.Len())
		}
		if got := rr.Header().Get("Content-Disposition"); got != `inline; filename="todos.md"` {
			t.Fatalf("%s Content-Disposition = %q", method, got)
		}
		if rr.Header().Get("ETag") == "" || rr.Header().Get("Last-Modified") == "" {
			t.Fatalf("%s missing ETag/Last-Modified", method)
		}
		if method == http.MethodHead {
			if rr.Body.Len() != 0 {
				t.Fatalf("HEAD body = %d, want 0", rr.Body.Len())
			}
			continue
		}
		body := rr.Body.String()
		for _, want := range []string{"# Todos", "## #casa", "## #trabajo", "## Sin etiqueta", "## Hechas", "- [x] Revisar correo"} {
			if !strings.Contains(body, want) {
				t.Fatalf("missing %q in markdown:\n%s", want, body)
			}
		}
	}
}

func TestTodoTagsMarkdownAndTags(t *testing.T) {
	h := newTodoTestHandler(t, true)
	seedTodoTasks(t, h)
	mux := todoTestMux(h)

	req := httptest.NewRequest(http.MethodGet, "/v1/todos/tags/trabajo.md?status=all", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("markdown status = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.HasPrefix(body, "# Todos — #trabajo\n") {
		t.Fatalf("unexpected heading: %s", body)
	}
	if strings.Contains(body, "## #trabajo") {
		t.Fatalf("tag view should be flat: %s", body)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/todos/tags", nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("tags status = %d", rr.Code)
	}
	var resp struct {
		Counts   todo.Counts     `json:"counts"`
		Tags     []todo.TagCount `json:"tags"`
		Untagged todo.Counts     `json:"untagged"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Counts.Total != 4 || resp.Untagged.Total != 1 {
		t.Fatalf("counts = %+v untagged = %+v", resp.Counts, resp.Untagged)
	}
}

func TestTodoTop(t *testing.T) {
	h := newTodoTestHandler(t, true)
	seedTodoTasks(t, h)

	req := httptest.NewRequest(http.MethodGet, "/v1/todos/top?tags=true", nil)
	req.Host = "hnmd.example.com"
	rr := httptest.NewRecorder()
	h.Top(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}

	var resp struct {
		SchemaVersion int `json:"schema_version"`
		ReturnedCount int `json:"returned_count"`
		Items         []struct {
			ID                string  `json:"id"`
			Type              string  `json:"type"`
			Title             string  `json:"title"`
			HNURL             string  `json:"hn_url"`
			ContentURL        *string `json:"content_url"`
			SuggestedFilename *string `json:"suggested_filename"`
			ContentLength     *int    `json:"content_length"`
		} `json:"items"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.SchemaVersion != 1 || resp.ReturnedCount != 4 || len(resp.Items) != 4 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	first := resp.Items[0]
	if first.ID != "todos" || first.Type != "todo" || first.HNURL != "https://hnmd.example.com/v1/todos.md" {
		t.Fatalf("first item = %+v", first)
	}
	if first.ContentURL == nil || *first.ContentURL != "/v1/todos.md" {
		t.Fatalf("content_url = %v", first.ContentURL)
	}
	if first.ContentLength == nil || *first.ContentLength <= 0 {
		t.Fatalf("content_length = %v", first.ContentLength)
	}
	if resp.Items[1].ID != "todos-tag-casa" || resp.Items[2].ID != "todos-tag-trabajo" || resp.Items[3].ID != "todos-tag-urgente" {
		t.Fatalf("tag items = %+v", resp.Items[1:])
	}
}

func TestTodoCRUD(t *testing.T) {
	h := newTodoTestHandler(t, true)
	mux := todoTestMux(h)

	body := `{"text":"Comprar pan #casa"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/todos/tasks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Location") == "" || !strings.HasPrefix(rr.Header().Get("Location"), "/v1/todos/tasks/") {
		t.Fatalf("Location = %q", rr.Header().Get("Location"))
	}
	var created todo.Task
	if err := json.NewDecoder(rr.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Title != "Comprar pan" || len(created.Tags) != 1 || created.Tags[0] != "casa" || created.Revision != 1 {
		t.Fatalf("created = %+v", created)
	}
	id := created.ID

	patch := `{"done":true}`
	req = httptest.NewRequest(http.MethodPatch, "/v1/todos/tasks/"+id, strings.NewReader(patch))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("patch status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var updated todo.Task
	if err := json.NewDecoder(rr.Body).Decode(&updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !updated.Done || updated.DoneAt == nil || updated.Revision != 2 {
		t.Fatalf("updated = %+v", updated)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/todos/tasks/"+id, nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("get status = %d", rr.Code)
	}

	req = httptest.NewRequest(http.MethodDelete, "/v1/todos/tasks/"+id, nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", rr.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/todos/tasks/"+id, nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("get after delete status = %d", rr.Code)
	}
}

func TestTodoDetailsRoundTrip(t *testing.T) {
	h := newTodoTestHandler(t, true)
	mux := todoTestMux(h)

	create := httptest.NewRequest(http.MethodPost, "/v1/todos/tasks",
		strings.NewReader(`{"text":"Comprar pan #casa","details":"primera\r\nsegunda"}`))
	create.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, create)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var created todo.Task
	if err := json.NewDecoder(rr.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Details != "primera\nsegunda" {
		t.Fatalf("create details = %q", created.Details)
	}
	id := created.ID

	patch := httptest.NewRequest(http.MethodPatch, "/v1/todos/tasks/"+id,
		strings.NewReader(`{"details":"nueva nota"}`))
	patch.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, patch)
	if rr.Code != http.StatusOK {
		t.Fatalf("patch status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var updated todo.Task
	if err := json.NewDecoder(rr.Body).Decode(&updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if updated.Details != "nueva nota" || updated.Title != created.Title || updated.Revision <= created.Revision {
		t.Fatalf("updated = %+v", updated)
	}

	tooLong := `{"details":"` + strings.Repeat("x", todo.MaxDetailsBytes+1) + `"}`
	patch = httptest.NewRequest(http.MethodPatch, "/v1/todos/tasks/"+id, strings.NewReader(tooLong))
	patch.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, patch)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"details"`) {
		t.Fatalf("too long details: status = %d, body = %s", rr.Code, rr.Body.String())
	}

	md := httptest.NewRequest(http.MethodGet, "/v1/todos.md?status=all", nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, md)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "  nueva nota\n") {
		t.Fatalf("markdown status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestTodoWritesForbidden(t *testing.T) {
	h := newTodoTestHandler(t, false)
	mux := todoTestMux(h)

	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/v1/todos/tasks", strings.NewReader(`{"text":"x"}`)),
		httptest.NewRequest(http.MethodPatch, "/v1/todos/tasks/a1", strings.NewReader(`{"done":true}`)),
		httptest.NewRequest(http.MethodDelete, "/v1/todos/tasks/a1", nil),
	} {
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("%s status = %d, want 403", req.Method, rr.Code)
		}
		var body todoErrorBody
		if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body.Error != "forbidden" {
			t.Fatalf("error = %q", body.Error)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/todos", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("read status = %d", rr.Code)
	}
}

func TestTodoInvalidRequests(t *testing.T) {
	h := newTodoTestHandler(t, true)
	mux := todoTestMux(h)

	tests := []struct {
		name        string
		method      string
		path        string
		body        string
		contentType string
		wantStatus  int
		wantField   string
	}{
		{name: "missing text", method: http.MethodPost, path: "/v1/todos/tasks", body: `{}`, contentType: "application/json", wantStatus: http.StatusBadRequest, wantField: "text"},
		{name: "unknown field", method: http.MethodPost, path: "/v1/todos/tasks", body: `{"text":"x","nope":1}`, contentType: "application/json", wantStatus: http.StatusBadRequest, wantField: "body"},
		{name: "invalid tag", method: http.MethodPost, path: "/v1/todos/tasks", body: `{"text":"x","tags":["bad tag"]}`, contentType: "application/json", wantStatus: http.StatusBadRequest, wantField: "tags"},
		{name: "wrong content type", method: http.MethodPost, path: "/v1/todos/tasks", body: `{"text":"x"}`, contentType: "text/plain", wantStatus: http.StatusBadRequest, wantField: "content_type"},
		{name: "empty patch", method: http.MethodPatch, path: "/v1/todos/tasks/a1", body: `{}`, contentType: "application/json", wantStatus: http.StatusBadRequest, wantField: "body"},
		{name: "body too large", method: http.MethodPost, path: "/v1/todos/tasks", body: `{"text":"` + strings.Repeat("x", 70000) + `"}`, contentType: "application/json", wantStatus: http.StatusRequestEntityTooLarge, wantField: "body"},
		{name: "invalid query tag", method: http.MethodGet, path: "/v1/todos?tag=bad%20tag", wantStatus: http.StatusBadRequest, wantField: "tag"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", tt.contentType)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)
			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", rr.Code, tt.wantStatus, rr.Body.String())
			}
			var body todoErrorBody
			if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Field == nil || *body.Field != tt.wantField {
				t.Fatalf("field = %v, want %q", body.Field, tt.wantField)
			}
		})
	}
}

func TestTodoMethodNotAllowed(t *testing.T) {
	h := newTodoTestHandler(t, true)
	req := httptest.NewRequest(http.MethodPut, "/v1/todos", nil)
	rr := httptest.NewRecorder()
	h.Index(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", rr.Code)
	}
	if rr.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("Allow = %q", rr.Header().Get("Allow"))
	}
	var body todoErrorBody
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error != "method_not_allowed" {
		t.Fatalf("error = %q", body.Error)
	}
}

func TestTodoNotFound(t *testing.T) {
	h := newTodoTestHandler(t, true)
	req := httptest.NewRequest(http.MethodGet, "/v1/todos/tasks/unknown", nil)
	req.SetPathValue("id", "unknown")
	rr := httptest.NewRecorder()
	h.GetTask(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rr.Code)
	}
}
