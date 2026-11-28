package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hnmd/internal/store"
)

func TestStatusReportsLastRefreshError(t *testing.T) {
	st := store.New()
	h := NewStatusHandler(st, nil, false, "test")

	get := func() map[string]any {
		t.Helper()
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/status", nil))
		var out map[string]any
		if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return out
	}

	if resp := get(); resp["last_refresh_error"] != nil || resp["last_refresh_error_at"] != nil {
		t.Fatalf("unexpected refresh error fields: %v", resp)
	}

	at := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	st.RecordRefreshError("hn_unavailable", at)
	resp := get()
	if resp["ready"] != false || resp["last_refresh_error"] != "hn_unavailable" || resp["last_refresh_error_at"] != "2026-11-27T10:00:00Z" {
		t.Fatalf("warm-up failure not reported: %v", resp)
	}

	st.Set(&store.Snapshot{Ready: true, Index: store.IndexResponse{SchemaVersion: 1, GeneratedAt: "2026-11-27T10:05:00Z"}})
	if resp := get(); resp["last_refresh_error"] != nil {
		t.Fatalf("error not cleared after successful refresh: %v", resp)
	}
}

func TestCORSExposesRequestID(t *testing.T) {
	h := CORS([]string{"http://localhost:5173"})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	req := httptest.NewRequest(http.MethodOptions, "/v1/todos", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if !strings.Contains(rr.Header().Get("Access-Control-Expose-Headers"), RequestIDHeader) ||
		!strings.Contains(rr.Header().Get("Access-Control-Allow-Headers"), RequestIDHeader) {
		t.Fatalf("headers = %v", rr.Header())
	}
}
