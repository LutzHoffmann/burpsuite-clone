package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/lutzifer/burpsuite-clone/internal/activechecks"
	"github.com/lutzifer/burpsuite-clone/internal/store"
)

type ActiveChecks interface {
	Start(context.Context, activechecks.Request) (activechecks.Report, error)
	Cancel(int64) error
}

func activeCheckID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, activechecks.ErrInvalidInput
	}
	return id, nil
}
func (s *Server) activeCheckStore(w http.ResponseWriter) (store.ActiveCheckStore, bool) {
	w.Header().Set("Cache-Control", "no-store")
	repo, ok := s.cfg.Store.(store.ActiveCheckStore)
	if !ok {
		http.Error(w, "active check history unavailable", http.StatusServiceUnavailable)
	}
	return repo, ok
}
func (s *Server) handleActiveChecksStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.cfg.ActiveChecks == nil {
		http.Error(w, "active checks unavailable", http.StatusServiceUnavailable)
		return
	}
	var input activechecks.Request
	if err := decodeJSONLimit(w, r, &input, 1024); err != nil {
		return
	}
	report, err := s.cfg.ActiveChecks.Start(r.Context(), input)
	if err != nil {
		switch {
		case errors.Is(err, activechecks.ErrInvalidInput):
			http.Error(w, "invalid active check input", http.StatusUnprocessableEntity)
		case errors.Is(err, activechecks.ErrScopeDenied):
			http.Error(w, "target outside scope", http.StatusForbidden)
		case errors.Is(err, activechecks.ErrBusy), errors.Is(err, store.ErrActiveCheckLimit):
			http.Error(w, "active checks busy or run limit reached", http.StatusConflict)
		case errors.Is(err, sql.ErrNoRows):
			http.Error(w, "crawl not found", http.StatusNotFound)
		default:
			http.Error(w, "active checks failed", http.StatusInternalServerError)
		}
		return
	}
	writeJSON(w, http.StatusAccepted, report)
}
func (s *Server) handleActiveChecksList(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.activeCheckStore(w)
	if !ok {
		return
	}
	runs, err := repo.ListActiveCheckRuns(r.Context())
	if err != nil {
		http.Error(w, "list active checks", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}
func (s *Server) handleActiveChecksGet(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.activeCheckStore(w)
	if !ok {
		return
	}
	id, err := activeCheckID(r)
	if err != nil {
		http.Error(w, "invalid run id", http.StatusBadRequest)
		return
	}
	run, err := repo.GetActiveCheckRun(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "get active checks", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, run)
}
func (s *Server) handleActiveChecksDelete(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.activeCheckStore(w)
	if !ok {
		return
	}
	id, err := activeCheckID(r)
	if err != nil {
		http.Error(w, "invalid run id", http.StatusBadRequest)
		return
	}
	err = repo.DeleteActiveCheckRun(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "run not found or still running", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "delete active checks", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) handleActiveChecksCancel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.cfg.ActiveChecks == nil {
		http.Error(w, "active checks unavailable", http.StatusServiceUnavailable)
		return
	}
	id, err := activeCheckID(r)
	if err != nil {
		http.Error(w, "invalid run id", http.StatusBadRequest)
		return
	}
	if err := s.cfg.ActiveChecks.Cancel(id); err != nil {
		http.Error(w, "run not active", http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
