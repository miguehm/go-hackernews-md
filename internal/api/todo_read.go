package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"hnmd/internal/store"
	"hnmd/internal/todo"
)

// renderMarkdown renders tasks as of now, the request's single reference
// time, so filters, counters and the header can never straddle midnight.
func (h *TodoHandler) renderMarkdown(tasks []todo.Task, filter todo.Filter, counts todo.Counts, now time.Time) (string, bool) {
	filtered := todo.FilterTasksAt(tasks, filter, now)
	generatedAt := displayTime(now, filter.Location)
	truncated := false
	for {
		markdown := todo.RenderMarkdown(filtered, todo.RenderOptions{
			Filter:      filter,
			Counts:      counts,
			GeneratedAt: generatedAt,
			Truncated:   truncated,
		})
		if len(markdown) <= h.maxMarkdown {
			return markdown, truncated
		}
		if len(filtered) == 0 {
			return todo.RenderMarkdown(nil, todo.RenderOptions{
				Filter:      filter,
				Counts:      counts,
				GeneratedAt: generatedAt,
				Truncated:   true,
			}), true
		}
		filtered = filtered[:len(filtered)-1]
		truncated = true
	}
}

func (h *TodoHandler) serveMarkdown(w http.ResponseWriter, r *http.Request, pathTag, filename string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeTodoMethodNotAllowed(w, "GET, HEAD")
		return
	}
	filter, err := h.parseTodoFilter(r)
	if err != nil {
		writeTodoValidationError(w, err)
		return
	}
	if pathTag != "" {
		filter.Tag = pathTag
	}
	filter = filter.Normalized()
	if !h.validateFilter(w, filter) {
		return
	}

	tasks := h.store.Snapshot()
	now := h.nowTime()
	counts := todo.ComputeCountsWith(tasks, todo.CountOptions{Now: now, DueDays: filter.DueDays, Location: filter.Location})
	markdown, _ := h.renderMarkdown(tasks, filter, counts, now)
	h.writeMarkdown(w, r, markdown, filename)
}

func (h *TodoHandler) Index(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeTodoMethodNotAllowed(w, http.MethodGet)
		return
	}

	filter, err := h.parseTodoFilter(r)
	if err != nil {
		writeTodoValidationError(w, err)
		return
	}
	if !h.validateFilter(w, filter) {
		return
	}
	tasks := h.store.Snapshot()
	modified := h.indexModified()
	now := h.nowTime()
	tagCounts, counts, _ := todo.ComputeTagCountsWith(tasks, todo.CountOptions{Now: now, DueDays: filter.DueDays, Location: filter.Location})
	allItems := todo.FilterTasksAt(tasks, filter, now)
	if allItems == nil {
		allItems = []todo.Task{}
	}

	limit := parseLimitParam(r, todoDefaultLimit, todoMaxLimit)
	offset := parseOffset(r)
	start, end := pageBounds(len(allItems), offset, limit)
	items := append([]todo.Task{}, allItems[start:end]...)
	hasMore := end < len(allItems)

	responseFilter := todoFilterJSON{Status: filter.Status, DueDays: filter.DueDays}
	if filter.Tag != "" {
		value := filter.Tag
		responseFilter.Tag = &value
	}
	resp := struct {
		SchemaVersion int             `json:"schema_version"`
		GeneratedAt   string          `json:"generated_at"`
		Filter        todoFilterJSON  `json:"filter"`
		Counts        todo.Counts     `json:"counts"`
		Tags          []todo.TagCount `json:"tags"`
		Offset        int             `json:"offset"`
		Limit         int             `json:"limit"`
		ReturnedCount int             `json:"returned_count"`
		Truncated     bool            `json:"truncated"`
		HasMore       bool            `json:"has_more"`
		NextOffset    *int            `json:"next_offset"`
		Items         []todo.Task     `json:"items"`
	}{
		SchemaVersion: 1,
		GeneratedAt:   h.indexGeneratedAt(modified),
		Filter:        responseFilter,
		Counts:        counts,
		Tags:          tagCounts,
		Offset:        start,
		Limit:         limit,
		ReturnedCount: len(items),
		HasMore:       hasMore,
		Items:         items,
	}
	if hasMore {
		next := end
		resp.NextOffset = &next
	}

	// Drop items from the tail first, then tag summaries, until it fits.
	_, sizeTruncated, err := marshalWithin(h.maxIndexBytes, &resp, func() bool {
		return dropLast(&resp.Items) || dropLast(&resp.Tags)
	})
	if err != nil {
		writeTodoError(w, http.StatusInternalServerError, "internal_error", "failed to encode response", "", "")
		return
	}
	resp.ReturnedCount = len(resp.Items)
	resp.HasMore = start+len(resp.Items) < len(allItems)
	resp.Truncated = hasMore || sizeTruncated || resp.HasMore
	resp.NextOffset = nil
	if resp.HasMore {
		next := start + len(resp.Items)
		resp.NextOffset = &next
	}
	data, err := json.Marshal(resp)
	if err != nil {
		writeTodoError(w, http.StatusInternalServerError, "internal_error", "failed to encode response", "", "")
		return
	}
	h.writeTodoIndexJSON(w, r, data, modified)
}

