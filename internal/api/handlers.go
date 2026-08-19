package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/gorilla/websocket"
	"github.com/lutzifer/burpsuite-clone/internal/events"
	"github.com/lutzifer/burpsuite-clone/internal/repeater"
	"github.com/lutzifer/burpsuite-clone/internal/store"
)

var websocketUpgrader = websocket.Upgrader{}

type repeaterAPIRequest struct {
	Method  string              `json:"method"`
	URL     string              `json:"url"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body"`
}

type repeaterAPIResponse struct {
	Status      int                 `json:"status"`
	Headers     map[string][]string `json:"headers"`
	Body        string              `json:"body"`
	DurationMS  int64               `json:"durationMs"`
	Size        int64               `json:"size"`
	Truncated   bool                `json:"truncated"`
	ContentType string              `json:"contentType"`
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	caFingerprint := ""
	caTrust := "unavailable"
	httpsInterception := false
	if s.cfg.Authority != nil {
		caFingerprint = s.cfg.Authority.FingerprintSHA256()
		caTrust = "manual"
		httpsInterception = true
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"apiAddr":           s.cfg.APIAddr,
		"proxyAddr":         s.cfg.ProxyAddr,
		"caFingerprint":     caFingerprint,
		"caTrust":           caTrust,
		"httpsInterception": httpsInterception,
	})
}

func (s *Server) handleCADownload(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", "attachment; filename=intercept-ca.pem")
	_, _ = w.Write(s.cfg.Authority.CACertPEM())
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Store == nil {
		http.Error(w, "history store unavailable", http.StatusServiceUnavailable)
		return
	}
	history, err := s.cfg.Store.ListHistory(r.Context(), store.HistoryFilter{
		Search: r.URL.Query().Get("search"),
		Method: r.URL.Query().Get("method"),
		Host:   r.URL.Query().Get("host"),
	})
	if err != nil {
		http.Error(w, "list history", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, history)
}

func (s *Server) handleHistoryDetail(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Store == nil {
		http.Error(w, "history store unavailable", http.StatusServiceUnavailable)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.Error(w, "invalid history id", http.StatusBadRequest)
		return
	}
	exchange, err := s.cfg.Store.GetExchange(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "history item not found", http.StatusNotFound)
			return
		}
		http.Error(w, "get history item", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, exchange)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	connection, err := websocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer connection.Close()

	subscriber, unsubscribe := s.cfg.Events.Subscribe()
	defer unsubscribe()
	for event := range subscriber {
		if err := connection.WriteJSON(event); err != nil {
			return
		}
	}
}

func (s *Server) handleRepeaterSend(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Repeater == nil {
		http.Error(w, "repeater unavailable", http.StatusServiceUnavailable)
		return
	}

	var request repeaterAPIRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid repeater request", http.StatusBadRequest)
		return
	}
	result, err := s.cfg.Repeater.Send(r.Context(), repeater.SendRequest{
		Method:  request.Method,
		URL:     request.URL,
		Headers: request.Headers,
		Body:    []byte(request.Body),
	})
	if err != nil {
		http.Error(w, "send repeater request", http.StatusBadGateway)
		return
	}

	if s.cfg.Events != nil {
		s.cfg.Events.Publish(events.Event{
			Type: "repeater.send.completed",
			Data: map[string]interface{}{
				"sessionId": r.PathValue("id"),
				"status":    result.Status,
			},
		})
	}
	writeJSON(w, http.StatusOK, repeaterAPIResponse{
		Status:      result.Status,
		Headers:     result.Headers,
		Body:        string(result.Body),
		DurationMS:  result.DurationMS,
		Size:        result.Size,
		Truncated:   result.Truncated,
		ContentType: result.ContentType,
	})
}

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
