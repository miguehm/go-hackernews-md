package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

const cacheControlRevalidate = "private, no-cache"

func weakETag(value string) string {
	if strings.HasPrefix(value, "W/") {
		return value
	}
	return "W/" + value
}

func etagFor(data []byte) string {
	sum := sha256.Sum256(data)
	return weakETag(`"` + hex.EncodeToString(sum[:]) + `"`)
}

func ifNoneMatch(r *http.Request, etag string) bool {
	for _, header := range r.Header.Values("If-None-Match") {
		for _, candidate := range strings.Split(header, ",") {
			candidate = strings.TrimSpace(candidate)
			if candidate == "*" {
				return true
			}
			if weakETag(candidate) == weakETag(etag) {
				return true
			}
		}
	}
	return false
}

func writeNotModified(w http.ResponseWriter, etag string, lastModified time.Time) {
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", cacheControlRevalidate)
	addVary(w, "Accept-Encoding")
	if !lastModified.IsZero() {
		w.Header().Set("Last-Modified", lastModified.UTC().Format(http.TimeFormat))
	}
	w.WriteHeader(http.StatusNotModified)
}

func addVary(w http.ResponseWriter, value string) {
	addVaryHeader(w.Header(), value)
}

func addVaryHeader(header http.Header, value string) {
	for _, existing := range header.Values("Vary") {
		for _, part := range strings.Split(existing, ",") {
			if strings.EqualFold(strings.TrimSpace(part), value) {
				return
			}
		}
	}
	header.Add("Vary", value)
}
