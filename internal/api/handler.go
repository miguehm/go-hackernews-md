package api

import (
	"encoding/json"
	"net/http"

	"hnmd/internal/store"
)

type Handler struct {
	store *store.Store
}

func New(st *store.Store) *Handler {
	return &Handler{store: st}
}

func (h *Handler) Top(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	snap, ok := h.store.Get()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if !ok {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "warming_up",
			"message": "initial scrape in progress, try again shortly",
		})
		return
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(snap)
}
