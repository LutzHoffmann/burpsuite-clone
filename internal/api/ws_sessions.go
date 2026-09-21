package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/lutzifer/burpsuite-clone/internal/wsrepeater"
)

type WSSessionService interface {
	Connect(context.Context, wsrepeater.ConnectRequest) (wsrepeater.SessionSnapshot, error)
	Send(context.Context, string, wsrepeater.SessionSendRequest) (wsrepeater.SessionSnapshot, error)
	Poll(string, uint64) (wsrepeater.SessionSnapshot, error)
	CloseSession(string) (wsrepeater.SessionSnapshot, error)
	Dispose(string) error
}

func (s *Server) wsSessionReady(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "no-store")
	if !sameOrigin(r) {
		http.Error(w, "untrusted Origin header", http.StatusForbidden)
		return false
	}
	if s.cfg.WSSessions == nil {
		http.Error(w, "WebSocket sessions unavailable", http.StatusServiceUnavailable)
		return false
	}
	return true
}

func wsSessionID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if len(id) != 32 {
		http.Error(w, "invalid session ID", 400)
		return "", false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			http.Error(w, "invalid session ID", 400)
			return "", false
		}
	}
	return id, true
}

func wsSessionQuery(w http.ResponseWriter, r *http.Request, poll bool) (uint64, bool) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q) > 1 || (!poll && len(q) != 0) {
		http.Error(w, "invalid session query", 400)
		return 0, false
	}
	if len(q) == 0 {
		return 0, true
	}
	values, ok := q["afterSequence"]
	if !ok || len(values) != 1 || values[0] == "" {
		http.Error(w, "invalid sequence cursor", 400)
		return 0, false
	}
	for _, c := range values[0] {
		if c < '0' || c > '9' {
			http.Error(w, "invalid sequence cursor", 400)
			return 0, false
		}
	}
	cursor, err := strconv.ParseUint(values[0], 10, 64)
	if err != nil {
		http.Error(w, "invalid sequence cursor", 400)
		return 0, false
	}
	return cursor, true
}

func decodeWSSessionJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 3<<20))
	if err != nil {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			http.Error(w, "JSON body too large", 413)
		} else {
			http.Error(w, "read JSON body", 400)
		}
		return false
	}
	if !validWSJSONUnicode(raw) || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || !uniqueJSONFields(raw) {
		http.Error(w, "invalid JSON body", 400)
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	return decodeJSONLimit(w, r, destination, 3<<20) == nil
}

// Reject duplicate keys, including nested headers, rather than silently changing
// the operator's request using encoding/json's last-value-wins behavior.
func uniqueJSONFields(raw []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var value func(int) bool
	value = func(depth int) bool {
		if depth > 16 {
			return false
		}
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return true
		}
		switch delim {
		case '{':
			keys := make(map[string]bool)
			for decoder.More() {
				token, err := decoder.Token()
				key, ok := token.(string)
				if err != nil || !ok || keys[key] {
					return false
				}
				keys[key] = true
				if !value(depth + 1) {
					return false
				}
			}
		case '[':
			for decoder.More() {
				if !value(depth + 1) {
					return false
				}
			}
		default:
			return false
		}
		_, err = decoder.Token()
		return err == nil
	}
	if !value(0) {
		return false
	}
	_, err := decoder.Token()
	return errors.Is(err, io.EOF)
}

func emptyWSSessionBody(w http.ResponseWriter, r *http.Request) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1))
	if err != nil || len(body) != 0 {
		http.Error(w, "request body must be empty", 400)
		return false
	}
	return true
}

func writeWSSession(w http.ResponseWriter, result wsrepeater.SessionSnapshot, err error) {
	if err != nil {
		code, message := 503, "WebSocket sessions unavailable"
		switch {
		case errors.Is(err, wsrepeater.ErrInvalid):
			code, message = 400, "invalid session request"
		case errors.Is(err, wsrepeater.ErrOutOfScope):
			code, message = 403, "target is outside current scope"
		case errors.Is(err, wsrepeater.ErrBusy):
			code, message = 429, "WebSocket session capacity reached"
		case errors.Is(err, wsrepeater.ErrSessionNotFound):
			code, message = 404, "session not found"
		case errors.Is(err, wsrepeater.ErrSessionConflict):
			code, message = 409, "session is busy or closed"
		}
		http.Error(w, message, code)
		return
	}
	// A separate response budget admits worst-case JSON escaping of a 1 MiB log.
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > 8<<20 {
		http.Error(w, "session response exceeds limit", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)
}

func (s *Server) handleWSSessionConnect(w http.ResponseWriter, r *http.Request) {
	if !s.wsSessionReady(w, r) {
		return
	}
	if _, ok := wsSessionQuery(w, r, false); !ok {
		return
	}
	var request wsrepeater.ConnectRequest
	if !decodeWSSessionJSON(w, r, &request) {
		return
	}
	result, err := s.cfg.WSSessions.Connect(r.Context(), request)
	writeWSSession(w, result, err)
}

func (s *Server) handleWSSessionSend(w http.ResponseWriter, r *http.Request) {
	if !s.wsSessionReady(w, r) {
		return
	}
	id, ok := wsSessionID(w, r)
	if !ok {
		return
	}
	if _, ok := wsSessionQuery(w, r, false); !ok {
		return
	}
	var request wsrepeater.SessionSendRequest
	if !decodeWSSessionJSON(w, r, &request) {
		return
	}
	result, err := s.cfg.WSSessions.Send(r.Context(), id, request)
	writeWSSession(w, result, err)
}

func (s *Server) handleWSSessionPoll(w http.ResponseWriter, r *http.Request) {
	if !s.wsSessionReady(w, r) {
		return
	}
	id, ok := wsSessionID(w, r)
	if !ok {
		return
	}
	cursor, ok := wsSessionQuery(w, r, true)
	if !ok {
		return
	}
	if !emptyWSSessionBody(w, r) {
		return
	}
	result, err := s.cfg.WSSessions.Poll(id, cursor)
	writeWSSession(w, result, err)
}

func (s *Server) handleWSSessionClose(w http.ResponseWriter, r *http.Request) {
	if !s.wsSessionReady(w, r) {
		return
	}
	id, ok := wsSessionID(w, r)
	if !ok {
		return
	}
	if _, ok := wsSessionQuery(w, r, false); !ok {
		return
	}
	if !emptyWSSessionBody(w, r) {
		return
	}
	result, err := s.cfg.WSSessions.CloseSession(id)
	writeWSSession(w, result, err)
}

func (s *Server) handleWSSessionDispose(w http.ResponseWriter, r *http.Request) {
	if !s.wsSessionReady(w, r) {
		return
	}
	id, ok := wsSessionID(w, r)
	if !ok {
		return
	}
	if _, ok := wsSessionQuery(w, r, false); !ok {
		return
	}
	if !emptyWSSessionBody(w, r) {
		return
	}
	if err := s.cfg.WSSessions.Dispose(id); err != nil {
		writeWSSession(w, wsrepeater.SessionSnapshot{}, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
