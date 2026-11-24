package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"hnmd/internal/store"
	"hnmd/internal/todo"
)

type StatusHandler struct {
	hn          *store.Store
	todos       *todo.Store
	authEnabled bool
	version     string
	now         func() time.Time
}

func NewStatusHandler(hn *store.Store, todos *todo.Store, authEnabled bool, version string) *StatusHandler {
	if version == "" {
		version = "dev"
	}
	return &StatusHandler{
		hn:          hn,
		todos:       todos,
		authEnabled: authEnabled,
		version:     version,
		now:         time.Now,
	}
}

func (h *StatusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", "", nil)
		return
	}

	resp := struct {
		SchemaVersion          int     `json:"schema_version"`
		Version                string  `json:"version"`
		AuthEnabled            bool    `json:"auth_enabled"`
		Ready                  bool    `json:"ready"`
		SnapshotGeneratedAt    *string `json:"snapshot_generated_at,omitempty"`
		NextRefreshAt          *string `json:"next_refresh_at,omitempty"`
		RefreshIntervalSeconds *int    `json:"refresh_interval_seconds,omitempty"`
		TodoCount              int     `json:"todo_count"`
		ServerTime             string  `json:"server_time"`
	}{
		SchemaVersion: 1,
		Version:       h.version,
		AuthEnabled:   h.authEnabled,
		ServerTime:    h.now().UTC().Format(time.RFC3339),
	}
	if index, ok := h.hn.GetIndex(); ok {
		resp.Ready = true
		resp.SnapshotGeneratedAt = &index.GeneratedAt
		resp.NextRefreshAt = index.NextRefreshAt
		resp.RefreshIntervalSeconds = index.RefreshIntervalSeconds
	}
	if h.todos != nil {
		resp.TodoCount = h.todos.Count()
	}

	data, err := json.Marshal(resp)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to encode response", "", nil)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
