package api

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/lutzifer/burpsuite-clone/internal/certs"
	"github.com/lutzifer/burpsuite-clone/internal/events"
	"github.com/lutzifer/burpsuite-clone/internal/intercept"
	"github.com/lutzifer/burpsuite-clone/internal/repeater"
	"github.com/lutzifer/burpsuite-clone/internal/store"
)

type Config struct {
	Store        store.Store
	Authority    *certs.Authority
	Events       *events.Hub
	Repeater     *repeater.Service
	APIAddr      string
	ProxyAddr    string
	Intercept    *intercept.Controller
	MaxBodyBytes int64
}

type Server struct {
	cfg Config
	mux *http.ServeMux
}

func NewServer(cfg Config) *Server {
	if cfg.Events == nil {
		cfg.Events = events.NewHub()
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = 1 << 20
	}
	srv := &Server{cfg: cfg, mux: http.NewServeMux()}
	srv.mux.HandleFunc("GET /api/status", srv.handleStatus)
	srv.mux.HandleFunc("GET /api/ca.pem", srv.handleCADownload)
	srv.mux.HandleFunc("GET /api/history", srv.handleHistory)
	srv.mux.HandleFunc("GET /api/history/{id}", srv.handleHistoryDetail)
	srv.mux.HandleFunc("PATCH /api/history/{id}", srv.handleHistoryUpdate)
	srv.mux.HandleFunc("GET /api/events", srv.handleEvents)
	srv.mux.HandleFunc("GET /api/intercept/queue", srv.handleInterceptQueue)
	srv.mux.HandleFunc("GET /api/intercept/config", srv.handleInterceptConfig)
	srv.mux.HandleFunc("PUT /api/intercept/config", srv.handleInterceptConfigUpdate)
	srv.mux.HandleFunc("POST /api/intercept/{id}/forward", srv.handleInterceptForward)
	srv.mux.HandleFunc("POST /api/intercept/{id}/drop", srv.handleInterceptDrop)
	srv.mux.HandleFunc("GET /api/repeater/sessions", srv.handleRepeaterSessions)
	srv.mux.HandleFunc("GET /api/repeater/sessions/{id}/history", srv.handleRepeaterHistory)
	srv.mux.HandleFunc("GET /api/repeater/sessions/{id}/compare", srv.handleRepeaterCompare)
	srv.mux.HandleFunc("POST /api/repeater/sessions/{id}/send", srv.handleRepeaterSend)
	srv.mux.HandleFunc("GET /", srv.handleUI)
	if cfg.Intercept != nil {
		cfg.Intercept.Queue().SetObserver(func(change intercept.Change) {
			eventType := "intercept.item." + change.Type
			cfg.Events.Publish(events.Event{Type: eventType, Data: map[string]interface{}{
				"id": change.Item.ID, "action": change.Action,
			}})
		})
	}
	return srv
}

func (s *Server) Handler() http.Handler {
	return s.validateRequest(s.mux)
}

func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	if _, err := os.Stat(filepath.Join("web", "dist", "index.html")); err == nil {
		http.FileServer(http.Dir(filepath.Join("web", "dist"))).ServeHTTP(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte("<!doctype html><html><head><title>BurpSuite Clone</title></head><body><h1>BurpSuite Clone</h1><p>The frontend build is unavailable. Run <code>npm run build</code> from <code>web</code>, then restart the proxy.</p></body></html>"))
}
