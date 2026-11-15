package scraper

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const userAgent = "hnmd-bot/1.0 (+https://news.ycombinator.com)"

type Fetcher struct {
	client  *http.Client
	maxSize int64
}

func NewFetcher(timeout time.Duration, maxSize int64) *Fetcher {
	return &Fetcher{
		client:  &http.Client{Timeout: timeout},
		maxSize: maxSize,
	}
}

func (f *Fetcher) FetchHTML(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.1")

	resp, err := f.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if ct != "" && !strings.Contains(ct, "text/html") && !strings.Contains(ct, "application/xhtml+xml") {
		return "", fmt.Errorf("unsupported content-type: %s", ct)
	}

	limited := io.LimitReader(resp.Body, f.maxSize+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return "", err
	}
	if int64(len(body)) > f.maxSize {
		return "", fmt.Errorf("body too large")
	}
	return string(body), nil
}
