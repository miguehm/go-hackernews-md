package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"hnmd/internal/store"
	"hnmd/internal/todo"
)

const (
	todoMarkdownMaxBytes = 512 * 1024
	todoBodyMaxBytes     = 64 * 1024
	todoDefaultLimit     = 100
	todoMaxLimit         = 500
)

var todoTaskIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type TodoHandler struct {
	store            *todo.Store
	publicBaseURL    string
	writesAllowed    bool
	maxIndexBytes    int
	maxMarkdown      int
	maxBodyBytes     int64
	defaultLocation  *time.Location
	fallbackModified time.Time
	now              func() time.Time
}

func NewTodoHandler(st *todo.Store, publicBaseURL string, writesAllowed bool) *TodoHandler {
	return NewTodoHandlerWithLocation(st, publicBaseURL, writesAllowed, time.UTC)
}

func NewTodoHandlerWithLocation(st *todo.Store, publicBaseURL string, writesAllowed bool, location *time.Location) *TodoHandler {
	if location == nil {
		location = time.UTC
	}
	return &TodoHandler{
		store:            st,
		publicBaseURL:    strings.TrimRight(publicBaseURL, "/"),
		writesAllowed:    writesAllowed,
		maxIndexBytes:    indexMaxBytes,
		maxMarkdown:      todoMarkdownMaxBytes,
		maxBodyBytes:     todoBodyMaxBytes,
		defaultLocation:  location,
		fallbackModified: time.Now().UTC(),
		now:              time.Now,
	}
}

func writeTodoError(w http.ResponseWriter, status int, code, message, field, taskID string) {
	writeErrorWith(w, status, code, message, errorOptions{field: field, taskID: taskID})
}

func writeTodoMethodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeTodoError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", "", "")
}

func writeTodoValidationError(w http.ResponseWriter, err error) {
	var validationErr *todo.ValidationError
	if errors.As(err, &validationErr) {
		writeTodoError(w, http.StatusBadRequest, "invalid_request", validationErr.Message, validationErr.Field, "")
		return
	}
	writeTodoError(w, http.StatusInternalServerError, "internal_error", "internal server error", "", "")
}

