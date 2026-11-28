package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"hnmd/internal/hn"
	"hnmd/internal/scraper"
	"hnmd/internal/store"
)

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestFetchErrorCode(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{fmt.Errorf("dial: %w: 10.0.0.1", scraper.ErrBlockedAddress), codeFetchBlocked},
		{fmt.Errorf("%w: \"ftp\"", scraper.ErrUnsupportedScheme), codeUnsupportedScheme},
		{fmt.Errorf("get: %w", scraper.ErrTooManyRedirects), codeTooManyRedirects},
		{fmt.Errorf("%w: application/pdf", scraper.ErrUnsupportedContentType), codeUnsupportedContent},
		{scraper.ErrBodyTooLarge, codeBodyTooLarge},
		{&scraper.StatusError{Code: 403}, "upstream_status_403"},
		{context.DeadlineExceeded, codeFetchTimeout},
		{fmt.Errorf("get: %w", timeoutErr{}), codeFetchTimeout},
		{errors.New("dial tcp 10.0.0.1:443: connection refused"), codeFetchFailed},
	}
	for _, tt := range tests {
		if got := fetchErrorCode(tt.err); got != tt.want {
			t.Errorf("fetchErrorCode(%v) = %q, want %q", tt.err, got, tt.want)
		}
	}
}

func TestProcessOneDoesNotLeakErrorDetails(t *testing.T) {
	secret := "dial tcp 10.0.0.7:443: connect: connection refused"
	sc := &fakeStoryClient{items: map[int]*hn.Item{1: {ID: 1, Title: "T", URL: "https://example.com"}}}
	svc := &Service{cfg: testConfig(), hn: sc, fetcher: &fakeFetcher{err: errors.New(secret)}, store: store.New()}

	item, _ := svc.processOne(context.Background(), 1, 1, nil, nil)
	if item.ContentError == nil || strings.Contains(*item.ContentError, "10.0.0.7") {
		t.Fatalf("content_error = %v, must not expose internal details", item.ContentError)
	}

	sc.itemErrs = map[int]error{1: errors.New(secret)}
	item, _ = svc.processOne(context.Background(), 1, 1, nil, nil)
	if item.ContentError == nil || *item.ContentError != codeHNUnavailable {
		t.Fatalf("content_error = %v, want %q", item.ContentError, codeHNUnavailable)
	}
}
