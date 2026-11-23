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
)

var todoTaskIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type TodoHandler struct {
	store         *todo.Store
	publicBaseURL string
	writesAllowed bool
	maxIndexBytes int
	maxMarkdown   int
	maxBodyBytes  int64
}

func NewTodoHandler(st *todo.Store, publicBaseURL string, writesAllowed bool) *TodoHandler {
	return &TodoHandler{
		store:         st,
		publicBaseURL: strings.TrimRight(publicBaseURL, "/"),
		writesAllowed: writesAllowed,
		maxIndexBytes: indexMaxBytes,
		maxMarkdown:   todoMarkdownMaxBytes,
		maxBodyBytes:  todoBodyMaxBytes,
	}
}

type todoErrorBody struct {
	Error   string  `json:"error"`
	Message string  `json:"message"`
	Field   *string `json:"field,omitempty"`
	TaskID  *string `json:"task_id,omitempty"`
}

func writeTodoError(w http.ResponseWriter, status int, code, message, field, taskID string) {
	body := todoErrorBody{Error: code, Message: message}
	if field != "" {
		body.Field = &field
	}
	if taskID != "" {
		body.TaskID = &taskID
	}
	data, err := json.Marshal(body)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(data)
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
	w.Header().Set("ETag", etagFor(data))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
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

func parseTodoFilter(r *http.Request) (todo.Filter, error) {
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
	return todo.Filter{Tag: tag, Status: status, DueDays: days}.Normalized(), nil
}

func parseTodoTaskID(raw string) (string, bool) {
	if !todoTaskIDPattern.MatchString(raw) {
		return "", false
	}
	return raw, true
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func nowDisplay() string {
	return time.Now().UTC().Format("2006-01-02 15:04")
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
	filtered := todo.FilterTasks(tasks, filter)
	generatedAt := nowDisplay()
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
	filter, err := parseTodoFilter(r)
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
	counts := todo.ComputeCountsWith(tasks, todo.CountOptions{DueDays: filter.DueDays})
	markdown, _ := h.renderMarkdown(tasks, filter, counts)
	h.writeMarkdown(w, r, markdown, filename)
}

var todoTaskTagPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

func (h *TodoHandler) Index(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeTodoMethodNotAllowed(w, http.MethodGet)
		return
	}

	filter, err := parseTodoFilter(r)
	if err != nil {
		writeTodoValidationError(w, err)
		return
	}
	if !h.validateFilter(w, filter) {
		return
	}
	tasks := h.store.Snapshot()
	tagCounts, counts, _ := todo.ComputeTagCountsWith(tasks, todo.CountOptions{DueDays: filter.DueDays})
	items := todo.FilterTasks(tasks, filter)
	if items == nil {
		items = []todo.Task{}
	}

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
		ReturnedCount int             `json:"returned_count"`
		Truncated     bool            `json:"truncated"`
		Items         []todo.Task     `json:"items"`
	}{
		SchemaVersion: 1,
		GeneratedAt:   nowRFC3339(),
		Filter:        responseFilter,
		Counts:        counts,
		Tags:          tagCounts,
		ReturnedCount: len(items),
		Items:         items,
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
	tasks := h.store.Snapshot()
	tagCounts, counts, untagged := todo.ComputeTagCountsWith(tasks, todo.CountOptions{DueDays: days})
	resp := struct {
		SchemaVersion int             `json:"schema_version"`
		GeneratedAt   string          `json:"generated_at"`
		Counts        todo.Counts     `json:"counts"`
		Tags          []todo.TagCount `json:"tags"`
		Untagged      todo.Counts     `json:"untagged"`
	}{
		SchemaVersion: 1,
		GeneratedAt:   nowRFC3339(),
		Counts:        counts,
		Tags:          tagCounts,
		Untagged:      untagged,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		writeTodoError(w, http.StatusInternalServerError, "internal_error", "failed to encode response", "", "")
		return
	}
	writeTodoJSON(w, http.StatusOK, data)
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

	tasks := h.store.Snapshot()
	counts := todo.ComputeCounts(tasks)
	open := todo.FilterTasks(tasks, todo.Filter{Status: "open"})
	updatedAt := nowRFC3339()
	if last := h.store.LastModified(); !last.IsZero() {
		updatedAt = last.UTC().Format(time.RFC3339)
	}

	items := make([]store.IndexItem, 0, 1+len(tasks))
	markdownAll, _ := h.renderMarkdown(tasks, todo.Filter{Status: "open"}, counts)
	items = append(items, h.topItem(r, "todos", 1, fmt.Sprintf("Todos (%d abiertas)", len(open)), "/v1/todos.md", "todos.md", markdownAll, updatedAt))

	if r.URL.Query().Get("tags") == "true" {
		tagCounts, _, _ := todo.ComputeTagCounts(tasks)
		for _, tagCount := range tagCounts {
			if tagCount.Open == 0 {
				continue
			}
			path := "/v1/todos/tags/" + tagCount.Tag + ".md"
			filename := "todos-" + tagCount.Tag + ".md"
			filter := todo.Filter{Tag: tagCount.Tag, Status: "open"}
			markdown, _ := h.renderMarkdown(tasks, filter, counts)
			id := "todos-tag-" + tagCount.Tag
			items = append(items, h.topItem(r, id, len(items)+1, fmt.Sprintf("#%s (%d abiertas)", tagCount.Tag, tagCount.Open), path, filename, markdown, updatedAt))
		}
	}

	resp := store.IndexResponse{
		SchemaVersion:          1,
		GeneratedAt:            nowRFC3339(),
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
	id, err := todo.NewID()
	if err != nil {
		writeTodoError(w, http.StatusInternalServerError, "internal_error", "failed to generate task id", "", "")
		return
	}
	now := nowRFC3339()
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
	created, err := h.store.Create(task)
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
	writeTodoJSON(w, http.StatusCreated, data)
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

	updated, err := h.store.Update(id, func(task *todo.Task) error {
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
					now := nowRFC3339()
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
	if err := h.store.Delete(id); err != nil {
		writeTodoError(w, http.StatusInternalServerError, "internal_error", "failed to delete task", "", "")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
