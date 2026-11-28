package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestIDGeneratesAndPropagates(t *testing.T) {
	var seen string
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestIDFrom(r.Context())
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if seen == "" || rr.Header().Get(RequestIDHeader) != seen {
		t.Fatalf("generated id = %q, header = %q", seen, rr.Header().Get(RequestIDHeader))
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(RequestIDHeader, "client-abc.123")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if seen != "client-abc.123" || rr.Header().Get(RequestIDHeader) != "client-abc.123" {
		t.Fatalf("propagated id = %q", seen)
	}

	for _, bad := range []string{"has space", "inj\nected", strings.Repeat("a", 65)} {
		req = httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(RequestIDHeader, bad)
		h.ServeHTTP(httptest.NewRecorder(), req)
		if seen == bad || seen == "" {
			t.Fatalf("invalid id %q was accepted (got %q)", bad, seen)
		}
	}
}

func decodeLogLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		out = append(out, entry)
	}
	return out
}

func testChain(logger *slog.Logger, mux http.Handler) http.Handler {
	return RequestID(AccessLog(logger)(Recover(logger)(GzipMiddleware(CORS(nil)(mux)))))
}

func TestAccessLogRecordsRouteAndStatus(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/todos/tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("short"))
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {})

	h := testChain(logger, mux)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/todos/tasks/abc", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", nil))

	lines := decodeLogLines(t, &buf)
	if len(lines) != 1 {
		t.Fatalf("log lines = %d (%s), want 1: /health must log at debug", len(lines), buf.String())
	}
	entry := lines[0]
	if entry["route"] != "/v1/todos/tasks/{id}" || entry["path"] != "/v1/todos/tasks/abc" {
		t.Fatalf("route/path = %v / %v", entry["route"], entry["path"])
	}
	if entry["status"] != float64(http.StatusTeapot) || entry["bytes"] != float64(5) {
		t.Fatalf("status/bytes = %v / %v", entry["status"], entry["bytes"])
	}
	if id, _ := entry["request_id"].(string); id == "" {
		t.Fatal("missing request_id")
	}
	if _, ok := entry["duration_ms"].(float64); !ok {
		t.Fatalf("duration_ms = %v", entry["duration_ms"])
	}
}

func TestAccessLogWarmupIsWarn(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/top", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	testChain(logger, mux).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/top", nil))
	if lines := decodeLogLines(t, &buf); len(lines) != 1 || lines[0]["level"] != "WARN" {
		t.Fatalf("logs = %s", buf.String())
	}
}

func TestRecoverReturnsJSON500(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	mux := http.NewServeMux()
	mux.HandleFunc("/boom", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("partial body that must not leak"))
		panic("kaboom")
	})

	rr := httptest.NewRecorder()
	testChain(logger, mux).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/boom", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "partial") || !strings.Contains(rr.Body.String(), `"internal_error"`) {
		t.Fatalf("body = %s", rr.Body)
	}
	if rr.Header().Get(RequestIDHeader) == "" {
		t.Fatal("500 response lost X-Request-ID")
	}

	var sawPanic, sawAccess bool
	for _, entry := range decodeLogLines(t, &buf) {
		switch entry["msg"] {
		case "panic":
			sawPanic = entry["panic"] == "kaboom" && strings.Contains(entry["stack"].(string), "goroutine")
		case "request":
			sawAccess = entry["status"] == float64(500) && entry["level"] == "ERROR"
		}
	}
	if !sawPanic || !sawAccess {
		t.Fatalf("logs = %s", buf.String())
	}
}

func TestRecoverRepanicsAbortHandler(t *testing.T) {
	h := Recover(slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	defer func() {
		if v := recover(); v != http.ErrAbortHandler {
			t.Fatalf("recovered %v, want http.ErrAbortHandler", v)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}
