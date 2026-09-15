package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/lutzifer/burpsuite-clone/internal/store"
)

func historyPageRequest(r *http.Request) (store.HistoryPageRequest, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return store.HistoryPageRequest{}, fmt.Errorf("invalid history query encoding")
	}
	request := store.HistoryPageRequest{Filter: store.HistoryFilter{Search: q.Get("search"), Method: q.Get("method"), Host: q.Get("host")}}
	for name, destination := range map[string]*int64{"beforeId": &request.BeforeID, "snapshotId": &request.SnapshotID} {
		if values, exists := q[name]; exists {
			if len(values) != 1 {
				return request, fmt.Errorf("invalid %s", name)
			}
			value, err := strconv.ParseInt(values[0], 10, 64)
			if err != nil || value <= 0 {
				return request, fmt.Errorf("invalid %s", name)
			}
			*destination = value
		}
	}
	if request.BeforeID > 0 && (request.SnapshotID == 0 || request.BeforeID > request.SnapshotID) {
		return request, fmt.Errorf("invalid history cursor boundary")
	}
	if values, exists := q["inScope"]; exists {
		if len(values) != 1 || (values[0] != "true" && values[0] != "false") {
			return request, fmt.Errorf("invalid inScope")
		}
		value := values[0] == "true"
		request.Filter.InScope = &value
	}
	return request, nil
}

func (s *Server) handleHistoryPage(w http.ResponseWriter, r *http.Request) {
	s.writeHistoryPage(w, r, false)
}

func (s *Server) writeHistoryPage(w http.ResponseWriter, r *http.Request, legacy bool) {
	repository, ok := s.cfg.Store.(store.HistoryPageStore)
	if !ok {
		http.Error(w, "paged history unavailable", http.StatusServiceUnavailable)
		return
	}
	request, err := historyPageRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	page, err := repository.ListHistoryPage(r.Context(), request)
	if err != nil {
		http.Error(w, "list history page", http.StatusInternalServerError)
		return
	}
	items := make([]historyItemDTO, len(page.Items))
	for i, item := range page.Items {
		items[i] = toHistoryItemDTO(item)
	}
	if legacy {
		writeJSON(w, http.StatusOK, items)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Items        []historyItemDTO `json:"items"`
		NextBeforeID int64            `json:"nextBeforeId"`
		SnapshotID   int64            `json:"snapshotId"`
	}{items, page.NextBeforeID, page.SnapshotID})
}