func writeTodoJSON(w http.ResponseWriter, status int, data []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	// Task responses already carry ETag: "<revision>" (the value clients send
	// back in If-Match); only fall back to a content hash when none was set.
	if w.Header().Get("ETag") == "" {
		w.Header().Set("ETag", etagFor(data))
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

// indexModified returns a stable timestamp for cacheable JSON indexes. Task
// writes move it forward; an empty store falls back to the handler start time
// so that repeated reads still share one ETag.
func (h *TodoHandler) indexModified() time.Time {
	if last := h.store.LastModified(); !last.IsZero() {
		return last.UTC()
	}
	if !h.fallbackModified.IsZero() {
		return h.fallbackModified.UTC()
	}
	return time.Now().UTC()
}

func (h *TodoHandler) indexGeneratedAt(modified time.Time) string {
	return modified.UTC().Format(time.RFC3339)
}

// writeTodoIndexJSON serves a revalidatable index. Unlike writeTodoJSON, the
// payload is cacheable (private, no-cache) and If-None-Match can produce a 304.
func (h *TodoHandler) writeTodoIndexJSON(w http.ResponseWriter, r *http.Request, data []byte, modified time.Time) {
	etag := etagFor(data)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", cacheControlRevalidate)
	addVary(w, "Accept-Encoding")
	w.Header().Set("ETag", etag)
	w.Header().Set("Last-Modified", modified.UTC().Format(http.TimeFormat))
	if ifNoneMatch(r, etag) {
		writeNotModified(w, etag, modified)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func parseDueDays(r *http.Request) (int, error) {
	values, ok := r.URL.Query()["due_days"]
	if !ok || len(values) == 0 || strings.TrimSpace(values[0]) == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(values[0]))
	if err != nil || parsed < 1 || parsed > todo.MaxDueDays {
		return 0, &todo.ValidationError{
			Field:   "due_days",
			Message: fmt.Sprintf("due_days must be between 1 and %d", todo.MaxDueDays),
		}
	}
	return parsed, nil
}

func (h *TodoHandler) parseTimezone(r *http.Request) (*time.Location, error) {
	values, ok := r.URL.Query()["tz"]
	if !ok || len(values) == 0 || strings.TrimSpace(values[0]) == "" {
		return h.defaultLocation, nil
	}
	if len(values) != 1 {
		return nil, &todo.ValidationError{Field: "tz", Message: "tz must be a valid IANA timezone"}
	}
	location, err := time.LoadLocation(strings.TrimSpace(values[0]))
	if err != nil {
		return nil, &todo.ValidationError{Field: "tz", Message: "tz must be a valid IANA timezone"}
	}
	return location, nil
}

func (h *TodoHandler) parseTodoFilter(r *http.Request) (todo.Filter, error) {
	var tag, status string
	if values, ok := r.URL.Query()["tag"]; ok && len(values) > 0 {
		tag = values[0]
	}
	if values, ok := r.URL.Query()["status"]; ok && len(values) > 0 {
		status = values[0]
	}
	days, err := parseDueDays(r)
	if err != nil {
		return todo.Filter{}, err
	}
	location, err := h.parseTimezone(r)
	if err != nil {
		return todo.Filter{}, err
	}
	return todo.Filter{Tag: tag, Status: status, DueDays: days, Location: location}.Normalized(), nil
}

func parseTodoTaskID(raw string) (string, bool) {
	if !todoTaskIDPattern.MatchString(raw) {
		return "", false
	}
	return raw, true
}

type ifMatchCondition struct {
	force     bool
	revisions []int
}

func parseIfMatch(r *http.Request) (ifMatchCondition, bool, error) {
	values := r.Header.Values("If-Match")
	var condition ifMatchCondition
	present := false
	for _, value := range values {
		for _, raw := range strings.Split(value, ",") {
			candidate := strings.TrimSpace(raw)
			if candidate == "" {
				continue
			}
			present = true
			if candidate == "*" {
				condition.force = true
				continue
			}
			candidate = strings.TrimPrefix(candidate, "W/")
			revision, err := strconv.Atoi(strings.Trim(candidate, `"`))
			if err != nil || revision < 1 {
				return ifMatchCondition{}, true, &todo.ValidationError{Field: "if_match", Message: "If-Match must be a task revision or *"}
			}
			condition.revisions = append(condition.revisions, revision)
		}
	}
	if !present {
		return ifMatchCondition{}, false, nil
	}
	if !condition.force && len(condition.revisions) == 0 {
		return ifMatchCondition{}, true, &todo.ValidationError{Field: "if_match", Message: "If-Match must be a task revision or *"}
	}
	return condition, true, nil
}

func writeIfMatchRequired(w http.ResponseWriter) {
	writeTodoError(w, http.StatusPreconditionRequired, "precondition_required", "If-Match header is required for task writes", "if_match", "")
}

func writeRevisionConflict(w http.ResponseWriter, current int) {
	w.Header().Set("ETag", `"`+strconv.Itoa(current)+`"`)
	writeErrorWith(w, http.StatusPreconditionFailed, "revision_conflict", "task revision does not match", errorOptions{
		currentRevision: &current,
	})
}

func (h *TodoHandler) nowTime() time.Time {
	if h.now == nil {
		return time.Now()
	}
	return h.now()
}

func (h *TodoHandler) nowRFC3339() string {
	return h.nowTime().UTC().Format(time.RFC3339)
}

func (h *TodoHandler) nowDisplay(location *time.Location) string {
	if location == nil {
		location = time.UTC
	}
	return h.nowTime().In(location).Format("2006-01-02 15:04")
}

func httpTimeFromRFC3339(value string) string {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Now().UTC().Format(http.TimeFormat)
	}
	return parsed.UTC().Format(http.TimeFormat)
}

func (h *TodoHandler) validateFilter(w http.ResponseWriter, filter todo.Filter) bool {
	if filter.Tag != "" && !todoTaskTagPattern.MatchString(filter.Tag) {
		writeTodoError(w, http.StatusBadRequest, "invalid_request", "invalid tag", "tag", "")
		return false
	}
	return true
}

func (h *TodoHandler) requireWrites(w http.ResponseWriter) bool {
	if !h.writesAllowed {
		writeTodoError(w, http.StatusForbidden, "forbidden", "Writes require API_KEYS or TODO_ALLOW_INSECURE_WRITES=true", "", "")
		return false
	}
	return true
}

func (h *TodoHandler) decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if contentType := r.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(contentType), "application/json") {
		writeTodoError(w, http.StatusBadRequest, "invalid_request", "Content-Type must be application/json", "content_type", "")
		return false
	}

	r.Body = http.MaxBytesReader(w, r.Body, h.maxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeTodoError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds 64 KiB", "body", "")
		} else {
			writeTodoError(w, http.StatusBadRequest, "invalid_request", "invalid JSON body", "body", "")
		}
		return false
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		writeTodoError(w, http.StatusBadRequest, "invalid_request", "unexpected trailing JSON", "body", "")
		return false
	}
	return true
}

