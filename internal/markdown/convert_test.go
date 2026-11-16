package markdown

import "testing"

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
