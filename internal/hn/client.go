package hn

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	baseURL   = "https://hacker-news.firebaseio.com/v0"
	userAgent = "hnmd-bot/1.0 (+https://news.ycombinator.com)"
)

type Item struct {
	ID          int    `json:"id"`
	Type        string `json:"type"`
	By          string `json:"by"`
	Time        int64  `json:"time"`
	Text        string `json:"text"`
	Dead        bool   `json:"dead"`
	Deleted     bool   `json:"deleted"`
	URL         string `json:"url"`
	Title       string `json:"title"`
	Score       int    `json:"score"`
	Descendants int    `json:"descendants"`
}

type Client struct {
	httpClient *http.Client
	baseURL    string
}

func NewClient(timeout time.Duration) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: timeout},
		baseURL:    baseURL,
	}
}

func (c *Client) TopStoryIDs(ctx context.Context) ([]int, error) {
	req, err := newRequest(ctx, c.baseURL+"/topstories.json")
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("topstories: status %d", resp.StatusCode)
	}
	var ids []int
	if err := json.NewDecoder(resp.Body).Decode(&ids); err != nil {
		return nil, err
	}
	return ids, nil
}

func (c *Client) Item(ctx context.Context, id int) (*Item, error) {
	url := fmt.Sprintf("%s/item/%d.json", c.baseURL, id)
	req, err := newRequest(ctx, url)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("item %d: status %d", id, resp.StatusCode)
	}
	var item Item
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return nil, err
	}
	return &item, nil
}

func newRequest(ctx context.Context, url string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	return req, nil
}
