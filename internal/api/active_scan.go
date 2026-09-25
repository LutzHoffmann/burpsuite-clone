package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/lutzifer/burpsuite-clone/internal/activescan"
	"github.com/lutzifer/burpsuite-clone/internal/store"
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
		case errors.Is(err, store.ErrActiveScanLimit):
			http.Error(w, "scan history limit reached", http.StatusConflict)
		case errors.Is(err, sql.ErrNoRows):
			http.Error(w, "history item not found", http.StatusNotFound)
		default:
			http.Error(w, "active scan failed", http.StatusInternalServerError)
		}
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) activeScanRuns(w http.ResponseWriter) (store.ActiveScanRunStore, bool) {
	w.Header().Set("Cache-Control", "no-store")
	repository, ok := s.cfg.Store.(store.ActiveScanRunStore)
	if !ok {
		http.Error(w, "scan history unavailable", http.StatusServiceUnavailable)
	}
	return repository, ok
}

func (s *Server) handleActiveScanRuns(w http.ResponseWriter, r *http.Request) {
	repository, ok := s.activeScanRuns(w)
	if !ok {
		return
	}
	runs, err := repository.ListActiveScanRuns(r.Context())
	if err != nil {
		http.Error(w, "list scan history", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (s *Server) handleActiveScanRun(w http.ResponseWriter, r *http.Request) {
	repository, ok := s.activeScanRuns(w)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.Error(w, "invalid scan id", http.StatusBadRequest)
		return
	}
	run, err := repository.GetActiveScanRun(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "scan not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "get scan", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) handleActiveScanRunDelete(w http.ResponseWriter, r *http.Request) {
	repository, ok := s.activeScanRuns(w)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.Error(w, "invalid scan id", http.StatusBadRequest)
		return
	}
	if err := repository.DeleteActiveScanRun(r.Context(), id); errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "scan not found or still running", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "delete scan", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
