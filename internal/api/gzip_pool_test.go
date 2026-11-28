package api

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestGzipSkipsSmallBodies(t *testing.T) {
	const body = `{"ok":true}`
	h := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `"strong"`)
		_, _ = w.Write([]byte(body))
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Header().Get("Content-Encoding") != "" || rr.Body.String() != body {
		t.Fatalf("small body was compressed: %v %q", rr.Header(), rr.Body)
	}
	// Vary must still be present: a larger variant of the resource may be gzipped.
	if !strings.Contains(rr.Header().Get("Vary"), "Accept-Encoding") || rr.Header().Get("ETag") != `"strong"` {
		t.Fatalf("headers = %v", rr.Header())
	}
}

// Pooled writers and buffers must never leak bytes between concurrent responses.
func TestGzipPoolConcurrentIsolation(t *testing.T) {
	h := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(strings.Repeat(r.URL.Query().Get("c"), 2000+len(r.URL.Query().Get("c"))*37)))
	}))
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		c := string(rune('a' + i%26))
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/?c="+c, nil)
			req.Header.Set("Accept-Encoding", "gzip")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			zr, err := gzip.NewReader(rr.Body)
			if err != nil {
				t.Error(err)
				return
			}
			got, err := io.ReadAll(zr)
			if err != nil || string(got) != strings.Repeat(c, 2037) {
				t.Errorf("c=%s: corrupted body (len %d, err %v)", c, len(got), err)
			}
		}()
	}
	wg.Wait()
}
