package api

import (
	"net/http"

	"github.com/lutzifer/burpsuite-clone/internal/certs"
	"github.com/lutzifer/burpsuite-clone/internal/events"
	"github.com/lutzifer/burpsuite-clone/internal/repeater"
	"github.com/lutzifer/burpsuite-clone/internal/store"
)

type Config struct {
	Store     store.Store
	Authority *certs.Authority
	Events    *events.Hub
	Repeater  *repeater.Service
	APIAddr   string
	ProxyAddr string
}

type Server struct {
	cfg Config
	mux *http.ServeMux
}

func NewServer(cfg Config) *Server {
	if cfg.Events == nil {
		cfg.Events = events.NewHub()
	}
	srv := &Server{cfg: cfg, mux: http.NewServeMux()}
	srv.mux.HandleFunc("GET /api/status", srv.handleStatus)
	srv.mux.HandleFunc("GET /api/ca.pem", srv.handleCADownload)
	srv.mux.HandleFunc("GET /api/history", srv.handleHistory)
	srv.mux.HandleFunc("GET /api/history/{id}", srv.handleHistoryDetail)
	srv.mux.HandleFunc("GET /api/events", srv.handleEvents)
	srv.mux.HandleFunc("POST /api/repeater/sessions/{id}/send", srv.handleRepeaterSend)
	return srv
}

func (s *Server) Handler() http.Handler {
	return s.mux
}
