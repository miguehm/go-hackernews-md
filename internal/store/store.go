package store

import (
	"sync"
	"time"
)

type ContentStatus string

const (
	StatusOK        ContentStatus = "ok"
	StatusNoContent ContentStatus = "no_content"
	StatusWarmingUp ContentStatus = "warming_up"
	StatusError     ContentStatus = "error"
)

type IndexItem struct {
	ID                string        `json:"id"`
	Rank              int           `json:"rank"`
	Type              string        `json:"type,omitempty"`
	Title             string        `json:"title"`
	HNURL             string        `json:"hn_url"`
	URL               *string       `json:"url"`
	By                *string       `json:"by"`
	Score             *int          `json:"score"`
	Descendants       *int          `json:"descendants"`
	PublishedAt       *string       `json:"published_at"`
	Domain            *string       `json:"domain"`
	ContentStatus     ContentStatus `json:"content_status"`
	ContentError      *string       `json:"content_error"`
	ContentURL        *string       `json:"content_url"`
	ContentType       *string       `json:"content_type"`
	SuggestedFilename *string       `json:"suggested_filename"`
	ContentLength     *int          `json:"content_length"`
	UpdatedAt         *string       `json:"updated_at"`
}

type IndexResponse struct {
	SchemaVersion          int         `json:"schema_version"`
	GeneratedAt            string      `json:"generated_at"`
	NextRefreshAt          *string     `json:"next_refresh_at"`
	RefreshIntervalSeconds *int        `json:"refresh_interval_seconds"`
	ReturnedCount          int         `json:"returned_count"`
	Truncated              bool        `json:"truncated"`
	Items                  []IndexItem `json:"items"`
}

type Article struct {
	Markdown          []byte
	Content           []byte
	ContentType       string
	SuggestedFilename string
	ContentLength     int
	ETag              string
	LastModified      time.Time
	UpdatedAt         time.Time
}

type Snapshot struct {
	Index     IndexResponse
	Articles  map[string]*Article
	Ready     bool
	LastError string
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

func (s *Store) GetIndex() (*IndexResponse, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.snapshot == nil || !s.snapshot.Ready {
		return nil, false
	}
	idx := s.snapshot.Index
	idx.Items = append([]IndexItem(nil), s.snapshot.Index.Items...)
	return &idx, true
}

func (s *Store) GetArticle(id string) (*Article, bool, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.snapshot == nil || !s.snapshot.Ready {
		return nil, false, false
	}
	article, ok := s.snapshot.Articles[id]
	if ok && article == nil {
		return nil, true, false
	}
	return article, true, ok
}

func (s *Store) Current() (*Snapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.snapshot == nil || !s.snapshot.Ready {
		return nil, false
	}
	return s.snapshot, true
}
