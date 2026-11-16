package markdown

import (
	"fmt"
	"net/url"
	"strings"

	htmltomd "github.com/JohannesKaufmann/html-to-markdown/v2"
)

const ContentType = "text/markdown; charset=utf-8"

func HTMLToMarkdown(htmlStr string) (string, error) {
	md, err := htmltomd.ConvertString(htmlStr)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(md), nil
}

type Meta struct {
	Title         string
	URL           string
	HNURL         string
	By            string
	Score         int
	Descendants   int
	PublishedDate string
}

func DomainFromURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func Render(meta Meta, body string) string {
	title := inlineText(meta.Title)
	if title == "" {
		title = "(untitled)"
	}
	author := inlineText(meta.By)
	if author == "" {
		author = "unknown"
	}
	published := inlineText(meta.PublishedDate)
	if published == "" {
		published = "unknown"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)
	fmt.Fprintf(&b, "> %s · %d points · %d comments · %s\n", author, meta.Score, meta.Descendants, published)
	if hnURL := inlineText(meta.HNURL); hnURL != "" {
		fmt.Fprintf(&b, "> HN: %s\n", hnURL)
	}
	if externalURL := inlineText(meta.URL); externalURL != "" {
		fmt.Fprintf(&b, "> URL: %s\n", externalURL)
	}
	b.WriteString("\n")
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")
	if body = strings.TrimSpace(body); body != "" {
		b.WriteString(body)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func Slugify(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case b.Len() > 0 && !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 60 {
		out = strings.Trim(out[:60], "-")
	}
	if out == "" {
		out = "article"
	}
	return out
}

func SuggestedFilename(id, title string) string {
	return id + "-" + Slugify(title) + ".md"
}

func inlineText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
