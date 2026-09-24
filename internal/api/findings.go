package api

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/lutzifer/burpsuite-clone/internal/store"
)

func (s *Server) handleFindings(w http.ResponseWriter, r *http.Request) {
	repository, ok := s.cfg.Store.(store.PassiveFindingsStore)
	if !ok {
		http.Error(w, "findings unavailable", http.StatusServiceUnavailable)
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}
	for key, values := range query {
		if key != "host" && key != "type" && key != "offset" && key != "snapshotId" {
			http.Error(w, "unknown filter", http.StatusBadRequest)
			return
		}
		if len(values) != 1 {
			http.Error(w, "duplicate filter", http.StatusBadRequest)
			return
		}
	}
	request := store.FindingsRequest{Host: query.Get("host"), Type: query.Get("type")}
	if len(request.Host) > 255 {
		http.Error(w, "invalid host", http.StatusBadRequest)
		return
	}
	if request.Type != "" && request.Type != "hsts_missing" && request.Type != "csp_missing" && request.Type != "cookie_secure_missing" {
		http.Error(w, "invalid type", http.StatusBadRequest)
		return
	}
	if value := query.Get("offset"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 || parsed > 1_000_000 {
			http.Error(w, "invalid offset", http.StatusBadRequest)
			return
		}
		request.Offset = parsed
	}
	if value := query.Get("snapshotId"); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < 1 {
			http.Error(w, "invalid snapshot", http.StatusBadRequest)
			return
		}
		request.SnapshotID = parsed
	}
	if request.Offset > 0 && request.SnapshotID == 0 {
		http.Error(w, "snapshot required for paging", http.StatusBadRequest)
		return
	}
	page, err := repository.ListPassiveFindings(r.Context(), request)
	if err != nil {
		http.Error(w, "list findings", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, page)
}