func (h *TodoHandler) renderMarkdown(tasks []todo.Task, filter todo.Filter, counts todo.Counts) (string, bool) {
	filtered := todo.FilterTasksAt(tasks, filter, h.nowTime())
	generatedAt := h.nowDisplay(filter.Location)
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

func (h *TodoHandler) writeMarkdown(w http.ResponseWriter, r *http.Request, markdown, filename string) {
	lastModified := h.store.LastModified()
	if lastModified.IsZero() {
		lastModified = time.Now().UTC()
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(markdown)))
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", filename))
	w.Header().Set("ETag", etagFor([]byte(markdown)))
	w.Header().Set("Last-Modified", lastModified.UTC().Format(http.TimeFormat))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write([]byte(markdown))
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
	counts := todo.ComputeCountsWith(tasks, todo.CountOptions{Now: h.nowTime(), DueDays: filter.DueDays, Location: filter.Location})
	markdown, _ := h.renderMarkdown(tasks, filter, counts)
	h.writeMarkdown(w, r, markdown, filename)
}

var todoTaskTagPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

func pageBounds(total, offset, limit int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return offset, end
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
	tagCounts, counts, _ := todo.ComputeTagCountsWith(tasks, todo.CountOptions{Now: h.nowTime(), DueDays: filter.DueDays, Location: filter.Location})
	allItems := todo.FilterTasksAt(tasks, filter, h.nowTime())
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

	data, err := json.Marshal(resp)
	if err != nil {
		writeTodoError(w, http.StatusInternalServerError, "internal_error", "failed to encode response", "", "")
		return
	}
	sizeTruncated := false
	for len(data) > h.maxIndexBytes && len(resp.Items) > 0 {
		resp.Items = resp.Items[:len(resp.Items)-1]
		sizeTruncated = true
		data, err = json.Marshal(resp)
		if err != nil {
			writeTodoError(w, http.StatusInternalServerError, "internal_error", "failed to encode response", "", "")
			return
		}
	}
	for len(data) > h.maxIndexBytes && len(resp.Tags) > 0 {
		resp.Tags = resp.Tags[:len(resp.Tags)-1]
		sizeTruncated = true
		data, err = json.Marshal(resp)
		if err != nil {
			writeTodoError(w, http.StatusInternalServerError, "internal_error", "failed to encode response", "", "")
			return
		}
	}
	resp.ReturnedCount = len(resp.Items)
	resp.HasMore = start+len(resp.Items) < len(allItems)
	resp.Truncated = hasMore || sizeTruncated || resp.HasMore
	resp.NextOffset = nil
	if resp.HasMore {
		next := start + len(resp.Items)
		resp.NextOffset = &next
	}
	data, err = json.Marshal(resp)
	if err != nil {
		writeTodoError(w, http.StatusInternalServerError, "internal_error", "failed to encode response", "", "")
		return
	}
	h.writeTodoIndexJSON(w, r, data, modified)
}

