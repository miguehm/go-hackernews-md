package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"hnmd/internal/todo"
)

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

func writeIfMatchRequired(w http.ResponseWriter) {
	writeTodoError(w, http.StatusPreconditionRequired, "precondition_required", "If-Match header is required for task writes", "if_match", "")
}

func writeRevisionConflict(w http.ResponseWriter, current int) {
	w.Header().Set("ETag", `"`+strconv.Itoa(current)+`"`)
	writeErrorWith(w, http.StatusPreconditionFailed, "revision_conflict", "task revision does not match", errorOptions{
		currentRevision: &current,
	})
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
		_, _ = w.Write([]byte(markdown)) //nolint:gosec // G705: text/markdown with nosniff, never rendered as HTML
	}
}
