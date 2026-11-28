package api

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"hnmd/internal/todo"
)

const (
	todoMarkdownMaxBytes = 512 * 1024
	todoBodyMaxBytes     = 64 * 1024
	todoDefaultLimit     = 100
	todoMaxLimit         = 500
)

var todoTaskIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// TodoHandler serves the /v1/todos* endpoints. Its methods are split by
// concern: todo_read.go (indexes and Markdown views), todo_write.go (task
// writes and GET of one task), todo_params.go (request parsing) and
// todo_response.go (response writers).
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

func (h *TodoHandler) requireWrites(w http.ResponseWriter) bool {
	if !h.writesAllowed {
		writeTodoError(w, http.StatusForbidden, "forbidden", "Writes require API_KEYS or TODO_ALLOW_INSECURE_WRITES=true", "", "")
		return false
	}
	return true
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

func displayTime(now time.Time, location *time.Location) string {
	if location == nil {
		location = time.UTC
	}
	return now.In(location).Format("2006-01-02 15:04")
}

func httpTimeFromRFC3339(value string) string {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Now().UTC().Format(http.TimeFormat)
	}
	return parsed.UTC().Format(http.TimeFormat)
}
