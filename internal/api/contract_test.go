package api

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"

	"hnmd/internal/store"
	"hnmd/internal/todo"
)

const contractBase = "http://hnmd.test/v1"

func init() {
	openapi3filter.RegisterBodyDecoder("text/markdown", openapi3filter.FileBodyDecoder)
}

func loadContract(t *testing.T) routers.Router {
	t.Helper()
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(filepath.Join("..", "..", "openapi.yaml"))
	if err != nil {
		t.Fatalf("load openapi.yaml: %v", err)
	}
	if err := doc.Validate(loader.Context); err != nil {
		t.Fatalf("openapi.yaml is not a valid OpenAPI document: %v", err)
	}
	doc.Servers = openapi3.Servers{{URL: contractBase}}
	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		t.Fatalf("router: %v", err)
	}
	return router
}

func contractFixtures(t *testing.T) (*store.Store, *todo.Store) {
	t.Helper()
	str := func(v string) *string { return &v }
	num := func(v int) *int { return &v }
	now := time.Now().UTC().Format(time.RFC3339)
	item := store.IndexItem{
		ID: "42123456", Rank: 1, Type: "story", Title: "A title",
		HNURL: "https://news.ycombinator.com/item?id=42123456",
		URL:   str("https://example.com/post"), By: str("alice"), Score: num(120), Descendants: num(30),
		PublishedAt: str(now), Domain: str("example.com"), ContentStatus: store.StatusOK,
		ContentURL: str("/v1/articles/42123456.md"), ContentType: str("text/markdown; charset=utf-8"),
		SuggestedFilename: str("42123456-a-title.md"), ContentLength: num(18), UpdatedAt: str(now),
	}
	failed := store.IndexItem{
		ID: "42120001", Rank: 2, Title: "Broken", HNURL: "https://news.ycombinator.com/item?id=42120001",
		ContentStatus: store.StatusError, ContentError: str("fetch_timeout"), UpdatedAt: str(now),
	}
	md := []byte("# A title\n\nBody\n")
	hn := readyStore([]store.IndexItem{item, failed}, map[string]*store.Article{
		"42123456": {
			Markdown: md, ContentType: "text/markdown; charset=utf-8", SuggestedFilename: "42123456-a-title.md",
			ContentLength: len(md), ETag: `"abc"`, LastModified: time.Now(), UpdatedAt: time.Now(),
		},
	})
	todos, err := todo.NewStore(filepath.Join(t.TempDir(), "todos.json"), 100)
	if err != nil {
		t.Fatal(err)
	}
	return hn, todos
}

type contractCase struct {
	name    string
	method  string
	path    string
	body    string
	headers map[string]string
	status  int
}

func TestResponsesMatchOpenAPIContract(t *testing.T) {
	router := loadContract(t)
	hn, todos := contractFixtures(t)
	h := NewRouter(RouterDeps{
		HN: hn, Todos: todos, APIKeys: []string{"secret"}, Version: "contract",
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	warming := NewRouter(RouterDeps{
		HN: store.New(), Todos: todos, APIKeys: []string{"secret"},
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})

	auth := map[string]string{"Authorization": "Bearer secret"}
	jsonAuth := map[string]string{"Authorization": "Bearer secret", "Content-Type": "application/json"}
	with := func(extra map[string]string) map[string]string {
		out := map[string]string{"Authorization": "Bearer secret", "Content-Type": "application/json"}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}

	cases := []contractCase{
		{"status", http.MethodGet, "/status", "", auth, 200},
		{"top", http.MethodGet, "/top?limit=1", "", auth, 200},
		{"top unauthorized", http.MethodGet, "/top", "", nil, 401},
		{"article", http.MethodGet, "/articles/42123456.md", "", auth, 200},
		{"article not found", http.MethodGet, "/articles/999.md", "", auth, 404},
		{"create task", http.MethodPost, "/todos/tasks", `{"id":"t1","text":"Comprar pan #casa","due_date":"2026-12-01"}`, jsonAuth, 201},
		{"create task retry", http.MethodPost, "/todos/tasks", `{"id":"t1","text":"Comprar pan #casa"}`, jsonAuth, 200},
		{"create task invalid", http.MethodPost, "/todos/tasks", `{"text":""}`, jsonAuth, 400},
		{"get task", http.MethodGet, "/todos/tasks/t1", "", auth, 200},
		{"patch without If-Match", http.MethodPatch, "/todos/tasks/t1", `{"done":true}`, jsonAuth, 428},
		{"patch stale revision", http.MethodPatch, "/todos/tasks/t1", `{"done":true}`, with(map[string]string{"If-Match": `"9"`}), 412},
		{"patch", http.MethodPatch, "/todos/tasks/t1", `{"done":true}`, with(map[string]string{"If-Match": `"1"`}), 200},
		{"todos", http.MethodGet, "/todos?status=all", "", auth, 200},
		{"todos markdown", http.MethodGet, "/todos.md", "", auth, 200},
		{"tags", http.MethodGet, "/todos/tags", "", auth, 200},
		{"tag markdown", http.MethodGet, "/todos/tags/casa.md", "", auth, 200},
		{"todos top", http.MethodGet, "/todos/top?tags=true", "", auth, 200},
		{"delete", http.MethodDelete, "/todos/tasks/t1", "", map[string]string{"Authorization": "Bearer secret", "If-Match": "*"}, 204},
	}

	run := func(t *testing.T, handler http.Handler, tc contractCase) {
		t.Helper()
		var body io.Reader
		if tc.body != "" {
			body = strings.NewReader(tc.body)
		}
		req := httptest.NewRequest(tc.method, contractBase+tc.path, body)
		for k, v := range tc.headers {
			req.Header.Set(k, v)
		}
		route, pathParams, err := router.FindRoute(req)
		if err != nil {
			t.Fatalf("%s %s is not in the contract: %v", tc.method, tc.path, err)
		}
		input := &openapi3filter.RequestValidationInput{
			Request: req, PathParams: pathParams, Route: route,
			Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc},
		}
		// Only validate requests the contract says are well formed.
		if tc.status < 400 {
			if err := openapi3filter.ValidateRequest(context.Background(), input); err != nil {
				t.Fatalf("test request violates the contract: %v", err)
			}
			if tc.body != "" {
				req.Body = io.NopCloser(strings.NewReader(tc.body))
			}
		}

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != tc.status {
			t.Fatalf("status = %d, want %d; body = %s", rr.Code, tc.status, rr.Body)
		}
		err = openapi3filter.ValidateResponse(context.Background(), &openapi3filter.ResponseValidationInput{
			RequestValidationInput: input,
			Status:                 rr.Code,
			Header:                 rr.Header(),
			Body:                   io.NopCloser(bytes.NewReader(rr.Body.Bytes())),
			Options:                &openapi3filter.Options{IncludeResponseStatus: true},
		})
		if err != nil {
			t.Fatalf("response violates the contract: %v\nbody: %s", err, rr.Body)
		}
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { run(t, h, tc) })
	}
	t.Run("warming up", func(t *testing.T) {
		run(t, warming, contractCase{"warming", http.MethodGet, "/top", "", auth, 503})
	})
}
