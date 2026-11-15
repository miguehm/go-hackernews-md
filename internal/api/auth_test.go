package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"hnmd/internal/store"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

func TestBearerAPIKeyAuth(t *testing.T) {
	auth := BearerAPIKeyAuth([]string{"secret-key", "rotated-key"})

	tests := []struct {
		name       string
		authHeader string
		setHeader  bool
		wantStatus int
	}{
		{name: "missing header", setHeader: false, wantStatus: http.StatusUnauthorized},
		{name: "empty header", authHeader: "", setHeader: true, wantStatus: http.StatusUnauthorized},
		{name: "wrong scheme", authHeader: "Basic secret-key", setHeader: true, wantStatus: http.StatusUnauthorized},
		{name: "bearer without token", authHeader: "Bearer ", setHeader: true, wantStatus: http.StatusUnauthorized},
		{name: "bearer whitespace token", authHeader: "Bearer    ", setHeader: true, wantStatus: http.StatusUnauthorized},
		{name: "invalid key", authHeader: "Bearer wrong", setHeader: true, wantStatus: http.StatusUnauthorized},
		{name: "almost matching key", authHeader: "Bearer secret-ke", setHeader: true, wantStatus: http.StatusUnauthorized},
		{name: "valid key", authHeader: "Bearer secret-key", setHeader: true, wantStatus: http.StatusOK},
		{name: "valid rotated key", authHeader: "Bearer rotated-key", setHeader: true, wantStatus: http.StatusOK},
		{name: "lowercase bearer scheme", authHeader: "bearer secret-key", setHeader: true, wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/top", nil)
			if tt.setHeader {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rr := httptest.NewRecorder()
			auth(okHandler()).ServeHTTP(rr, req)
			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
			if tt.wantStatus == http.StatusUnauthorized {
				if got := rr.Header().Get("WWW-Authenticate"); got != "Bearer" {
					t.Fatalf("WWW-Authenticate = %q, want %q", got, "Bearer")
				}
				if ct := rr.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
					t.Fatalf("Content-Type = %q", ct)
				}
				var body map[string]string
				if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
					t.Fatalf("decode body: %v", err)
				}
				if body["error"] != "unauthorized" {
					t.Fatalf("error = %q", body["error"])
				}
			}
		})
	}
}

func TestTopHandlerStillReturns503WhenWarmingUp(t *testing.T) {
	h := New(store.New())
	req := httptest.NewRequest(http.MethodGet, "/top", nil)
	rr := httptest.NewRecorder()
	h.Top(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
}

func TestBearerAPIKeyAuthRejectsWhenNoKeysConfigured(t *testing.T) {
	auth := BearerAPIKeyAuth(nil)
	req := httptest.NewRequest(http.MethodGet, "/top", nil)
	req.Header.Set("Authorization", "Bearer anything")
	rr := httptest.NewRecorder()
	auth(okHandler()).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}
