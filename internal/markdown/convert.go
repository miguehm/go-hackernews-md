package markdown

import (
	"fmt"
	"html"
	"net/url"
	"strings"
	"time"

	"hnmd/internal/store"

	htmltomd "github.com/JohannesKaufmann/html-to-markdown/v2"
)

func HTMLToMarkdown(htmlStr string) (string, error) {
	md, err := htmltomd.ConvertString(htmlStr)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(md), nil
}

type Meta struct {
	Title       string
	URL         string
	HNURL       string
	By          string
	Score       int
	Descendants int
	PublishedAt string
	Type        string
	Domain      string
	HasExternal bool
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
	source := "Ask HN / sin URL externa"
	if meta.HasExternal && meta.URL != "" {
		source = meta.URL
	}
	if meta.Domain == "" {
		meta.Domain = "-"
	}
	if meta.PublishedAt == "" {
		meta.PublishedAt = "-"
	}
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(escapeTitle(meta.Title))
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "- **Fuente:** %s\n", source)
	fmt.Fprintf(&b, "- **HN:** %s\n", meta.HNURL)
	fmt.Fprintf(&b, "- **Autor:** %s | **Puntos:** %d | **Comentarios:** %d\n", safe(meta.By), meta.Score, meta.Descendants)
	fmt.Fprintf(&b, "- **Publicado:** %s | **Tipo:** %s | **Dominio:** %s\n", meta.PublishedAt, safe(meta.Type), meta.Domain)
	b.WriteString("\n---\n\n")
	if strings.TrimSpace(body) == "" {
		b.WriteString("_Sin contenido extraído._\n")
	} else {
		b.WriteString(body)
		b.WriteString("\n")
	}
	return b.String()
}

func escapeTitle(t string) string {
	if t == "" {
		return "(sin título)"
	}
	return t
}

func safe(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func BuildNewsItem(rank int, id int, itemType, title, by, externalURL string, score, descendants int, ts int64, bodyMD string, status string, errMsg *string) store.NewsItem {
	hnURL := fmt.Sprintf("https://news.ycombinator.com/item?id=%d", id)
	var urlPtr *string
	domain := ""
	hasExt := false
	if externalURL != "" {
		u := externalURL
		urlPtr = &u
		domain = DomainFromURL(externalURL)
		hasExt = true
	}
	meta := Meta{
		Title:       title,
		URL:         externalURL,
		HNURL:       hnURL,
		By:          by,
		Score:       score,
		Descendants: descendants,
		PublishedAt: store.FormatTime(ts),
		Type:        itemType,
		Domain:      domain,
		HasExternal: hasExt,
	}
	md := Render(meta, bodyMD)
	scraped := store.NowRFC3339()
	return store.NewsItem{
		ID:          id,
		Rank:        rank,
		Type:        itemType,
		Title:       title,
		URL:         urlPtr,
		HNURL:       hnURL,
		By:          by,
		Score:       score,
		Descendants: descendants,
		Time:        ts,
		PublishedAt: store.FormatTime(ts),
		Domain:      domain,
		Markdown:    md,
		Status:      status,
		Error:       errMsg,
		ScrapedAt:   &scraped,
	}
}

func ErrorItem(rank int, id int, itemType, title, by, externalURL string, score, descendants int, ts int64, errMsg string) store.NewsItem {
	e := errMsg
	item := BuildNewsItem(rank, id, itemType, title, by, externalURL, score, descendants, ts, "", "error", &e)
	item.Markdown = ""
	return item
}

// Ensure time import used in some contexts
var _ = time.Now
var _ = html.EscapeString
