package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

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

func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, "internal_error", "method not allowed", "", nil)
		return
	}

	idx, ok := h.store.GetIndex()
	if !ok {
		h.writeWarmingUp(w)
		return
	}

	limit := parseLimit(r)
	items := idx.Items
	truncated := idx.Truncated
	if len(items) > limit {
		items = items[:limit]
		truncated = true
	}

	if items == nil {
		items = []store.IndexItem{}
	}
	resp := store.IndexResponse{
		SchemaVersion:          idx.SchemaVersion,
		GeneratedAt:            idx.GeneratedAt,
		NextRefreshAt:          idx.NextRefreshAt,
		RefreshIntervalSeconds: idx.RefreshIntervalSeconds,
		ReturnedCount:          len(items),
		Truncated:              truncated,
		Items:                  items,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to encode response", "", nil)
		return
	}
	for len(data) > indexMaxBytes && len(resp.Items) > 0 {
		resp.Items = resp.Items[:len(resp.Items)-1]
		resp.ReturnedCount = len(resp.Items)
		resp.Truncated = true
		data, err = json.Marshal(resp)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to encode response", "", nil)
			return
		}
	}
	if len(data) > indexMaxBytes {
		resp.Items = []store.IndexItem{}
		resp.ReturnedCount = 0
		resp.Truncated = true
		data, err = json.Marshal(resp)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to encode response", "", nil)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("ETag", etagFor(data))
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (h *Handler) Article(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, "internal_error", "method not allowed", "", nil)
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

	w.Header().Set("Content-Type", article.ContentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(article.Markdown)))
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", article.SuggestedFilename))
	w.Header().Set("ETag", article.ETag)
	w.Header().Set("Last-Modified", article.LastModified.UTC().Format(http.TimeFormat))
	w.Header().Set("Cache-Control", "no-cache")
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

type errorBody struct {
	Error             string  `json:"error"`
	Message           string  `json:"message"`
	ItemID            *string `json:"item_id,omitempty"`
	RetryAfterSeconds *int    `json:"retry_after_seconds,omitempty"`
}

func writeError(w http.ResponseWriter, status int, code, message, itemID string, retryAfter *int) {
	body := errorBody{
		Error:             code,
		Message:           message,
		RetryAfterSeconds: retryAfter,
	}
	if itemID != "" {
		body.ItemID = &itemID
	}
	data, err := json.Marshal(body)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func parseLimit(r *http.Request) int {
	values, ok := r.URL.Query()["limit"]
	if !ok || len(values) != 1 {
		return defaultLimit
	}
	n, err := strconv.Atoi(strings.TrimSpace(values[0]))
	if err != nil {
		return defaultLimit
	}
	if n < minLimit {
		return minLimit
	}
	if n > maxLimit {
		return maxLimit
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

func etagFor(data []byte) string {
	sum := sha256.Sum256(data)
	return `"` + hex.EncodeToString(sum[:]) + `"`
}