type todoFilterJSON struct {
	Tag     *string `json:"tag"`
	Status  string  `json:"status"`
	DueDays int     `json:"due_days,omitempty"`
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
	data, err := json.Marshal(resp)
	if err != nil {
		writeTodoError(w, http.StatusInternalServerError, "internal_error", "failed to encode response", "", "")
		return
	}
	sizeTruncated := false
	for len(data) > h.maxIndexBytes && len(resp.Tags) > 0 {
		resp.Tags = resp.Tags[:len(resp.Tags)-1]
		sizeTruncated = true
		data, err = json.Marshal(resp)
		if err != nil {
			writeTodoError(w, http.StatusInternalServerError, "internal_error", "failed to encode response", "", "")
			return
		}
	}
	resp.ReturnedCount = len(resp.Tags)
	resp.HasMore = start+len(resp.Tags) < len(allTags)
	resp.Truncated = hasMore || sizeTruncated || resp.HasMore
	resp.NextOffset = nil
	if resp.HasMore {
		next := start + len(resp.Tags)
		resp.NextOffset = &next
	}
	data, err = json.Marshal(resp)
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
	counts := todo.ComputeCountsWith(tasks, todo.CountOptions{Now: h.nowTime(), Location: location})
	open := todo.FilterTasksAt(tasks, todo.Filter{Status: "open", Location: location}, h.nowTime())
	updatedAt := h.nowRFC3339()
	if last := h.store.LastModified(); !last.IsZero() {
		updatedAt = last.UTC().Format(time.RFC3339)
	}

	items := make([]store.IndexItem, 0, 1+len(tasks))
	markdownAll, _ := h.renderMarkdown(tasks, todo.Filter{Status: "open", Location: location}, counts)
	items = append(items, h.topItem(r, "todos", 1, fmt.Sprintf("Todos (%d abiertas)", len(open)), "/v1/todos.md", "todos.md", markdownAll, updatedAt))

	if r.URL.Query().Get("tags") == "true" {
		tagCounts, _, _ := todo.ComputeTagCountsWith(tasks, todo.CountOptions{Now: h.nowTime(), Location: location})
		for _, tagCount := range tagCounts {
			if tagCount.Open == 0 {
				continue
			}
			path := "/v1/todos/tags/" + tagCount.Tag + ".md"
			filename := "todos-" + tagCount.Tag + ".md"
			filter := todo.Filter{Tag: tagCount.Tag, Status: "open", Location: location}
			markdown, _ := h.renderMarkdown(tasks, filter, counts)
			id := "todos-tag-" + tagCount.Tag
			items = append(items, h.topItem(r, id, len(items)+1, fmt.Sprintf("#%s (%d abiertas)", tagCount.Tag, tagCount.Open), path, filename, markdown, updatedAt))
		}
	}

	resp := store.IndexResponse{
		SchemaVersion:          1,
		GeneratedAt:            h.nowRFC3339(),
		NextRefreshAt:          nil,
		RefreshIntervalSeconds: nil,
		ReturnedCount:          len(items),
		Truncated:              false,
		Items:                  items,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		writeTodoError(w, http.StatusInternalServerError, "internal_error", "failed to encode response", "", "")
		return
	}
	for len(data) > h.maxIndexBytes && len(resp.Items) > 0 {
		resp.Items = resp.Items[:len(resp.Items)-1]
		resp.ReturnedCount = len(resp.Items)
		resp.Truncated = true
		data, err = json.Marshal(resp)
		if err != nil {
			writeTodoError(w, http.StatusInternalServerError, "internal_error", "failed to encode response", "", "")
			return
		}
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

func (h *TodoHandler) Tasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeTodoMethodNotAllowed(w, http.MethodPost)
		return
	}
	h.CreateTask(w, r)
}

