package api

import (
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/lutzifer/burpsuite-clone/internal/store"
)

func wsPageRequest(r *http.Request) (store.WSPageRequest, error) {
	var page store.WSPageRequest
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return page, fmt.Errorf("invalid WebSocket query encoding")
	}
	for name, destination := range map[string]*int64{"beforeId": &page.BeforeID, "snapshotId": &page.SnapshotID} {
		if values, exists := q[name]; exists {
			if len(values) != 1 {
				return page, fmt.Errorf("invalid %s", name)
			}
			value, err := wsID(values[0], true)
			if err != nil {
				return page, fmt.Errorf("invalid %s", name)
			}
			*destination = value
		}
	}
	if page.BeforeID > 0 && (page.SnapshotID == 0 || page.BeforeID > page.SnapshotID) {
		return page, fmt.Errorf("invalid WebSocket cursor boundary")
	}
	return page, nil
}

func wsID(raw string, allowZero bool) (int64, error) {
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("invalid WebSocket ID")
		}
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 0 || (!allowZero && id == 0) {
		return 0, fmt.Errorf("invalid WebSocket ID")
	}
	return id, nil
}

func (s *Server) wsRepository(w http.ResponseWriter, r *http.Request) (store.WebSocketStore, bool) {
	if !sameOrigin(r) {
		http.Error(w, "untrusted Origin header", http.StatusForbidden)
		return nil, false
	}
	repository, ok := s.cfg.Store.(store.WebSocketStore)
	if !ok {
		http.Error(w, "WebSocket history unavailable", http.StatusServiceUnavailable)
	}
	return repository, ok
}

func wsReadError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "WebSocket record not found", http.StatusNotFound)
		return
	}
	http.Error(w, "WebSocket history unavailable", http.StatusInternalServerError)
}

func (s *Server) handleWSConnections(w http.ResponseWriter, r *http.Request) {
	repository, ok := s.wsRepository(w, r)
	if !ok {
		return
	}
	request, err := wsPageRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	page, err := repository.ListWSConnections(r.Context(), request)
	if err != nil {
		wsReadError(w, err)
		return
	}
	if page.Items == nil {
		page.Items = []store.WSConnection{}
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) handleWSConnection(w http.ResponseWriter, r *http.Request) {
	repository, ok := s.wsRepository(w, r)
	if !ok {
		return
	}
	id, err := wsID(r.PathValue("id"), false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	connection, err := repository.GetWSConnection(r.Context(), id)
	if err != nil {
		wsReadError(w, err)
		return
	}
	if connection == nil {
		wsReadError(w, sql.ErrNoRows)
		return
	}
	writeJSON(w, http.StatusOK, connection)
}

func (s *Server) handleWSMessages(w http.ResponseWriter, r *http.Request) {
	repository, ok := s.wsRepository(w, r)
	if !ok {
		return
	}
	id, err := wsID(r.PathValue("id"), false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	request, err := wsPageRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	connection, err := repository.GetWSConnection(r.Context(), id)
	if err != nil {
		wsReadError(w, err)
		return
	}
	if connection == nil {
		wsReadError(w, sql.ErrNoRows)
		return
	}
	page, err := repository.ListWSMessages(r.Context(), id, request)
	if err != nil {
		wsReadError(w, err)
		return
	}
	if page.Items == nil {
		page.Items = []store.WSMessage{}
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) handleWSMessage(w http.ResponseWriter, r *http.Request) {
	repository, ok := s.wsRepository(w, r)
	if !ok {
		return
	}
	id, err := wsID(r.PathValue("id"), false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	messageID, err := wsID(r.PathValue("messageId"), false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	message, err := repository.GetWSMessage(r.Context(), id, messageID)
	if err != nil {
		wsReadError(w, err)
		return
	}
	if message == nil || message.ConnectionID != id {
		wsReadError(w, sql.ErrNoRows)
		return
	}
	payload, format := hex.EncodeToString(message.Payload), "hex"
	if message.Type == "text" && (message.Encoding == "" || message.Encoding == "identity" || message.Encoding == "utf-8") && utf8.Valid(message.Payload) {
		payload, format = string(message.Payload), "text"
	}
	writeJSON(w, http.StatusOK, struct {
		*store.WSMessage
		Payload       string `json:"payload"`
		PayloadFormat string `json:"payloadFormat"`
	}{message, payload, format})
}
