package api

import (
	"net/http"
	"os"
	"path/filepath"

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
	srv.mux.HandleFunc("GET /", srv.handleUI)
	return srv
}

func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	if _, err := os.Stat(filepath.Join("web", "dist", "index.html")); err == nil {
		http.FileServer(http.Dir(filepath.Join("web", "dist"))).ServeHTTP(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte("<!doctype html><html><head><title>BurpSuite Clone</title></head><body><h1>BurpSuite Clone</h1><p>The frontend build is unavailable. Run <code>npm run build</code> from <code>web</code>, then restart the proxy.</p></body></html>"))
}
