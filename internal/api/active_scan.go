package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/lutzifer/burpsuite-clone/internal/activescan"
)

type ActiveScanner interface {
	Scan(context.Context, activescan.Request) (activescan.Report, error)
}

func (s *Server) handleActiveScan(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.cfg.ActiveScanner == nil {
		http.Error(w, "active scanner unavailable", http.StatusServiceUnavailable)
		return
	}
	var input activescan.Request
	if err := decodeJSONLimit(w, r, &input, 1024); err != nil {
		return
	}
	report, err := s.cfg.ActiveScanner.Scan(r.Context(), input)
	if err != nil {
		switch {
		case errors.Is(err, activescan.ErrInvalidInput):
			http.Error(w, "invalid scan input", http.StatusUnprocessableEntity)
		case errors.Is(err, activescan.ErrScopeDenied):
			http.Error(w, "target outside scope", http.StatusForbidden)
		case errors.Is(err, activescan.ErrBusy):
			http.Error(w, "scanner busy", http.StatusConflict)
		case errors.Is(err, sql.ErrNoRows):
			http.Error(w, "history item not found", http.StatusNotFound)
		default:
			http.Error(w, "active scan failed", http.StatusInternalServerError)
		}
		return
	}
	writeJSON(w, http.StatusOK, report)
}