func (h *TodoHandler) CreateTask(w http.ResponseWriter, r *http.Request) {
	if !h.requireWrites(w) {
		return
	}
	var req struct {
		ID      string   `json:"id"`
		Text    string   `json:"text"`
		Tags    []string `json:"tags"`
		Details string   `json:"details"`
		DueDate *string  `json:"due_date"`
		Done    bool     `json:"done"`
	}
	if !h.decodeJSON(w, r, &req) {
		return
	}

	title, tags, err := todo.ParseText(req.Text, req.Tags)
	if err != nil {
		writeTodoValidationError(w, err)
		return
	}
	details := todo.NormalizeDetails(req.Details)
	if err := todo.ValidateDetails(details); err != nil {
		writeTodoValidationError(w, err)
		return
	}
	dueDate, err := todo.NormalizeDueDate(req.DueDate)
	if err != nil {
		writeTodoValidationError(w, err)
		return
	}

	providedID := strings.TrimSpace(req.ID)
	if providedID != "" && !todoTaskIDPattern.MatchString(providedID) {
		writeTodoError(w, http.StatusBadRequest, "invalid_request", "invalid task id", "id", "")
		return
	}
	id := providedID
	if id == "" {
		id, err = todo.NewID()
		if err != nil {
			writeTodoError(w, http.StatusInternalServerError, "internal_error", "failed to generate task id", "", "")
			return
		}
	}
	now := h.nowRFC3339()
	task := todo.Task{
		ID:        id,
		Title:     title,
		Tags:      tags,
		Details:   details,
		DueDate:   dueDate,
		Done:      req.Done,
		CreatedAt: now,
		UpdatedAt: now,
		Revision:  1,
	}
	if req.Done {
		task.DoneAt = &now
	}

	var created todo.Task
	status := http.StatusCreated
	if providedID == "" {
		created, err = h.store.Create(task)
	} else {
		var wasCreated bool
		created, wasCreated, err = h.store.CreateWithID(task)
		if !wasCreated {
			status = http.StatusOK
		}
	}
	if errors.Is(err, todo.ErrTaskLimit) {
		writeTodoError(w, http.StatusBadRequest, "invalid_request", "task limit reached", "text", "")
		return
	}
	if err != nil {
		writeTodoError(w, http.StatusInternalServerError, "internal_error", "failed to save task", "", "")
		return
	}

	data, err := json.Marshal(created)
	if err != nil {
		writeTodoError(w, http.StatusInternalServerError, "internal_error", "failed to encode response", "", "")
		return
	}
	w.Header().Set("Location", "/v1/todos/tasks/"+created.ID)
	w.Header().Set("ETag", `"`+strconv.Itoa(created.Revision)+`"`)
	writeTodoJSON(w, status, data)
}

func (h *TodoHandler) Task(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.GetTask(w, r)
	case http.MethodPatch:
		h.UpdateTask(w, r)
	case http.MethodDelete:
		h.DeleteTask(w, r)
	default:
		writeTodoMethodNotAllowed(w, "GET, PATCH, DELETE")
	}
}

func (h *TodoHandler) GetTask(w http.ResponseWriter, r *http.Request) {
	id, ok := parseTodoTaskID(r.PathValue("id"))
	if !ok {
		writeTodoError(w, http.StatusNotFound, "not_found", "Task not found", "", r.PathValue("id"))
		return
	}
	task, found := h.store.Get(id)
	if !found {
		writeTodoError(w, http.StatusNotFound, "not_found", "Task not found", "", id)
		return
	}
	data, err := json.Marshal(task)
	if err != nil {
		writeTodoError(w, http.StatusInternalServerError, "internal_error", "failed to encode response", "", "")
		return
	}
	w.Header().Set("ETag", `"`+strconv.Itoa(task.Revision)+`"`)
	w.Header().Set("Last-Modified", httpTimeFromRFC3339(task.UpdatedAt))
	writeTodoJSON(w, http.StatusOK, data)
}

// mergeTextTags folds hashtags parsed from a PATCH text into the task's existing
// tags. Stored titles have their hashtags stripped, so a text that contains no
// hashtags must leave the tag list completely untouched. When new hashtags are
// present they are merged (deduplicated and normalized) with the existing tags;
// an explicit `tags` field bypasses this helper and replaces the list outright.
func mergeTextTags(existing, parsed []string) ([]string, error) {
	if len(parsed) == 0 {
		return existing, nil
	}
	return todo.NormalizeTags(append(append([]string(nil), existing...), parsed...))
}

