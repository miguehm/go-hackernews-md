package scraper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

const (
	userAgent = "hnmd-bot/1.0 (+https://news.ycombinator.com)"
	// maxRedirects bounds how many redirects a single fetch may follow.
	maxRedirects = 5
)

var (
	// ErrBlockedAddress is returned when a URL (or a redirect target) resolves
	// to an address that must not be reached from the server, such as
	// loopback, private, link-local or metadata ranges.
	ErrBlockedAddress = errors.New("blocked address")
	// ErrUnsupportedScheme is returned for URLs that are not http or https.
	ErrUnsupportedScheme = errors.New("unsupported scheme")
	// ErrTooManyRedirects is returned when a fetch exceeds maxRedirects.
	ErrTooManyRedirects = errors.New("too many redirects")
	// ErrUnsupportedContentType is returned when the response is not HTML.
	ErrUnsupportedContentType = errors.New("unsupported content-type")
	// ErrBodyTooLarge is returned when the response exceeds the size limit.
	ErrBodyTooLarge = errors.New("body too large")
)

// StatusError reports a non-2xx upstream response.
type StatusError struct {
	Code int
}

func (e *StatusError) Error() string { return fmt.Sprintf("status %d", e.Code) }

type Fetcher struct {
	client  *http.Client
	maxSize int64
}

// NewFetcher returns a fetcher that refuses to connect to non-public
// addresses. The check runs on the resolved IP at dial time, so it also
// covers redirects and DNS rebinding.
func NewFetcher(timeout time.Duration, maxSize int64) *Fetcher {
	return newFetcher(timeout, maxSize, IsPublicAddr)
}

func newFetcher(timeout time.Duration, maxSize int64, allowed func(netip.Addr) bool) *Fetcher {
	dialer := &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
		Control:   dialControl(allowed),
	}
	transport := &http.Transport{
		// No proxy: going through one would make the dial-time IP check
		// validate the proxy instead of the real destination.
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
	}
	return &Fetcher{
		client: &http.Client{
			Timeout:       timeout,
			Transport:     transport,
			CheckRedirect: checkRedirect,
		},
		maxSize: maxSize,
	}
}

func dialControl(allowed func(netip.Addr) bool) func(network, address string, _ syscall.RawConn) error {
	return func(network, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return fmt.Errorf("%w: %s", ErrBlockedAddress, address)
		}
		addr, err := netip.ParseAddr(host)
		if err != nil || !allowed(addr.Unmap()) {
			return fmt.Errorf("%w: %s", ErrBlockedAddress, host)
		}
		return nil
	}
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return ErrTooManyRedirects
	}
	return validateScheme(req.URL)
}

func validateScheme(u *url.URL) error {
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedScheme, u.Scheme)
	}
}

// nonPublicPrefixes lists ranges not covered by the netip.Addr predicates
// used in IsPublicAddr.
var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "this" network
	netip.MustParsePrefix("100.64.0.0/10"),   // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved + broadcast
	netip.MustParsePrefix("64:ff9b::/96"),    // NAT64 (can embed private IPv4)
	netip.MustParsePrefix("64:ff9b:1::/48"),  // local-use NAT64
	netip.MustParsePrefix("2001:db8::/32"),   // documentation
}

// IsPublicAddr reports whether addr is a globally routable unicast address.
func IsPublicAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() ||
		addr.IsLoopback() ||
		addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsInterfaceLocalMulticast() ||
		addr.IsMulticast() ||
		addr.IsUnspecified() {
		return false
	}
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

func (f *Fetcher) FetchHTML(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	if err := validateScheme(req.URL); err != nil {
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
		return "", &StatusError{Code: resp.StatusCode}
	}
	ct := resp.Header.Get("Content-Type")
	if ct != "" && !strings.Contains(ct, "text/html") && !strings.Contains(ct, "application/xhtml+xml") {
		return "", fmt.Errorf("%w: %s", ErrUnsupportedContentType, ct)
	}

	limited := io.LimitReader(resp.Body, f.maxSize+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return "", err
	}
	if int64(len(body)) > f.maxSize {
		return "", ErrBodyTooLarge
	}
	return string(body), nil
}
