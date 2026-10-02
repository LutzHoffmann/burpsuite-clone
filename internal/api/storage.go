package api

import (
	"net/http"

	"github.com/lutzifer/burpsuite-clone/internal/store"
)

func (s *Server) handleStorage(w http.ResponseWriter, r *http.Request) {
	repository, ok := s.cfg.Store.(store.QuotaStore)
	if !ok {
		http.Error(w, "storage budget unavailable", http.StatusServiceUnavailable)
		return
	}
	status, err := repository.StorageStatus(r.Context())
	if err != nil {
		http.Error(w, "read storage budget", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleStorageUpdate(w http.ResponseWriter, r *http.Request) {
	repository, ok := s.cfg.Store.(store.QuotaStore)
	if !ok {
		http.Error(w, "storage budget unavailable", http.StatusServiceUnavailable)
		return
	}
	var update struct {
		LimitBytes int64 `json:"limitBytes"`
	}
	if err := s.decodeJSON(w, r, &update); err != nil {
		return
	}
	if update.LimitBytes <= 0 || update.LimitBytes > 1<<40 {
		http.Error(w, "limitBytes must be between 1 byte and 1 TiB", http.StatusBadRequest)
		return
	}
	status, err := repository.SetStorageLimit(r.Context(), update.LimitBytes)
	if err != nil {
		http.Error(w, "update storage budget", http.StatusInternalServerError)
		return
	}
	s.cfg.Events.PublishStoragePaused(status.Paused, status.Revision)
	writeJSON(w, http.StatusOK, status)
}
