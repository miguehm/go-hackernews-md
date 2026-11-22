package scraper

import (
	"strings"
	"testing"
)

func TestExtractContent(t *testing.T) {
	html := `<html><head><title>Example</title></head><body>` +
		`<article><h1>Hello</h1><p>This is the article body with enough text to be extracted properly by the readability algorithm.</p></article>` +
		`</body></html>`

	tests := []struct {
		name    string
		html    string
		pageURL string
		want    string
		wantErr bool
	}{
		{name: "valid url", html: html, pageURL: "https://example.com/post", want: "Hello"},
		{name: "invalid url", html: html, pageURL: "://bad", want: "Hello"},
		{name: "empty html", html: "", pageURL: "https://example.com/post", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, err := ExtractContent(tt.html, tt.pageURL)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.want != "" && !strings.Contains(content, tt.want) {
				t.Fatalf("content = %q, want containing %q", content, tt.want)
			}
		})
	}
}
