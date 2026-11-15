package scraper

import (
	"net/url"
	"strings"

	readability "github.com/go-shiori/go-readability"
)

func ExtractContent(html string, pageURL string) (string, error) {
	u, err := url.Parse(pageURL)
	if err != nil {
		u = nil
	}
	article, err := readability.FromReader(strings.NewReader(html), u)
	if err != nil {
		return "", err
	}
	return article.Content, nil
}
