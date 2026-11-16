package service

import (
	"strings"
	"testing"

	"hnmd/internal/store"
)

func strPtr(v string) *string { return &v }
func intPtr(v int) *int       { return &v }

func richItem() store.IndexItem {
	return store.IndexItem{
		ID:            "42123456",
		Rank:          1,
		Title:         "A title",
		HNURL:         "https://news.ycombinator.com/item?id=42123456",
		URL:           strPtr("https://example.com/post"),
		By:            strPtr("alice"),
		Score:         intPtr(231),
		Descendants:   intPtr(87),
		PublishedAt:   strPtr("2026-11-16T10:12:00Z"),
		ContentStatus: store.StatusOK,
	}
}

func TestFinishArticleHeaderOnly(t *testing.T) {
	s := &Service{}
	item, article := s.finishArticle(richItem(), "", nil)
	if article == nil {
		t.Fatal("expected article")
	}
	if item.ContentStatus != store.StatusOK {
		t.Fatalf("status = %q, want ok", item.ContentStatus)
	}
	want := "# A title\n\n" +
		"> alice · 231 points · 87 comments · 2026-11-16\n" +
		"> HN: https://news.ycombinator.com/item?id=42123456\n" +
		"> URL: https://example.com/post\n"
	if string(article.Markdown) != want {
		t.Fatalf("markdown = %q, want %q", string(article.Markdown), want)
	}
	if item.ContentLength == nil || *item.ContentLength != len(want) {
		t.Fatalf("content_length = %v, want %d", item.ContentLength, len(want))
	}
	if item.ContentURL == nil || *item.ContentURL != "/v1/articles/42123456.md" {
		t.Fatalf("content_url = %v", item.ContentURL)
	}
	if item.SuggestedFilename == nil || *item.SuggestedFilename != "42123456-a-title.md" {
		t.Fatalf("suggested_filename = %v", item.SuggestedFilename)
	}
}

func TestFinishArticleTooLarge(t *testing.T) {
	s := &Service{}
	item := store.IndexItem{ID: "1", Rank: 1, Title: "Big", HNURL: "https://news.ycombinator.com/item?id=1"}
	item, article := s.finishArticle(item, strings.Repeat("x", maxArticleBytes), nil)
	if article != nil {
		t.Fatal("expected no article")
	}
	if item.ContentStatus != store.StatusError {
		t.Fatalf("status = %q, want error", item.ContentStatus)
	}
	if item.ContentError == nil || *item.ContentError != "too_large" {
		t.Fatalf("content_error = %v", item.ContentError)
	}
}

func TestFinishArticleRetentionFallback(t *testing.T) {
	s := &Service{}
	prev := &store.Article{Content: []byte("prior body")}
	item := store.IndexItem{ID: "1", Rank: 1, Title: "Big", HNURL: "https://news.ycombinator.com/item?id=1"}
	item, article := s.finishArticle(item, strings.Repeat("x", maxArticleBytes), prev)
	if article == nil {
		t.Fatal("expected retained article")
	}
	if item.ContentStatus != store.StatusOK {
		t.Fatalf("status = %q, want ok", item.ContentStatus)
	}
	if string(article.Content) != "prior body" {
		t.Fatalf("content = %q, want prior body", string(article.Content))
	}
}

func TestMetaFromItemFormatsPublishedDate(t *testing.T) {
	meta := metaFromItem(richItem())
	if meta.PublishedDate != "2026-11-16" {
		t.Fatalf("published date = %q, want 2026-11-16", meta.PublishedDate)
	}
	if meta.By != "alice" || meta.Score != 231 || meta.Descendants != 87 {
		t.Fatalf("unexpected meta: %+v", meta)
	}
}

func TestErrorWithMetadataClearsContentFields(t *testing.T) {
	item := richItem()
	item.ContentURL = strPtr("/v1/articles/42123456.md")
	item.ContentType = strPtr("text/markdown; charset=utf-8")
	item.SuggestedFilename = strPtr("42123456-a-title.md")
	item.ContentLength = intPtr(42)

	item = errorWithMetadata(item, "fetch: boom")
	if item.ContentStatus != store.StatusError {
		t.Fatalf("status = %q, want error", item.ContentStatus)
	}
	if item.ContentError == nil || *item.ContentError != "fetch: boom" {
		t.Fatalf("content_error = %v", item.ContentError)
	}
	if item.ContentURL != nil || item.ContentType != nil || item.SuggestedFilename != nil || item.ContentLength != nil {
		t.Fatalf("content fields not cleared: %+v", item)
	}
}