func (h *TodoHandler) Markdown(w http.ResponseWriter, r *http.Request) {
	h.serveMarkdown(w, r, "", "todos.md")
}

func (h *TodoHandler) TagsMarkdown(w http.ResponseWriter, r *http.Request) {
	rawTag := r.PathValue("tag")
	if !strings.HasSuffix(rawTag, ".md") {
		writeTodoError(w, http.StatusNotFound, "not_found", "Tag not found", "", rawTag)
		return
	}
	tag := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(rawTag, ".md")))
	filename := "todos-" + tag + ".md"
	h.serveMarkdown(w, r, tag, filename)
}

func (h *TodoHandler) Tags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeTodoMethodNotAllowed(w, http.MethodGet)
		return
	}
	days, err := parseDueDays(r)
	if err != nil {
		writeTodoValidationError(w, err)
		return
	}
	location, err := h.parseTimezone(r)
	if err != nil {
		writeTodoValidationError(w, err)
		return
	}
	tasks := h.store.Snapshot()
	modified := h.indexModified()
	allTags, counts, untagged := todo.ComputeTagCountsWith(tasks, todo.CountOptions{Now: h.nowTime(), DueDays: days, Location: location})
	limit := parseLimitParam(r, todoDefaultLimit, todoMaxLimit)
	offset := parseOffset(r)
	start, end := pageBounds(len(allTags), offset, limit)
	tags := append([]todo.TagCount(nil), allTags[start:end]...)
	hasMore := end < len(allTags)
	resp := struct {
		SchemaVersion int             `json:"schema_version"`
		GeneratedAt   string          `json:"generated_at"`
		Counts        todo.Counts     `json:"counts"`
		Tags          []todo.TagCount `json:"tags"`
		Untagged      todo.Counts     `json:"untagged"`
		Offset        int             `json:"offset"`
		Limit         int             `json:"limit"`
		ReturnedCount int             `json:"returned_count"`
		Truncated     bool            `json:"truncated"`
		HasMore       bool            `json:"has_more"`
		NextOffset    *int            `json:"next_offset"`
	}{
		SchemaVersion: 1,
		GeneratedAt:   h.indexGeneratedAt(modified),
		Counts:        counts,
		Tags:          tags,
		Untagged:      untagged,
		Offset:        start,
		Limit:         limit,
		ReturnedCount: len(tags),
		HasMore:       hasMore,
	}
	if hasMore {
		next := end
		resp.NextOffset = &next
	}
	_, sizeTruncated, err := marshalWithin(h.maxIndexBytes, &resp, func() bool { return dropLast(&resp.Tags) })
	if err != nil {
		writeTodoError(w, http.StatusInternalServerError, "internal_error", "failed to encode response", "", "")
		return
	}
	resp.ReturnedCount = len(resp.Tags)
	resp.HasMore = start+len(resp.Tags) < len(allTags)
	resp.Truncated = hasMore || sizeTruncated || resp.HasMore
	resp.NextOffset = nil
	if resp.HasMore {
		next := start + len(resp.Tags)
		resp.NextOffset = &next
	}
	data, err := json.Marshal(resp)
	if err != nil {
		writeTodoError(w, http.StatusInternalServerError, "internal_error", "failed to encode response", "", "")
		return
	}
	h.writeTodoIndexJSON(w, r, data, modified)
}

