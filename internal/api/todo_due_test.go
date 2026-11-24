package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hnmd/internal/todo"
)

func todoDueRequest(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if method == http.MethodPatch || method == http.MethodDelete {
		req.Header.Set("If-Match", "*")
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func TestTodoDueDateRoundTrip(t *testing.T) {
	h := newTodoTestHandler(t, true)
	mux := todoTestMux(h)

	rr := todoDueRequest(t, mux, http.MethodPost, "/v1/todos/tasks",
		`{"text":"Pagar impuestos #casa","due_date":"2026-11-30"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var created todo.Task
	if err := json.NewDecoder(rr.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.DueDate == nil || *created.DueDate != "2026-11-30" {
		t.Fatalf("created due_date = %v", created.DueDate)
	}
	id := created.ID

	rr = todoDueRequest(t, mux, http.MethodPatch, "/v1/todos/tasks/"+id, `{"due_date":"2026-12-15"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("patch status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var updated todo.Task
	if err := json.NewDecoder(rr.Body).Decode(&updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if updated.DueDate == nil || *updated.DueDate != "2026-12-15" || updated.Title != created.Title {
		t.Fatalf("updated = %+v", updated)
	}

	updated = todo.Task{}
	rr = todoDueRequest(t, mux, http.MethodPatch, "/v1/todos/tasks/"+id, `{"due_date":""}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("clear status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if err := json.NewDecoder(rr.Body).Decode(&updated); err != nil {
		t.Fatalf("decode clear: %v", err)
	}
	if updated.DueDate != nil {
		t.Fatalf("empty due_date must clear it, got %q", *updated.DueDate)
	}

	updated = todo.Task{}
	rr = todoDueRequest(t, mux, http.MethodPatch, "/v1/todos/tasks/"+id, `{"due_date":null}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("null status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if err := json.NewDecoder(rr.Body).Decode(&updated); err != nil {
		t.Fatalf("decode null: %v", err)
	}
	if updated.DueDate != nil {
		t.Fatalf("null due_date must clear it, got %q", *updated.DueDate)
	}

	for _, body := range []string{`{"due_date":"30-11-2026"}`, `{"text":"x","due_date":"manana"}`} {
		rr = todoDueRequest(t, mux, http.MethodPatch, "/v1/todos/tasks/"+id, body)
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"due_date"`) {
			t.Fatalf("invalid due_date %s: status = %d, body = %s", body, rr.Code, rr.Body.String())
		}
	}

	rr = todoDueRequest(t, mux, http.MethodPost, "/v1/todos/tasks", `{"text":"x","due_date":"2026-02-30"}`)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"due_date"`) {
		t.Fatalf("invalid create due_date: status = %d, body = %s", rr.Code, rr.Body.String())
	}

	rr = todoDueRequest(t, mux, http.MethodPatch, "/v1/todos/tasks/"+id, `{"due_date":"2026-11-30"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("restore due_date: %d", rr.Code)
	}
	rr = todoDueRequest(t, mux, http.MethodGet, "/v1/todos.md?status=all", "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "(vence 2026-11-30)") {
		t.Fatalf("markdown due_date: status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestTodoDueFiltersAndCounts(t *testing.T) {
	h := newTodoTestHandler(t, true)
	now := time.Now().UTC()
	date := func(offset int) *string {
		value := now.AddDate(0, 0, offset).Format(todo.DueDateLayout)
		return &value
	}
	tasks := []todo.Task{
		{ID: "overdue", Title: "Vencida", Tags: []string{"casa"}, DueDate: date(-1), CreatedAt: "2026-11-01T00:00:00Z", UpdatedAt: "2026-11-01T00:00:00Z", Revision: 1},
		{ID: "today", Title: "Hoy", Tags: []string{"casa"}, DueDate: date(0), CreatedAt: "2026-11-02T00:00:00Z", UpdatedAt: "2026-11-02T00:00:00Z", Revision: 1},
		{ID: "soon", Title: "Pronto", Tags: []string{"trabajo"}, DueDate: date(1), CreatedAt: "2026-11-03T00:00:00Z", UpdatedAt: "2026-11-03T00:00:00Z", Revision: 1},
		{ID: "later", Title: "Lejos", Tags: []string{"trabajo"}, DueDate: date(6), CreatedAt: "2026-11-04T00:00:00Z", UpdatedAt: "2026-11-04T00:00:00Z", Revision: 1},
		{ID: "none", Title: "Sin fecha", Tags: []string{}, CreatedAt: "2026-11-05T00:00:00Z", UpdatedAt: "2026-11-05T00:00:00Z", Revision: 1},
		{ID: "done-old", Title: "Hecha", Tags: []string{"casa"}, Done: true, DueDate: date(-5), CreatedAt: "2026-11-06T00:00:00Z", UpdatedAt: "2026-11-06T00:00:00Z", Revision: 1},
	}
	for _, task := range tasks {
		if _, err := h.store.Create(task); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	type indexResponse struct {
		Filter struct {
			Status  string `json:"status"`
			DueDays int    `json:"due_days"`
		} `json:"filter"`
		Counts todo.Counts     `json:"counts"`
		Tags   []todo.TagCount `json:"tags"`
		Items  []todo.Task     `json:"items"`
	}
	var resp indexResponse
	mux := todoTestMux(h)
	rr := todoDueRequest(t, mux, http.MethodGet, "/v1/todos?status=all&due_days=3", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("index status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Filter.DueDays != 3 {
		t.Fatalf("filter.due_days = %d, want 3", resp.Filter.DueDays)
	}
	if resp.Counts.Overdue != 1 || resp.Counts.DueSoon != 2 {
		t.Fatalf("counts = %+v, want overdue 1, due_soon 2", resp.Counts)
	}
	var casa todo.TagCount
	for _, tag := range resp.Tags {
		if tag.Tag == "casa" {
			casa = tag
		}
	}
	if casa.Overdue != 1 || casa.DueSoon != 1 {
		t.Fatalf("casa = %+v", casa)
	}

	rr = todoDueRequest(t, mux, http.MethodGet, "/v1/todos?status=overdue", "")
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode overdue: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].ID != "overdue" {
		t.Fatalf("overdue items = %+v", resp.Items)
	}

	rr = todoDueRequest(t, mux, http.MethodGet, "/v1/todos?status=due_soon&due_days=3", "")
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode due_soon: %v", err)
	}
	if len(resp.Items) != 2 || resp.Items[0].ID != "today" || resp.Items[1].ID != "soon" {
		t.Fatalf("due_soon items = %+v", resp.Items)
	}

	rr = todoDueRequest(t, mux, http.MethodGet, "/v1/todos?status=due_soon&due_days=7", "")
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode due_soon/7: %v", err)
	}
	if len(resp.Items) != 3 || resp.Items[2].ID != "later" {
		t.Fatalf("due_soon/7 items = %+v", resp.Items)
	}

	rr = todoDueRequest(t, mux, http.MethodGet, "/v1/todos.md?status=due_soon&due_days=3", "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Hoy") ||
		strings.Contains(rr.Body.String(), "Vencida") || strings.Contains(rr.Body.String(), "Lejos") {
		t.Fatalf("due_soon markdown: status = %d, body = %s", rr.Code, rr.Body.String())
	}

	rr = todoDueRequest(t, mux, http.MethodGet, "/v1/todos/tags?due_days=3", "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"due_soon":2`) ||
		!strings.Contains(rr.Body.String(), `"overdue":1`) {
		t.Fatalf("tags counts: status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestTodoInvalidDueDays(t *testing.T) {
	h := newTodoTestHandler(t, true)
	mux := todoTestMux(h)
	for _, path := range []string{
		"/v1/todos?due_days=0",
		"/v1/todos?due_days=31",
		"/v1/todos?due_days=abc",
		"/v1/todos.md?due_days=99",
	} {
		rr := todoDueRequest(t, mux, http.MethodGet, path, "")
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"due_days"`) {
			t.Fatalf("%s: status = %d, body = %s", path, rr.Code, rr.Body.String())
		}
	}
}
