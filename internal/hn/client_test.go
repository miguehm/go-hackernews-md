package hn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := NewClient(time.Second)
	c.baseURL = srv.URL + "/v0"
	return c
}

func TestNewClient(t *testing.T) {
	c := NewClient(7 * time.Second)
	if c.baseURL != baseURL {
		t.Fatalf("baseURL = %q, want %q", c.baseURL, baseURL)
	}
	if c.httpClient == nil || c.httpClient.Timeout != 7*time.Second {
		t.Fatalf("httpClient = %+v, want timeout 7s", c.httpClient)
	}
}

func TestTopStoryIDs(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    []int
		wantErr string
	}{
		{
			name: "ok",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("method = %q, want GET", r.Method)
				}
				if r.URL.Path != "/v0/topstories.json" {
					t.Errorf("path = %q", r.URL.Path)
				}
				_, _ = w.Write([]byte(`[3,1,2]`))
			},
			want: []int{3, 1, 2},
		},
		{
			name: "empty list",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`[]`))
			},
			want: []int{},
		},
		{
			name: "status error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			wantErr: "topstories: status 500",
		},
		{
			name: "invalid json",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`not-json`))
			},
			wantErr: "invalid character",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, tt.handler)
			ids, err := c.TopStoryIDs(context.Background())
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				if ids != nil {
					t.Fatalf("ids = %v, want nil", ids)
				}
				return
			}
			if err != nil {
				t.Fatalf("TopStoryIDs: %v", err)
			}
			if len(ids) != len(tt.want) {
				t.Fatalf("ids = %v, want %v", ids, tt.want)
			}
			for i := range ids {
				if ids[i] != tt.want[i] {
					t.Fatalf("ids = %v, want %v", ids, tt.want)
				}
			}
		})
	}
}

func TestItem(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    *Item
		wantErr string
	}{
		{
			name: "ok",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("method = %q, want GET", r.Method)
				}
				if r.URL.Path != "/v0/item/42.json" {
					t.Errorf("path = %q", r.URL.Path)
				}
				_, _ = w.Write([]byte(`{"id":42,"type":"story","by":"alice","time":1763282400,"title":"Hello","url":"https://example.com/a","score":231,"descendants":87}`))
			},
			want: &Item{ID: 42, Type: "story", By: "alice", Time: 1763282400, Title: "Hello", URL: "https://example.com/a", Score: 231, Descendants: 87},
		},
		{
			name: "null item",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`null`))
			},
			want: &Item{},
		},
		{
			name: "status error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			},
			wantErr: "item 42: status 404",
		},
		{
			name: "invalid json",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{`))
			},
			wantErr: "unexpected EOF",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, tt.handler)
			item, err := c.Item(context.Background(), 42)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				if item != nil {
					t.Fatalf("item = %+v, want nil", item)
				}
				return
			}
			if err != nil {
				t.Fatalf("Item: %v", err)
			}
			if item == nil || *item != *tt.want {
				t.Fatalf("item = %+v, want %+v", item, tt.want)
			}
		})
	}
}

func TestClientContextCanceled(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be reached with a canceled context")
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if ids, err := c.TopStoryIDs(ctx); err == nil || ids != nil {
		t.Fatalf("TopStoryIDs = %v, %v; want error and nil", ids, err)
	}
	if item, err := c.Item(ctx, 1); err == nil || item != nil {
		t.Fatalf("Item = %v, %v; want error and nil", item, err)
	}
}
