package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"hnmd/internal/todo"
)

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
