package scraper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestFetcher(t *testing.T, maxSize int64, handler http.HandlerFunc) *Fetcher {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	f := NewFetcher(time.Second, maxSize)
	f.client = srv.Client()
	return f
}

func TestNewFetcher(t *testing.T) {
	f := NewFetcher(3*time.Second, 1024)
	if f.client == nil || f.client.Timeout != 3*time.Second {
		t.Fatalf("client = %+v, want timeout 3s", f.client)
	}
	if f.maxSize != 1024 {
		t.Fatalf("maxSize = %d, want 1024", f.maxSize)
	}
}

func TestFetchHTML(t *testing.T) {
	tests := []struct {
		name    string
		maxSize int64
		handler http.HandlerFunc
		want    string
		wantErr string
	}{
		{
			name:    "ok with charset",
			maxSize: 1024,
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("method = %q, want GET", r.Method)
				}
				if got := r.Header.Get("User-Agent"); got != userAgent {
					t.Errorf("User-Agent = %q, want %q", got, userAgent)
				}
				if got := r.Header.Get("Accept"); !strings.Contains(got, "text/html") {
					t.Errorf("Accept = %q, want text/html", got)
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write([]byte("<html><body>hi</body></html>"))
			},
			want: "<html><body>hi</body></html>",
		},
		{
			name:    "ok xhtml",
			maxSize: 1024,
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/xhtml+xml")
				_, _ = w.Write([]byte("<html/>"))
			},
			want: "<html/>",
		},
		{
			name:    "ok without content type",
			maxSize: 1024,
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header()["Content-Type"] = nil
				_, _ = w.Write([]byte("plain"))
			},
			want: "plain",
		},
		{
			name:    "body exactly at limit",
			maxSize: 5,
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				_, _ = w.Write([]byte("12345"))
			},
			want: "12345",
		},
		{
			name:    "status error",
			maxSize: 1024,
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			},
			wantErr: "status 404",
		},
		{
			name:    "redirect status is error",
			maxSize: 1024,
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusMovedPermanently)
			},
			wantErr: "status 301",
		},
		{
			name:    "unsupported content type",
			maxSize: 1024,
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			},
			wantErr: "unsupported content-type: application/json",
		},
		{
			name:    "body too large",
			maxSize: 5,
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				_, _ = w.Write([]byte("123456"))
			},
			wantErr: "body too large",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newTestFetcher(t, tt.maxSize, tt.handler)
			body, err := f.FetchHTML(context.Background(), "http://example.com/article")
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				if body != "" {
					t.Fatalf("body = %q, want empty", body)
				}
				return
			}
			if err != nil {
				t.Fatalf("FetchHTML: %v", err)
			}
			if body != tt.want {
				t.Fatalf("body = %q, want %q", body, tt.want)
			}
		})
	}
}

func TestFetchHTMLInvalidURL(t *testing.T) {
	f := NewFetcher(time.Second, 1024)
	if body, err := f.FetchHTML(context.Background(), "http://[::1"); err == nil || body != "" {
		t.Fatalf("FetchHTML = %q, %v; want error and empty body", body, err)
	}
}

func TestFetchHTMLContextCanceled(t *testing.T) {
	f := newTestFetcher(t, 1024, func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be reached with a canceled context")
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if body, err := f.FetchHTML(ctx, "http://example.com/article"); err == nil || body != "" {
		t.Fatalf("FetchHTML = %q, %v; want error and empty body", body, err)
	}
}
