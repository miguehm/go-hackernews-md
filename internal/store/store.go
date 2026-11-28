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

// Snapshot is immutable once passed to Store.Set: readers share the pointer
// returned by Current, so never mutate a published snapshot in place.
type Snapshot struct {
	Index    IndexResponse
	Articles map[string]*Article
	Ready    bool
}

// RefreshError describes the most recent failed refresh.
type RefreshError struct {
	Message string
	At      time.Time
}

type Store struct {
	mu         sync.RWMutex
	snapshot   *Snapshot
	refreshErr *RefreshError
}

func New() *Store {
	return &Store{}
}

func (s *Store) Set(snap *Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot = snap
	if snap != nil && snap.Ready {
		s.refreshErr = nil
	}
}

// RecordRefreshError remembers a failed refresh until the next successful
// Set. It works before the first snapshot exists, so warm-up failures are
// visible in /v1/status.
func (s *Store) RecordRefreshError(message string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshErr = &RefreshError{Message: message, At: at.UTC()}
}

// LastRefreshError returns the latest refresh failure since the last
// successful refresh, if any.
func (s *Store) LastRefreshError() (RefreshError, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.refreshErr == nil {
		return RefreshError{}, false
	}
	return *s.refreshErr, true
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
