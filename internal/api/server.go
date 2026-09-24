package api

import (
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/lutzifer/burpsuite-clone/internal/certs"
	"github.com/lutzifer/burpsuite-clone/internal/events"
	"github.com/lutzifer/burpsuite-clone/internal/intercept"
	"github.com/lutzifer/burpsuite-clone/internal/repeater"
	"github.com/lutzifer/burpsuite-clone/internal/store"
	"github.com/lutzifer/burpsuite-clone/internal/target"
	"github.com/lutzifer/burpsuite-clone/internal/wsrepeater"
)

type Config struct {
	Store         store.Store
	Authority     *certs.Authority
	Events        *events.Hub
	Repeater      *repeater.Service
	WSRepeater    *wsrepeater.Service
	WSSessions    WSSessionService
	APIAddr       string
	ProxyAddr     string
	Intercept     *intercept.Controller
	Target        *target.Service
	Intruder      IntruderService
	ActiveScanner ActiveScanner
	MaxBodyBytes  int64
}

type Server struct {
	cfg               Config
	mux               *http.ServeMux
	interceptConfigMu sync.Mutex
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
	srv.mux.HandleFunc("GET /api/history/page", srv.handleHistoryPage)
	srv.mux.HandleFunc("GET /api/findings", srv.handleFindings)
	srv.mux.HandleFunc("POST /api/active-scan", srv.handleActiveScan)
	srv.mux.HandleFunc("GET /api/active-scan/runs", srv.handleActiveScanRuns)
	srv.mux.HandleFunc("GET /api/active-scan/runs/{id}", srv.handleActiveScanRun)
	srv.mux.HandleFunc("DELETE /api/active-scan/runs/{id}", srv.handleActiveScanRunDelete)
	srv.mux.HandleFunc("GET /api/websockets", srv.handleWSConnections)
	srv.mux.HandleFunc("GET /api/websockets/{id}", srv.handleWSConnection)
	srv.mux.HandleFunc("GET /api/websockets/{id}/messages", srv.handleWSMessages)
	srv.mux.HandleFunc("GET /api/websockets/{id}/messages/{messageId}", srv.handleWSMessage)
	srv.mux.HandleFunc("GET /api/websockets/{id}/messages/{messageId}/draft", srv.handleWSRepeaterDraft)
	srv.mux.HandleFunc("POST /api/websocket-repeater/send", srv.handleWSRepeaterSend)
	srv.mux.HandleFunc("POST /api/websocket-repeater/sessions", srv.handleWSSessionConnect)
	srv.mux.HandleFunc("GET /api/websocket-repeater/sessions/{id}", srv.handleWSSessionPoll)
	srv.mux.HandleFunc("POST /api/websocket-repeater/sessions/{id}/send", srv.handleWSSessionSend)
	srv.mux.HandleFunc("POST /api/websocket-repeater/sessions/{id}/close", srv.handleWSSessionClose)
	srv.mux.HandleFunc("DELETE /api/websocket-repeater/sessions/{id}", srv.handleWSSessionDispose)
	srv.mux.HandleFunc("GET /api/storage", srv.handleStorage)
	srv.mux.HandleFunc("PUT /api/storage", srv.handleStorageUpdate)
	srv.mux.HandleFunc("GET /api/history/{id}", srv.handleHistoryDetail)
	srv.mux.HandleFunc("PATCH /api/history/{id}", srv.handleHistoryUpdate)
	srv.mux.HandleFunc("GET /api/events", srv.handleEvents)
	srv.mux.HandleFunc("GET /api/intercept/queue", srv.handleInterceptQueue)
	srv.mux.HandleFunc("GET /api/intercept/config", srv.handleInterceptConfig)
	srv.mux.HandleFunc("PUT /api/intercept/config", srv.handleInterceptConfigUpdate)
	srv.mux.HandleFunc("POST /api/intercept/{id}/forward", srv.handleInterceptForward)
	srv.mux.HandleFunc("POST /api/intercept/{id}/drop", srv.handleInterceptDrop)
	srv.mux.HandleFunc("GET /api/intercept/response-queue", srv.handleResponseQueue)
	srv.mux.HandleFunc("POST /api/intercept/response/{id}/forward", srv.handleResponseForward)
	srv.mux.HandleFunc("POST /api/intercept/response/{id}/drop", srv.handleResponseDrop)
	srv.mux.HandleFunc("GET /api/repeater/sessions", srv.handleRepeaterSessions)
	srv.mux.HandleFunc("GET /api/repeater/sessions/{id}/history", srv.handleRepeaterHistory)
	srv.mux.HandleFunc("GET /api/repeater/sessions/{id}/compare", srv.handleRepeaterCompare)
	srv.mux.HandleFunc("POST /api/repeater/sessions/{id}/send", srv.handleRepeaterSend)
	srv.mux.HandleFunc("GET /api/scope/rules", srv.handleScopeRules)
	srv.mux.HandleFunc("PUT /api/scope/rules", srv.handleScopeRulesUpdate)
	srv.mux.HandleFunc("GET /api/target/tree", srv.handleTargetTree)
	srv.mux.HandleFunc("GET /api/target/endpoints/{id}", srv.handleTargetEndpoint)
	srv.mux.HandleFunc("GET /api/target/endpoints/{id}/requests", srv.handleTargetRequests)
	srv.mux.HandleFunc("GET /api/target/endpoints/{id}/parameters", srv.handleTargetParameters)
	srv.mux.HandleFunc("GET /api/target/rebuild", srv.handleTargetRebuild)
	srv.mux.HandleFunc("POST /api/intruder/jobs", srv.handleIntruderCreate)
	srv.mux.HandleFunc("POST /api/intruder/preview", srv.handleIntruderPreview)
	srv.mux.HandleFunc("POST /api/intruder/jobs/{id}/baseline", srv.handleIntruderBaseline)
	srv.mux.HandleFunc("GET /api/intruder/jobs", srv.handleIntruderList)
	srv.mux.HandleFunc("GET /api/intruder/jobs/{id}", srv.handleIntruderGet)
	srv.mux.HandleFunc("PUT /api/intruder/jobs/{id}", srv.handleIntruderUpdate)
	srv.mux.HandleFunc("DELETE /api/intruder/jobs/{id}", srv.handleIntruderDelete)
	srv.mux.HandleFunc("POST /api/intruder/jobs/{id}/start", srv.handleIntruderStart)
	srv.mux.HandleFunc("POST /api/intruder/jobs/{id}/pause", srv.handleIntruderPause)
	srv.mux.HandleFunc("POST /api/intruder/jobs/{id}/resume", srv.handleIntruderResume)
	srv.mux.HandleFunc("POST /api/intruder/jobs/{id}/abort", srv.handleIntruderAbort)
	srv.mux.HandleFunc("GET /api/intruder/jobs/{id}/results", srv.handleIntruderResults)
	srv.mux.HandleFunc("GET /api/intruder/jobs/{id}/results/{sequence}", srv.handleIntruderResult)
	srv.mux.HandleFunc("POST /api/target/rebuild", srv.handleTargetRebuildRetry)
	srv.mux.HandleFunc("GET /", srv.handleUI)
	if cfg.Intercept != nil {
		cfg.Intercept.Queue().SetObserver(func(change intercept.Change) {
			eventType := "intercept.item." + change.Type
			cfg.Events.Publish(events.Event{Type: eventType, Data: map[string]interface{}{
				"id": change.Item.ID, "action": change.Action,
			}})
		})
		// Response lifecycle events are intercept.response.queued and
		// intercept.response.completed; request events retain intercept.item.*.
		cfg.Intercept.ResponseQueue().SetObserver(func(change intercept.Change) {
			cfg.Events.Publish(events.Event{Type: "intercept.response." + change.Type, Data: map[string]interface{}{
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
