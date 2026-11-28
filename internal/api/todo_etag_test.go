package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Task responses must expose the revision as ETag so that clients can send it
// back verbatim in If-Match (README/openapi contract).
func TestTaskETagIsRevisionAndRoundTrips(t *testing.T) {
	h := newTodoTestHandler(t, true)
	handler := GzipMiddleware(todoTestMux(h))

	create := httptest.NewRequest(http.MethodPost, "/v1/todos/tasks", strings.NewReader(`{"text":"Probar ETag"}`))
	create.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, create)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rr.Code, rr.Body)
	}
	if got := rr.Header().Get("ETag"); got != `"1"` && got != `W/"1"` {
		t.Fatalf("create ETag = %q, want revision 1", got)
	}
	location := rr.Header().Get("Location")

	get := httptest.NewRequest(http.MethodGet, location, nil)
	get.Header.Set("Accept-Encoding", "gzip")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, get)
	etag := rr.Header().Get("ETag")
	if etag != `"1"` && etag != `W/"1"` {
		t.Fatalf("get ETag = %q, want revision 1", etag)
	}

	patch := httptest.NewRequest(http.MethodPatch, location, strings.NewReader(`{"done":true}`))
	patch.Header.Set("Content-Type", "application/json")
	patch.Header.Set("If-Match", etag)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, patch)
	if rr.Code != http.StatusOK {
		t.Fatalf("patch with If-Match %q: status = %d, body = %s", etag, rr.Code, rr.Body)
	}
	if got := rr.Header().Get("ETag"); got != `"2"` && got != `W/"2"` {
		t.Fatalf("patch ETag = %q, want revision 2", got)
	}
}
