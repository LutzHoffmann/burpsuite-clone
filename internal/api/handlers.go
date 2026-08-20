package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
	"github.com/lutzifer/burpsuite-clone/internal/events"
	"github.com/lutzifer/burpsuite-clone/internal/intercept"
	"github.com/lutzifer/burpsuite-clone/internal/repeater"
	"github.com/lutzifer/burpsuite-clone/internal/store"
)

var websocketUpgrader = websocket.Upgrader{}

type messageDTO struct {
	Headers   map[string][]string `json:"headers"`
	Body      string              `json:"body"`
	Raw       string              `json:"raw"`
	TextSafe  bool                `json:"textSafe"`
	Truncated bool                `json:"truncated"`
}

type exchangeDTO struct {
	ID                int64      `json:"id"`
	Method            string     `json:"method"`
	Scheme            string     `json:"scheme"`
	Host              string     `json:"host"`
	Path              string     `json:"path"`
	Query             string     `json:"query"`
	Status            int        `json:"status"`
	MIMEType          string     `json:"mimeType"`
	RequestSize       int64      `json:"requestSize"`
	ResponseSize      int64      `json:"responseSize"`
	DurationMS        int64      `json:"durationMs"`
	StartedAt         time.Time  `json:"startedAt"`
	Intercepted       bool       `json:"intercepted"`
	Error             bool       `json:"error"`
	ErrorMessage      string     `json:"errorMessage"`
	RequestTruncated  bool       `json:"requestTruncated"`
	ResponseTruncated bool       `json:"responseTruncated"`
	InScope           bool       `json:"inScope"`
	ScopeVersion      int64      `json:"scopeVersion"`
	ScopeRuleID       *int64     `json:"scopeRuleId"`
	Request           messageDTO `json:"request"`
	Response          messageDTO `json:"response"`
	Tags              []string   `json:"tags"`
	Note              string     `json:"note"`
}

type historyItemDTO struct {
	ID           int64     `json:"id"`
	Method       string    `json:"method"`
	Scheme       string    `json:"scheme"`
	Host         string    `json:"host"`
	Path         string    `json:"path"`
	Query        string    `json:"query"`
	Status       int       `json:"status"`
	MIMEType     string    `json:"mimeType"`
	RequestSize  int64     `json:"requestSize"`
	ResponseSize int64     `json:"responseSize"`
	DurationMS   int64     `json:"durationMs"`
	StartedAt    time.Time `json:"startedAt"`
	Intercepted  bool      `json:"intercepted"`
	Error        bool      `json:"error"`
	InScope      bool      `json:"inScope"`
	ScopeVersion int64     `json:"scopeVersion"`
	ScopeRuleID  *int64    `json:"scopeRuleId"`
}

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
	TextSafe    bool                `json:"textSafe"`
}

type interceptItemDTO struct {
	ID            string              `json:"id"`
	Method        string              `json:"method"`
	URL           string              `json:"url"`
	Headers       map[string][]string `json:"headers"`
	Body          string              `json:"body"`
	BodyEditable  bool                `json:"bodyEditable"`
	BodyTruncated bool                `json:"bodyTruncated"`
}

