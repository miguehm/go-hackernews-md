package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func corsTestHandler() http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	})
	authed := BearerAPIKeyAuth([]string{"key"})(inner)
	return CORS(testCORSOrigins)(authed)
}

func TestCORSPreflightBeforeAuth(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/v1/todos/tasks", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "PATCH")
	req.Header.Set("Access-Control-Request-Headers", "authorization,content-type,if-match,if-none-match")
	corsTestHandler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if rr.Body.Len() != 0 {
		t.Fatalf("preflight body = %q", rr.Body.String())
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("Allow-Origin = %q", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, "PATCH") || !strings.Contains(got, "DELETE") || !strings.Contains(got, "OPTIONS") {
		t.Fatalf("Allow-Methods = %q", got)
	}
	allowHeaders := rr.Header().Get("Access-Control-Allow-Headers")
	for _, header := range []string{"Authorization", "Content-Type", "If-Match", "If-None-Match"} {
		if !strings.Contains(allowHeaders, header) {
			t.Fatalf("Allow-Headers = %q, missing %s", allowHeaders, header)
		}
	}
	exposeHeaders := rr.Header().Get("Access-Control-Expose-Headers")
	for _, header := range []string{"ETag", "Retry-After", "Location"} {
		if !strings.Contains(exposeHeaders, header) {
			t.Fatalf("Expose-Headers = %q, missing %s", exposeHeaders, header)
		}
	}
	if got := rr.Header().Get("Vary"); !strings.Contains(got, "Origin") {
		t.Fatalf("Vary = %q", got)
	}
}

func TestCORSPreflightDisallowedOrigin(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/v1/todos/tasks", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Access-Control-Request-Method", "PATCH")
	corsTestHandler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rr.Code)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Allow-Origin = %q for disallowed origin", got)
	}
	if got := rr.Header().Get("Vary"); !strings.Contains(got, "Origin") {
		t.Fatalf("Vary = %q", got)
	}
}

func TestCORSSimpleRequest(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/top", nil)
	req.Header.Set("Origin", "capacitor://localhost")
	req.Header.Set("Authorization", "Bearer key")
	corsTestHandler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "capacitor://localhost" {
		t.Fatalf("Allow-Origin = %q", got)
	}
	if got := rr.Header().Get("Access-Control-Expose-Headers"); !strings.Contains(got, "ETag") {
		t.Fatalf("Expose-Headers = %q", got)
	}
	if got := rr.Header().Get("Vary"); !strings.Contains(got, "Origin") {
		t.Fatalf("Vary = %q", got)
	}
}

func TestCORSWildcardIgnored(t *testing.T) {
	handler := CORS([]string{"*", " "})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://evil.example")
	handler.ServeHTTP(rr, req)
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Allow-Origin = %q, wildcard must not be accepted", got)
	}
}

func TestCORSAndGzipVary(t *testing.T) {
	handler := GzipMiddleware(CORS(testCORSOrigins)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"message":"` + strings.Repeat("x", 128) + `"}`))
	})))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/todos", nil)
	req.Header.Set("Origin", "web")
	req.Header.Set("Accept-Encoding", "gzip")
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if got := rr.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q", got)
	}
	vary := strings.Join(rr.Header().Values("Vary"), ",")
	if !strings.Contains(vary, "Origin") || !strings.Contains(vary, "Accept-Encoding") {
		t.Fatalf("Vary = %q", vary)
	}
}

var testCORSOrigins = []string{"web", "http://localhost:5173", "https://localhost", "capacitor://localhost"}
