package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"hnmd/internal/store"
)

const (
	defaultLimit  = 20
	minLimit      = 1
	maxLimit      = 30
	indexMaxBytes = 128 * 1024
)

var itemIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type Handler struct {
	store             *store.Store
	retryAfterSeconds int
}

func New(st *store.Store, retryAfterSeconds int) *Handler {
	if retryAfterSeconds <= 0 {
		retryAfterSeconds = 300
	}
	return &Handler{store: st, retryAfterSeconds: retryAfterSeconds}
}

// Top is the deprecated alias for /v1/top.
func (h *Handler) Top(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Deprecation", "true")
	h.Index(w, r)
}

type indexPageResponse struct {
	store.IndexResponse
	Offset     int  `json:"offset"`
	Limit      int  `json:"limit"`
	HasMore    bool `json:"has_more"`
	NextOffset *int `json:"next_offset"`
}

func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", "", nil)
		return
	}

	idx, ok := h.store.GetIndex()
	if !ok {
		h.writeWarmingUp(w)
		return
	}

	limit := parseLimit(r)
	offset := parseOffset(r)
	total := len(idx.Items)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	items := append([]store.IndexItem(nil), idx.Items[offset:end]...)
	if items == nil {
		items = []store.IndexItem{}
	}

	resp := indexPageResponse{
		IndexResponse: store.IndexResponse{
			SchemaVersion:          idx.SchemaVersion,
			GeneratedAt:            idx.GeneratedAt,
			NextRefreshAt:          idx.NextRefreshAt,
			RefreshIntervalSeconds: idx.RefreshIntervalSeconds,
			ReturnedCount:          len(items),
			Truncated:              idx.Truncated || end < total,
			Items:                  items,
		},
		Offset: offset,
		Limit:  limit,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to encode response", "", nil)
		return
	}
	sizeTruncated := false
	for len(data) > indexMaxBytes && len(resp.Items) > 0 {
		resp.Items = resp.Items[:len(resp.Items)-1]
		sizeTruncated = true
		data, err = json.Marshal(resp)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to encode response", "", nil)
			return
		}
	}
	if len(data) > indexMaxBytes {
		resp.Items = []store.IndexItem{}
		resp.ReturnedCount = 0
		sizeTruncated = true
		data, err = json.Marshal(resp)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to encode response", "", nil)
			return
		}
	}
	resp.ReturnedCount = len(resp.Items)
	resp.HasMore = offset+len(resp.Items) < total
	resp.Truncated = resp.Truncated || sizeTruncated || resp.HasMore
	if resp.HasMore {
		next := offset + len(resp.Items)
		resp.NextOffset = &next
	}
	data, err = json.Marshal(resp)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to encode response", "", nil)
		return
	}

	etag := etagFor(data)
	var lastModified time.Time
	if parsed, parseErr := time.Parse(time.RFC3339, idx.GeneratedAt); parseErr == nil {
		lastModified = parsed
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", cacheControlRevalidate)
	addVary(w, "Accept-Encoding")
	w.Header().Set("ETag", etag)
	if !lastModified.IsZero() {
		w.Header().Set("Last-Modified", lastModified.UTC().Format(http.TimeFormat))
	}
	if ifNoneMatch(r, etag) {
		writeNotModified(w, etag, lastModified)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (h *Handler) Article(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", "", nil)
		return
	}

	rawID := r.PathValue("itemId")
	id, ok := parseItemID(rawID)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No markdown available for this item", rawID, nil)
		return
	}

	article, ready, found := h.store.GetArticle(id)
	if !ready {
		h.writeWarmingUp(w)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "not_found", "No markdown available for this item", id, nil)
		return
	}

	etag := weakETag(article.ETag)
	w.Header().Set("Content-Type", article.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", article.SuggestedFilename))
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", cacheControlRevalidate)
	addVary(w, "Accept-Encoding")
	if !article.LastModified.IsZero() {
		w.Header().Set("Last-Modified", article.LastModified.UTC().Format(http.TimeFormat))
	}
	if ifNoneMatch(r, etag) {
		writeNotModified(w, etag, article.LastModified)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(article.Markdown)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(article.Markdown)
	}
}

func (h *Handler) writeWarmingUp(w http.ResponseWriter) {
	retryAfter := h.retryAfterSeconds
	w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	writeError(w, http.StatusServiceUnavailable, "warming_up", "Server is warming up, try again later", "", &retryAfter)
}

func parseLimit(r *http.Request) int {
	return parseLimitParam(r, defaultLimit, maxLimit)
}

func parseLimitParam(r *http.Request, def, max int) int {
	values, ok := r.URL.Query()["limit"]
	if !ok || len(values) != 1 {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(values[0]))
	if err != nil {
		return def
	}
	if n < minLimit {
		return minLimit
	}
	if n > max {
		return max
	}
	return n
}

func parseOffset(r *http.Request) int {
	values, ok := r.URL.Query()["offset"]
	if !ok || len(values) != 1 {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(values[0]))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func parseItemID(raw string) (string, bool) {
	if !strings.HasSuffix(raw, ".md") {
		return "", false
	}
	id := strings.TrimSuffix(raw, ".md")
	if !itemIDPattern.MatchString(id) {
		return "", false
	}
	return id, true
}