type historyUpdateRequest struct {
	Tags []string `json:"tags"`
	Note string   `json:"note"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	caFingerprint := ""
	caTrust := "unavailable"
	httpsInterception := false
	if s.cfg.Authority != nil {
		caFingerprint = s.cfg.Authority.FingerprintSHA256()
		caTrust = "manual"
		httpsInterception = true
	}
	var activeProject interface{}
	if projectStore, ok := s.cfg.Store.(store.ProjectStore); ok {
		if project, err := projectStore.ActiveProject(r.Context()); err == nil {
			activeProject = project
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"apiAddr": s.cfg.APIAddr, "proxyAddr": s.cfg.ProxyAddr,
		"caFingerprint": caFingerprint, "caTrust": caTrust,
		"httpsInterception": httpsInterception, "activeProject": activeProject,
	})
}

func (s *Server) handleCADownload(w http.ResponseWriter, _ *http.Request) {
	if s.cfg.Authority == nil {
		http.Error(w, "certificate authority unavailable", http.StatusServiceUnavailable)
		return
	}
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
		Search: r.URL.Query().Get("search"), Method: r.URL.Query().Get("method"), Host: r.URL.Query().Get("host"),
	})
	if err != nil {
		http.Error(w, "list history", http.StatusInternalServerError)
		return
	}
	dtos := make([]historyItemDTO, len(history))
	for index, item := range history {
		dtos[index] = toHistoryItemDTO(item)
	}
	writeJSON(w, http.StatusOK, dtos)
}

func (s *Server) handleHistoryDetail(w http.ResponseWriter, r *http.Request) {
	exchange, ok := s.getExchange(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, toExchangeDTO(exchange))
}

func (s *Server) handleHistoryUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := historyID(r)
	if err != nil {
		http.Error(w, "invalid history id", http.StatusBadRequest)
		return
	}
	metadataStore, ok := s.cfg.Store.(store.MetadataStore)
	if !ok {
		http.Error(w, "history metadata unavailable", http.StatusServiceUnavailable)
		return
	}
	var update historyUpdateRequest
	if err := s.decodeJSON(w, r, &update); err != nil {
		return
	}
	if err := metadataStore.UpdateExchangeMetadata(r.Context(), id, update.Tags, update.Note); err != nil {
		http.Error(w, "update history", http.StatusInternalServerError)
		return
	}
	s.cfg.Events.Publish(events.Event{Type: "history.entry.updated", Data: map[string]interface{}{"id": id}})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getExchange(w http.ResponseWriter, r *http.Request) (*store.Exchange, bool) {
	if s.cfg.Store == nil {
		http.Error(w, "history store unavailable", http.StatusServiceUnavailable)
		return nil, false
	}
	id, err := historyID(r)
	if err != nil {
		http.Error(w, "invalid history id", http.StatusBadRequest)
		return nil, false
	}
	exchange, err := s.cfg.Store.GetExchange(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "history item not found", http.StatusNotFound)
		} else {
			http.Error(w, "get history item", http.StatusInternalServerError)
		}
		return nil, false
	}
	return exchange, true
}

func historyID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, errors.New("invalid history id")
	}
	return id, nil
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

func (s *Server) handleInterceptQueue(w http.ResponseWriter, _ *http.Request) {
	if s.cfg.Intercept == nil {
		writeJSON(w, http.StatusOK, []interceptItemDTO{})
		return
	}
	items := s.cfg.Intercept.Queue().List()
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	dtos := make([]interceptItemDTO, 0, len(items))
	for _, item := range items {
		body := ""
		if item.BodyEditable {
			body = string(item.Body)
		}
		dtos = append(dtos, interceptItemDTO{
			ID: item.ID, Method: item.Method, URL: item.URL, Headers: item.Headers,
			Body: body, BodyEditable: item.BodyEditable, BodyTruncated: item.BodyTruncated,
		})
	}
	writeJSON(w, http.StatusOK, dtos)
}

func (s *Server) handleInterceptConfig(w http.ResponseWriter, _ *http.Request) {
	if s.cfg.Intercept == nil {
		writeJSON(w, http.StatusOK, intercept.ControllerState{})
		return
	}
	writeJSON(w, http.StatusOK, s.cfg.Intercept.State())
}

func (s *Server) handleInterceptConfigUpdate(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Intercept == nil {
		http.Error(w, "intercept unavailable", http.StatusServiceUnavailable)
		return
	}
	var state intercept.ControllerState
	if err := s.decodeJSON(w, r, &state); err != nil {
		return
	}
	s.cfg.Intercept.Update(state)
	if projectStore, ok := s.cfg.Store.(store.ProjectStore); ok {
		encoded, _ := json.Marshal(state)
		if err := projectStore.SetSetting(r.Context(), "intercept.config", string(encoded)); err != nil {
			http.Error(w, "persist intercept config", http.StatusInternalServerError)
			return
		}
	}
	s.cfg.Events.Publish(events.Event{Type: "settings.changed", Data: state})
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handleInterceptForward(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Intercept == nil {
		http.Error(w, "intercept unavailable", http.StatusServiceUnavailable)
		return
	}
	item, ok := s.cfg.Intercept.Queue().Get(r.PathValue("id"))
	if !ok {
		http.Error(w, "intercept item not found", http.StatusNotFound)
		return
	}
	var edit interceptItemDTO
	if err := s.decodeJSON(w, r, &edit); err != nil {
		return
	}
	if !item.BodyEditable && edit.Body != "" {
		http.Error(w, "binary request body is not editable", http.StatusBadRequest)
		return
	}
	body := item.Body
	bodySet := false
	if item.BodyEditable {
		body = []byte(edit.Body)
		bodySet = true
	}
	if err := s.cfg.Intercept.Queue().Forward(item.ID, intercept.RequestEdit{
		Method: edit.Method, URL: edit.URL, Headers: edit.Headers, Body: body, BodySet: bodySet,
	}); err != nil {
		http.Error(w, "forward intercept item", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleInterceptDrop(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Intercept == nil {
		http.Error(w, "intercept unavailable", http.StatusServiceUnavailable)
		return
	}
	var empty struct{}
	if err := s.decodeJSON(w, r, &empty); err != nil {
		return
	}
	if err := s.cfg.Intercept.Queue().Drop(r.PathValue("id")); err != nil {
		http.Error(w, "drop intercept item", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRepeaterSend(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Repeater == nil {
		http.Error(w, "repeater unavailable", http.StatusServiceUnavailable)
		return
	}
	var request repeaterAPIRequest
	if err := s.decodeJSON(w, r, &request); err != nil {
		return
	}
	result, err := s.cfg.Repeater.Send(r.Context(), repeater.SendRequest{
		Method: request.Method, URL: request.URL, Headers: request.Headers, Body: []byte(request.Body),
	})
	if err != nil {
		http.Error(w, "send repeater request", http.StatusBadGateway)
		return
	}
	textSafe := textSafeBody(result.ContentType, result.Body)
	responseBody := ""
	if textSafe {
		responseBody = string(result.Body)
	}
	if projectStore, ok := s.cfg.Store.(store.ProjectStore); ok {
		send := &store.RepeaterSend{
			SessionID: r.PathValue("id"), Method: request.Method, URL: request.URL,
			RequestHeaders: request.Headers, RequestBody: request.Body, Status: result.Status,
			ResponseHeaders: result.Headers, ResponseBody: responseBody, DurationMS: result.DurationMS,
			Size: result.Size, Truncated: result.Truncated, ContentType: result.ContentType, SentAt: time.Now().UTC(),
		}
		if err := projectStore.SaveRepeaterSend(r.Context(), send); err != nil {
			http.Error(w, "persist repeater request", http.StatusInternalServerError)
			return
		}
	}
	s.cfg.Events.Publish(events.Event{Type: "repeater.send.completed", Data: map[string]interface{}{
		"sessionId": r.PathValue("id"), "status": result.Status,
	}})
	writeJSON(w, http.StatusOK, repeaterAPIResponse{
		Status: result.Status, Headers: result.Headers, Body: responseBody,
		DurationMS: result.DurationMS, Size: result.Size, Truncated: result.Truncated,
		ContentType: result.ContentType, TextSafe: textSafe,
	})
}

func (s *Server) handleRepeaterSessions(w http.ResponseWriter, r *http.Request) {
	projectStore, ok := s.cfg.Store.(store.ProjectStore)
	if !ok {
		writeJSON(w, http.StatusOK, []store.RepeaterSession{})
		return
	}
	sessions, err := projectStore.ListRepeaterSessions(r.Context())
	if err != nil {
		http.Error(w, "list repeater sessions", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, sessions)
}

func (s *Server) handleRepeaterHistory(w http.ResponseWriter, r *http.Request) {
	projectStore, ok := s.cfg.Store.(store.ProjectStore)
	if !ok {
		writeJSON(w, http.StatusOK, []store.RepeaterSend{})
		return
	}
	sends, err := projectStore.ListRepeaterSends(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, "list repeater history", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, sends)
}

func (s *Server) handleRepeaterCompare(w http.ResponseWriter, r *http.Request) {
	projectStore, ok := s.cfg.Store.(store.ProjectStore)
	if !ok {
		http.Error(w, "repeater history unavailable", http.StatusServiceUnavailable)
		return
	}
	leftID, leftErr := strconv.ParseInt(r.URL.Query().Get("left"), 10, 64)
	rightID, rightErr := strconv.ParseInt(r.URL.Query().Get("right"), 10, 64)
	if leftErr != nil || rightErr != nil {
		http.Error(w, "invalid comparison ids", http.StatusBadRequest)
		return
	}
	sends, err := projectStore.ListRepeaterSends(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, "list repeater history", http.StatusInternalServerError)
		return
	}
	var left, right *store.RepeaterSend
	for index := range sends {
		if sends[index].ID == leftID {
			left = &sends[index]
		}
		if sends[index].ID == rightID {
			right = &sends[index]
		}
	}
	if left == nil || right == nil {
		http.Error(w, "comparison send not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"leftId": left.ID, "rightId": right.ID, "statusEqual": left.Status == right.Status,
		"bodyEqual": left.ResponseBody == right.ResponseBody,
		"sizeDelta": right.Size - left.Size, "durationDeltaMs": right.DurationMS - left.DurationMS,
	})
}

func toExchangeDTO(exchange *store.Exchange) exchangeDTO {
	return exchangeDTO{
		ID: exchange.ID, Method: exchange.Method, Scheme: exchange.Scheme, Host: exchange.Host,
		Path: exchange.Path, Query: exchange.Query, Status: exchange.Status, MIMEType: exchange.MIMEType,
		RequestSize: exchange.RequestSize, ResponseSize: exchange.ResponseSize,
		DurationMS: exchange.Duration.Milliseconds(), StartedAt: exchange.StartedAt,
		Intercepted: exchange.Intercepted, Error: exchange.Error, ErrorMessage: exchange.ErrorMessage,
		RequestTruncated: exchange.RequestTruncated, ResponseTruncated: exchange.ResponseTruncated,
		InScope: exchange.InScope, ScopeVersion: exchange.ScopeVersion, ScopeRuleID: exchange.ScopeRuleID,
		Request:  toMessageDTO(exchange.Request.Headers, exchange.Request.Body, exchange.Request.Raw, exchange.RequestTruncated),
		Response: toMessageDTO(exchange.Response.Headers, exchange.Response.Body, exchange.Response.Raw, exchange.ResponseTruncated),
		Tags:     exchange.Tags, Note: exchange.Note,
	}
}

func toHistoryItemDTO(item store.HistoryItem) historyItemDTO {
	return historyItemDTO{
		ID: item.ID, Method: item.Method, Scheme: item.Scheme, Host: item.Host, Path: item.Path, Query: item.Query,
		Status: item.Status, MIMEType: item.MIMEType, RequestSize: item.RequestSize, ResponseSize: item.ResponseSize,
		DurationMS: item.DurationMS, StartedAt: item.StartedAt, Intercepted: item.Intercepted, Error: item.Error,
		InScope: item.InScope, ScopeVersion: item.ScopeVersion, ScopeRuleID: item.ScopeRuleID,
	}
}

func toMessageDTO(headers map[string][]string, body, raw []byte, truncated bool) messageDTO {
	contentType := http.Header(headers).Get("Content-Type")
	textSafe := textSafeBody(contentType, body)
	bodyText := ""
	if textSafe {
		bodyText = string(body)
	}
	rawText := ""
	if utf8.Valid(raw) {
		rawText = string(raw)
	}
	return messageDTO{Headers: headers, Body: bodyText, Raw: rawText, TextSafe: textSafe, Truncated: truncated}
}

func textSafeBody(contentType string, body []byte) bool {
	if len(body) == 0 {
		return true
	}
	if !utf8.Valid(body) {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return strings.HasPrefix(mediaType, "text/") || strings.Contains(mediaType, "json") ||
		strings.Contains(mediaType, "xml") || mediaType == "application/x-www-form-urlencoded"
}

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
