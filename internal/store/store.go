package store

import (
	"sync"
	"time"
)

type NewsItem struct {
	ID          int        `json:"id"`
	Rank        int        `json:"rank"`
	Type        string     `json:"type"`
	Title       string     `json:"title"`
	URL         *string    `json:"url"`
	HNURL       string     `json:"hn_url"`
	By          string     `json:"by"`
	Score       int        `json:"score"`
	Descendants int        `json:"descendants"`
	Time        int64      `json:"time"`
	PublishedAt string     `json:"published_at"`
	Domain      string     `json:"domain"`
	Markdown    string     `json:"markdown"`
	Status      string     `json:"status"`
	Error       *string    `json:"error"`
	ScrapedAt   *string    `json:"scraped_at"`
}

type Snapshot struct {
	GeneratedAt     string     `json:"generated_at"`
	NextRefreshAt   string     `json:"next_refresh_at"`
	RefreshInterval string     `json:"refresh_interval"`
	Count           int        `json:"count"`
	Items           []NewsItem `json:"items"`
	LastError       string     `json:"last_refresh_error,omitempty"`
	Ready           bool       `json:"-"`
}

type Store struct {
	mu       sync.RWMutex
	snapshot *Snapshot
}

func New() *Store {
	return &Store{}
}

func (s *Store) Set(snap *Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot = snap
}

func (s *Store) Get() (*Snapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.snapshot == nil || !s.snapshot.Ready {
		return nil, false
	}
	cp := *s.snapshot
	items := make([]NewsItem, len(s.snapshot.Items))
	copy(items, s.snapshot.Items)
	cp.Items = items
	return &cp, true
}

func FormatTime(t int64) string {
	if t == 0 {
		return ""
	}
	return time.Unix(t, 0).UTC().Format(time.RFC3339)
}

func NowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}
