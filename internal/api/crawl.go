package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/lutzifer/burpsuite-clone/internal/crawl"
	"github.com/lutzifer/burpsuite-clone/internal/store"
)

type Crawler interface {
	Start(context.Context, crawl.Request) (crawl.Report, error)
	Cancel(int64) error
}

func crawlID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, crawl.ErrInvalidInput
	}
	return id, nil
}

func (s *Server) crawlStore(w http.ResponseWriter) (store.CrawlStore, bool) {
	w.Header().Set("Cache-Control", "no-store")
	repository, ok := s.cfg.Store.(store.CrawlStore)
	if !ok {
		http.Error(w, "crawl history unavailable", http.StatusServiceUnavailable)
	}
	return repository, ok
}
func (s *Server) handleCrawlStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.cfg.Crawler == nil {
		http.Error(w, "crawler unavailable", http.StatusServiceUnavailable)
		return
	}
	var input crawl.Request
	if err := decodeJSONLimit(w, r, &input, 1024); err != nil {
		return
	}
	report, err := s.cfg.Crawler.Start(r.Context(), input)
	if err != nil {
		switch {
		case errors.Is(err, crawl.ErrInvalidInput):
			http.Error(w, "invalid crawl input", http.StatusUnprocessableEntity)
		case errors.Is(err, crawl.ErrScopeDenied):
			http.Error(w, "target outside scope", http.StatusForbidden)
		case errors.Is(err, crawl.ErrBusy), errors.Is(err, store.ErrCrawlLimit):
			http.Error(w, "crawler busy or run limit reached", http.StatusConflict)
		case errors.Is(err, sql.ErrNoRows):
			http.Error(w, "history item not found", http.StatusNotFound)
		default:
			http.Error(w, "crawl start failed", http.StatusInternalServerError)
		}
		return
	}
	writeJSON(w, http.StatusAccepted, report)
}
func (s *Server) handleCrawlList(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.crawlStore(w)
	if !ok {
		return
	}
	runs, err := repo.ListCrawlRuns(r.Context())
	if err != nil {
		http.Error(w, "list crawl runs", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}
func (s *Server) handleCrawlGet(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.crawlStore(w)
	if !ok {
		return
	}
	id, err := crawlID(r)
	if err != nil {
		http.Error(w, "invalid crawl id", http.StatusBadRequest)
		return
	}
	run, err := repo.GetCrawlRun(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "crawl not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "get crawl", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, run)
}
func (s *Server) handleCrawlDelete(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.crawlStore(w)
	if !ok {
		return
	}
	id, err := crawlID(r)
	if err != nil {
		http.Error(w, "invalid crawl id", http.StatusBadRequest)
		return
	}
	err = repo.DeleteCrawlRun(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "crawl not found or still running", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "delete crawl", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) handleCrawlCancel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.cfg.Crawler == nil {
		http.Error(w, "crawler unavailable", http.StatusServiceUnavailable)
		return
	}
	id, err := crawlID(r)
	if err != nil {
		http.Error(w, "invalid crawl id", http.StatusBadRequest)
		return
	}
	err = s.cfg.Crawler.Cancel(id)
	if err != nil {
		http.Error(w, "crawl not running", http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
