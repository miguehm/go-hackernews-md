package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"hnmd/internal/store"
	"hnmd/internal/todo"
)

func TestRouterSetsNosniffOnEveryResponse(t *testing.T) {
	todos, err := todo.NewStore(filepath.Join(t.TempDir(), "todos.json"), 10)
	if err != nil {
		t.Fatal(err)
	}
	h := NewRouter(RouterDeps{
		HN:      store.New(),
		Todos:   todos,
		APIKeys: []string{"secret"},
		Logger:  slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/health", http.StatusOK},
		{"/v1/status", http.StatusUnauthorized},
		{"/nope", http.StatusNotFound},
	} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rr.Code != tc.want || rr.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: status %d (want %d), nosniff %q", tc.path, rr.Code, tc.want, rr.Header().Get("X-Content-Type-Options"))
		}
	}
}