func (h *TodoHandler) absoluteURL(r *http.Request, path string) string {
	base := h.publicBaseURL
	if base == "" {
		scheme := "https"
		if r.TLS == nil {
			scheme = "http"
		}
		base = scheme + "://" + r.Host
	}
	return base + path
}

func (h *TodoHandler) Top(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeTodoMethodNotAllowed(w, http.MethodGet)
		return
	}

	location, err := h.parseTimezone(r)
	if err != nil {
		writeTodoValidationError(w, err)
		return
	}
	tasks := h.store.Snapshot()
	now := h.nowTime()
	nowRFC3339 := now.UTC().Format(time.RFC3339)
	counts := todo.ComputeCountsWith(tasks, todo.CountOptions{Now: now, Location: location})
	open := todo.FilterTasksAt(tasks, todo.Filter{Status: "open", Location: location}, now)
	updatedAt := nowRFC3339
	if last := h.store.LastModified(); !last.IsZero() {
		updatedAt = last.UTC().Format(time.RFC3339)
	}

	items := make([]store.IndexItem, 0, 1+len(tasks))
	markdownAll, _ := h.renderMarkdown(tasks, todo.Filter{Status: "open", Location: location}, counts, now)
	items = append(items, h.topItem(r, "todos", 1, fmt.Sprintf("Todos (%d abiertas)", len(open)), "/v1/todos.md", "todos.md", markdownAll, updatedAt))

	if r.URL.Query().Get("tags") == "true" {
		tagCounts, _, _ := todo.ComputeTagCountsWith(tasks, todo.CountOptions{Now: now, Location: location})
		for _, tagCount := range tagCounts {
			if tagCount.Open == 0 {
				continue
			}
			path := "/v1/todos/tags/" + tagCount.Tag + ".md"
			filename := "todos-" + tagCount.Tag + ".md"
			filter := todo.Filter{Tag: tagCount.Tag, Status: "open", Location: location}
			markdown, _ := h.renderMarkdown(tasks, filter, counts, now)
			id := "todos-tag-" + tagCount.Tag
			items = append(items, h.topItem(r, id, len(items)+1, fmt.Sprintf("#%s (%d abiertas)", tagCount.Tag, tagCount.Open), path, filename, markdown, updatedAt))
		}
	}

	resp := store.IndexResponse{
		SchemaVersion:          1,
		GeneratedAt:            nowRFC3339,
		NextRefreshAt:          nil,
		RefreshIntervalSeconds: nil,
		ReturnedCount:          len(items),
		Truncated:              false,
		Items:                  items,
	}
	data, _, err := marshalWithin(h.maxIndexBytes, &resp, func() bool {
		if !dropLast(&resp.Items) {
			return false
		}
		resp.ReturnedCount = len(resp.Items)
		resp.Truncated = true
		return true
	})
	if err != nil {
		writeTodoError(w, http.StatusInternalServerError, "internal_error", "failed to encode response", "", "")
		return
	}
	writeTodoJSON(w, http.StatusOK, data)
}

func (h *TodoHandler) topItem(r *http.Request, id string, rank int, title, contentPath, filename, markdown, updatedAt string) store.IndexItem {
	contentType := "text/markdown; charset=utf-8"
	contentURL := contentPath
	hnURL := h.absoluteURL(r, contentPath)
	length := len(markdown)
	return store.IndexItem{
		ID:                id,
		Rank:              rank,
		Type:              "todo",
		Title:             title,
		HNURL:             hnURL,
		URL:               nil,
		By:                nil,
		Score:             nil,
		Descendants:       nil,
		PublishedAt:       nil,
		Domain:            nil,
		ContentStatus:     store.StatusOK,
		ContentError:      nil,
		ContentURL:        &contentURL,
		ContentType:       &contentType,
		SuggestedFilename: &filename,
		ContentLength:     &length,
		UpdatedAt:         &updatedAt,
	}
}
