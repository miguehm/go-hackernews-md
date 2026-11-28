package api

import (
	"encoding/json"
	"net/http"
	"strconv"
)

type errorBody struct {
	Error             string  `json:"error"`
	Message           string  `json:"message"`
	Field             *string `json:"field,omitempty"`
	ItemID            *string `json:"item_id,omitempty"`
	TaskID            *string `json:"task_id,omitempty"`
	RetryAfterSeconds *int    `json:"retry_after_seconds,omitempty"`
	CurrentRevision   *int    `json:"current_revision,omitempty"`
}

type errorOptions struct {
	field           string
	itemID          string
	taskID          string
	retryAfter      *int
	currentRevision *int
}

func writeError(w http.ResponseWriter, status int, code, message, itemID string, retryAfter *int) {
	writeErrorWith(w, status, code, message, errorOptions{itemID: itemID, retryAfter: retryAfter})
}

func writeErrorWith(w http.ResponseWriter, status int, code, message string, opts errorOptions) {
	body := errorBody{
		Error:             code,
		Message:           message,
		RetryAfterSeconds: opts.retryAfter,
		CurrentRevision:   opts.currentRevision,
	}
	if opts.field != "" {
		body.Field = &opts.field
	}
	if opts.itemID != "" {
		body.ItemID = &opts.itemID
	}
	if opts.taskID != "" {
		body.TaskID = &opts.taskID
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