func (h *TodoHandler) UpdateTask(w http.ResponseWriter, r *http.Request) {
	if !h.requireWrites(w) {
		return
	}
	id, ok := parseTodoTaskID(r.PathValue("id"))
	if !ok {
		writeTodoError(w, http.StatusNotFound, "not_found", "Task not found", "", r.PathValue("id"))
		return
	}
	condition, hasCondition, err := parseIfMatch(r)
	if err != nil {
		writeTodoValidationError(w, err)
		return
	}
	if !hasCondition {
		writeIfMatchRequired(w)
		return
	}

	var req struct {
		Text    *string         `json:"text"`
		Tags    *[]string       `json:"tags"`
		Details *string         `json:"details"`
		DueDate json.RawMessage `json:"due_date"`
		Done    *bool           `json:"done"`
	}
	if !h.decodeJSON(w, r, &req) {
		return
	}
	if req.Text == nil && req.Tags == nil && req.Details == nil && req.DueDate == nil && req.Done == nil {
		writeTodoError(w, http.StatusBadRequest, "invalid_request", "at least one field is required", "body", "")
		return
	}

	updated, err := h.store.UpdateIfRevision(id, condition.revisions, condition.force, func(task *todo.Task) error {
		if req.Text != nil {
			title, parsedTags, err := todo.ParseText(*req.Text, nil)
			if err != nil {
				return err
			}
			task.Title = title
			// `text` updates the title only: on its own it never replaces tags.
			// Explicit `tags` (handled below) still takes priority.
			if req.Tags == nil {
				tags, err := mergeTextTags(task.Tags, parsedTags)
				if err != nil {
					return err
				}
				task.Tags = tags
			}
		}
		if req.Tags != nil {
			tags, err := todo.NormalizeTags(*req.Tags)
			if err != nil {
				return err
			}
			task.Tags = tags
		}
		if req.Details != nil {
			details := todo.NormalizeDetails(*req.Details)
			if err := todo.ValidateDetails(details); err != nil {
				return err
			}
			task.Details = details
		}
		if req.DueDate != nil {
			var value *string
			if err := json.Unmarshal(req.DueDate, &value); err != nil {
				return &todo.ValidationError{Field: "due_date", Message: "due_date must be a date string or null"}
			}
			dueDate, err := todo.NormalizeDueDate(value)
			if err != nil {
				return err
			}
			task.DueDate = dueDate
		}
		if req.Done != nil {
			if *req.Done != task.Done {
				task.Done = *req.Done
				if *req.Done {
					now := h.nowRFC3339()
					task.DoneAt = &now
				} else {
					task.DoneAt = nil
				}
			}
		}
		return nil
	})
	if errors.Is(err, todo.ErrNotFound) {
		writeTodoError(w, http.StatusNotFound, "not_found", "Task not found", "", id)
		return
	}
	var conflict *todo.RevisionConflictError
	if errors.As(err, &conflict) {
		writeRevisionConflict(w, conflict.CurrentRevision)
		return
	}
	if err != nil {
		writeTodoValidationError(w, err)
		return
	}

	data, err := json.Marshal(updated)
	if err != nil {
		writeTodoError(w, http.StatusInternalServerError, "internal_error", "failed to encode response", "", "")
		return
	}
	w.Header().Set("ETag", `"`+strconv.Itoa(updated.Revision)+`"`)
	w.Header().Set("Last-Modified", httpTimeFromRFC3339(updated.UpdatedAt))
	writeTodoJSON(w, http.StatusOK, data)
}

func (h *TodoHandler) DeleteTask(w http.ResponseWriter, r *http.Request) {
	if !h.requireWrites(w) {
		return
	}
	id, ok := parseTodoTaskID(r.PathValue("id"))
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	condition, hasCondition, err := parseIfMatch(r)
	if err != nil {
		writeTodoValidationError(w, err)
		return
	}
	if !hasCondition {
		writeIfMatchRequired(w)
		return
	}
	if err := h.store.DeleteIfRevision(id, condition.revisions, condition.force); err != nil {
		var conflict *todo.RevisionConflictError
		if errors.As(err, &conflict) {
			writeRevisionConflict(w, conflict.CurrentRevision)
			return
		}
		writeTodoError(w, http.StatusInternalServerError, "internal_error", "failed to delete task", "", "")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
