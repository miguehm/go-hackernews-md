package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"hnmd/internal/todo"
)

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

func (h *TodoHandler) validateFilter(w http.ResponseWriter, filter todo.Filter) bool {
	if filter.Tag != "" && !todo.ValidTag(filter.Tag) {
		writeTodoError(w, http.StatusBadRequest, "invalid_request", "invalid tag", "tag", "")
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

type todoFilterJSON struct {
	Tag     *string `json:"tag"`
	Status  string  `json:"status"`
	DueDays int     `json:"due_days,omitempty"`
}
