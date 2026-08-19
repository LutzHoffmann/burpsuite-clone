package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/gorilla/websocket"
	"github.com/lutzifer/burpsuite-clone/internal/store"
)

var websocketUpgrader = websocket.Upgrader{}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"apiAddr":           s.cfg.APIAddr,
		"proxyAddr":         s.cfg.ProxyAddr,
		"caFingerprint":     s.cfg.Authority.FingerprintSHA256(),
		"httpsInterception": true,
	})
}

func (s *Server) handleCADownload(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/x-pem-file")
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

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
