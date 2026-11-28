package service

import (
	"context"
	"errors"
	"net"
	"strconv"

	"hnmd/internal/scraper"
)

// Stable values for IndexItem.ContentError. They are safe to show to clients:
// unlike raw Go errors they never include addresses, hostnames or internal
// details. The full error is logged server-side.
const (
	codeHNUnavailable      = "hn_unavailable"
	codeItemUnavailable    = "item_unavailable"
	codeFetchFailed        = "fetch_failed"
	codeFetchTimeout       = "fetch_timeout"
	codeFetchBlocked       = "fetch_blocked"
	codeUnsupportedScheme  = "unsupported_scheme"
	codeTooManyRedirects   = "too_many_redirects"
	codeUnsupportedContent = "unsupported_content_type"
	codeBodyTooLarge       = "body_too_large"
	codeExtractFailed      = "extract_failed"
	codeConvertFailed      = "convert_failed"
	codeTooLarge           = "too_large"
	codeUpstreamStatusPref = "upstream_status_"
)

func fetchErrorCode(err error) string {
	var statusErr *scraper.StatusError
	var netErr net.Error
	switch {
	case errors.Is(err, scraper.ErrBlockedAddress):
		return codeFetchBlocked
	case errors.Is(err, scraper.ErrUnsupportedScheme):
		return codeUnsupportedScheme
	case errors.Is(err, scraper.ErrTooManyRedirects):
		return codeTooManyRedirects
	case errors.Is(err, scraper.ErrUnsupportedContentType):
		return codeUnsupportedContent
	case errors.Is(err, scraper.ErrBodyTooLarge):
		return codeBodyTooLarge
	case errors.As(err, &statusErr):
		return codeUpstreamStatusPref + strconv.Itoa(statusErr.Code)
	case errors.Is(err, context.DeadlineExceeded),
		errors.As(err, &netErr) && netErr.Timeout():
		return codeFetchTimeout
	default:
		return codeFetchFailed
	}
}
