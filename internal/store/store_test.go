package store

import (
	"sync"
	"testing"
	"time"
)

func readySnapshot() *Snapshot {
	now := time.Now().UTC().Format(time.RFC3339)
	return &Snapshot{
		Ready: true,
		Index: IndexResponse{
			SchemaVersion: 1,
			GeneratedAt:   now,
			Items: []IndexItem{
				{ID: "1", Rank: 1, Title: "first"},
				{ID: "2", Rank: 2, Title: "second"},
			},
		},
		Articles: map[string]*Article{
			"1": {Markdown: []byte("# first"), Content: []byte("first")},
			"2": nil,
		},
	}
}

func TestNewStartsEmpty(t *testing.T) {
	s := New()
	if s.snapshot != nil {
		t.Fatalf("snapshot = %+v, want nil", s.snapshot)
	}
	if idx, ok := s.GetIndex(); ok || idx != nil {
		t.Fatalf("GetIndex = %+v, %v; want nil, false", idx, ok)
	}
	if article, ready, ok := s.GetArticle("1"); article != nil || ready || ok {
		t.Fatalf("GetArticle = %+v, %v, %v; want nil, false, false", article, ready, ok)
	}
	if snap, ok := s.Current(); ok || snap != nil {
		t.Fatalf("Current = %+v, %v; want nil, false", snap, ok)
	}
}

func TestSetAndGetIndex(t *testing.T) {
	s := New()
	snap := readySnapshot()
	s.Set(snap)

	idx, ok := s.GetIndex()
	if !ok || idx == nil {
		t.Fatal("GetIndex should return a ready index")
	}
	if idx.SchemaVersion != 1 || idx.ReturnedCount != 0 || len(idx.Items) != 2 {
		t.Fatalf("index = %+v", idx)
	}
	idx.Items[0].Title = "mutated"
	if got, _ := s.GetIndex(); got.Items[0].Title != "first" {
		t.Fatalf("GetIndex returned an aliased items slice: %+v", got.Items)
	}
}

func TestGetIndexNotReady(t *testing.T) {
	s := New()
	snap := readySnapshot()
	snap.Ready = false
	s.Set(snap)
	if idx, ok := s.GetIndex(); ok || idx != nil {
		t.Fatalf("GetIndex = %+v, %v; want nil, false", idx, ok)
	}
}

func TestGetArticle(t *testing.T) {
	s := New()
	s.Set(readySnapshot())

	tests := []struct {
		name      string
		id        string
		wantBody  string
		wantReady bool
		wantOK    bool
	}{
		{name: "present", id: "1", wantBody: "first", wantReady: true, wantOK: true},
		{name: "present but nil", id: "2", wantReady: true, wantOK: false},
		{name: "missing", id: "3", wantReady: true, wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			article, ready, ok := s.GetArticle(tt.id)
			if ready != tt.wantReady || ok != tt.wantOK {
				t.Fatalf("GetArticle(%q) ready=%v ok=%v, want %v/%v", tt.id, ready, ok, tt.wantReady, tt.wantOK)
			}
			if tt.wantOK {
				if article == nil || string(article.Content) != tt.wantBody {
					t.Fatalf("article = %+v, want content %q", article, tt.wantBody)
				}
			} else if article != nil {
				t.Fatalf("article = %+v, want nil", article)
			}
		})
	}
}

func TestGetArticleNotReady(t *testing.T) {
	s := New()
	snap := readySnapshot()
	snap.Ready = false
	s.Set(snap)
	if article, ready, ok := s.GetArticle("1"); article != nil || ready || ok {
		t.Fatalf("GetArticle = %+v, %v, %v; want nil, false, false", article, ready, ok)
	}
}

func TestCurrent(t *testing.T) {
	s := New()
	snap := readySnapshot()
	s.Set(snap)

	got, ok := s.Current()
	if !ok || got != snap {
		t.Fatalf("Current = %+v, %v; want %+v, true", got, ok, snap)
	}

	snap.Ready = false
	s.Set(snap)
	if got, ok := s.Current(); ok || got != nil {
		t.Fatalf("Current = %+v, %v; want nil, false", got, ok)
	}
}

func TestConcurrentAccess(t *testing.T) {
	s := New()
	s.Set(readySnapshot())
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			s.Set(readySnapshot())
		}()
		go func() {
			defer wg.Done()
			_, _ = s.GetIndex()
		}()
		go func() {
			defer wg.Done()
			_, _, _ = s.GetArticle("1")
			_, _ = s.Current()
		}()
	}
	wg.Wait()
}
