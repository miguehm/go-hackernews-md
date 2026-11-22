package markdown

import (
	"strings"
	"testing"
)

func TestRenderHeader(t *testing.T) {
	meta := Meta{
		Title:         "A title",
		URL:           "https://example.com/post",
		HNURL:         "https://news.ycombinator.com/item?id=1",
		By:            "alice",
		Score:         231,
		Descendants:   87,
		PublishedDate: "2026-11-16",
	}
	got := Render(meta, "Body")
	want := "# A title\n\n" +
		"> alice · 231 points · 87 comments · 2026-11-16\n" +
		"> HN: https://news.ycombinator.com/item?id=1\n" +
		"> URL: https://example.com/post\n\n" +
		"Body\n"
	if got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
}

func TestRenderWithoutExternalURL(t *testing.T) {
	meta := Meta{
		Title:         "Ask HN: How?",
		HNURL:         "https://news.ycombinator.com/item?id=2",
		By:            "bob",
		Score:         45,
		Descendants:   30,
		PublishedDate: "2026-11-16",
	}
	got := Render(meta, "")
	want := "# Ask HN: How?\n\n" +
		"> bob · 45 points · 30 comments · 2026-11-16\n" +
		"> HN: https://news.ycombinator.com/item?id=2\n"
	if got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
}

func TestRenderUnknownMetadata(t *testing.T) {
	got := Render(Meta{}, "")
	want := "# (untitled)\n\n> unknown · 0 points · 0 comments · unknown\n"
	if got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
}

func TestSlugify(t *testing.T) {
	tests := map[string]string{
		"A title about something":  "a-title-about-something",
		"Ask HN: How do you do X?": "ask-hn-how-do-you-do-x",
		"---":                      "article",
		"":                         "article",
		"Ünïcode and spaces":       "n-code-and-spaces",
		"Multiple     spaces":      "multiple-spaces",
		"Trailing punctuation!!!":  "trailing-punctuation",
	}
	for input, want := range tests {
		if got := Slugify(input); got != want {
			t.Fatalf("Slugify(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSuggestedFilename(t *testing.T) {
	if got := SuggestedFilename("42123456", "A title about something"); got != "42123456-a-title-about-something.md" {
		t.Fatalf("SuggestedFilename() = %q", got)
	}
}

func TestHTMLToMarkdown(t *testing.T) {
	tests := []struct {
		name string
		html string
		want string
	}{
		{name: "inline formatting", html: "<p>Hello <b>world</b></p>", want: "Hello **world**"},
		{name: "empty", html: "", want: ""},
		{name: "whitespace trimmed", html: "  <p>Text</p>\n", want: "Text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := HTMLToMarkdown(tt.html)
			if err != nil {
				t.Fatalf("HTMLToMarkdown: %v", err)
			}
			if got != tt.want {
				t.Fatalf("HTMLToMarkdown(%q) = %q, want %q", tt.html, got, tt.want)
			}
		})
	}
}

func TestDomainFromURL(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{raw: "", want: ""},
		{raw: "://bad", want: ""},
		{raw: "https://example.com/post?q=1", want: "example.com"},
		{raw: "https://example.com:8080/x", want: "example.com"},
		{raw: "http://sub.domain.test", want: "sub.domain.test"},
	}
	for _, tt := range tests {
		if got := DomainFromURL(tt.raw); got != tt.want {
			t.Fatalf("DomainFromURL(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestRenderNormalizesLineEndings(t *testing.T) {
	got := Render(Meta{Title: "Title"}, "first\r\nsecond\rthird\n")
	want := "# Title\n\n> unknown · 0 points · 0 comments · unknown\n\nfirst\nsecond\nthird\n"
	if got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
}

func TestSlugifyTruncatesTo60(t *testing.T) {
	got := Slugify(strings.Repeat("a", 61))
	if len(got) != 60 || got != strings.Repeat("a", 60) {
		t.Fatalf("Slugify(61 chars) = %q (len %d), want 60 chars", got, len(got))
	}
	got = Slugify(strings.Repeat("a", 59) + " " + strings.Repeat("b", 5))
	if len(got) > 60 || got[len(got)-1] == '-' {
		t.Fatalf("Slugify long with separator = %q (len %d), want no trailing dash", got, len(got))
	}
}
