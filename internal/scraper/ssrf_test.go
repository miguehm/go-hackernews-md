package scraper

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestIsPublicAddr(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"93.184.216.34", true},
		{"2606:4700::1111", true},
		{"127.0.0.1", false},
		{"10.1.2.3", false},
		{"172.16.0.1", false},
		{"192.168.1.1", false},
		{"169.254.169.254", false},
		{"100.64.0.1", false},
		{"0.0.0.0", false},
		{"255.255.255.255", false},
		{"224.0.0.1", false},
		{"::1", false},
		{"::", false},
		{"fe80::1", false},
		{"fd00::1", false},
		{"::ffff:127.0.0.1", false},
		{"::ffff:10.0.0.1", false},
		{"64:ff9b::a00:1", false},
	}
	for _, tt := range tests {
		if got := IsPublicAddr(netip.MustParseAddr(tt.addr)); got != tt.want {
			t.Errorf("IsPublicAddr(%s) = %v, want %v", tt.addr, got, tt.want)
		}
	}
}

func TestFetchHTMLBlocksLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("blocked server must not be reached")
	}))
	t.Cleanup(srv.Close)

	f := NewFetcher(time.Second, 1024)
	if _, err := f.FetchHTML(context.Background(), srv.URL); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("err = %v, want ErrBlockedAddress", err)
	}
}

func TestFetchHTMLRejectsScheme(t *testing.T) {
	f := NewFetcher(time.Second, 1024)
	for _, raw := range []string{"file:///etc/passwd", "ftp://example.com/x", "gopher://example.com"} {
		if _, err := f.FetchHTML(context.Background(), raw); !errors.Is(err, ErrUnsupportedScheme) {
			t.Errorf("%s: err = %v, want ErrUnsupportedScheme", raw, err)
		}
	}
}

// onlyLoopback1 allows 127.0.0.1 so a test server is reachable while any
// other address (e.g. 127.0.0.2) is treated as non-public.
func onlyLoopback1(addr netip.Addr) bool {
	return addr == netip.MustParseAddr("127.0.0.1")
}

func TestFetchHTMLBlocksRedirectToBlockedAddress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		port := r.Host[strings.LastIndex(r.Host, ":")+1:]
		http.Redirect(w, r, "http://127.0.0.2:"+port+"/internal", http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	f := newFetcher(time.Second, 1024, onlyLoopback1)
	if _, err := f.FetchHTML(context.Background(), srv.URL); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("err = %v, want ErrBlockedAddress", err)
	}
}

func TestFetchHTMLRejectsRedirectScheme(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	f := newFetcher(time.Second, 1024, onlyLoopback1)
	if _, err := f.FetchHTML(context.Background(), srv.URL); !errors.Is(err, ErrUnsupportedScheme) {
		t.Fatalf("err = %v, want ErrUnsupportedScheme", err)
	}
}

func TestFetchHTMLRedirectLimit(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Redirect(w, r, "/loop", http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	f := newFetcher(time.Second, 1024, onlyLoopback1)
	if _, err := f.FetchHTML(context.Background(), srv.URL); !errors.Is(err, ErrTooManyRedirects) {
		t.Fatalf("err = %v, want ErrTooManyRedirects", err)
	}
	if hits != maxRedirects {
		t.Fatalf("hits = %d, want %d", hits, maxRedirects)
	}
}

func TestFetchHTMLFollowsAllowedRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/final" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<p>ok</p>"))
			return
		}
		http.Redirect(w, r, "/final", http.StatusMovedPermanently)
	}))
	t.Cleanup(srv.Close)

	f := newFetcher(time.Second, 1024, onlyLoopback1)
	body, err := f.FetchHTML(context.Background(), srv.URL)
	if err != nil || body != "<p>ok</p>" {
		t.Fatalf("FetchHTML = %q, %v", body, err)
	}
}
