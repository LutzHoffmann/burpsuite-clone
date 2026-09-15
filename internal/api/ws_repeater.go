package api

import (
	"bytes"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/lutzifer/burpsuite-clone/internal/wsrepeater"
)

func (s *Server) handleWSRepeaterDraft(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.wsRepository(w, r)
	if !ok {
		return
	}
	id, err := wsID(r.PathValue("id"), false)
	if err != nil {
		http.Error(w, "invalid connection ID", 400)
		return
	}
	messageID, err := wsID(r.PathValue("messageId"), false)
	if err != nil {
		http.Error(w, "invalid message ID", 400)
		return
	}
	connection, err := repo.GetWSConnection(r.Context(), id)
	if err != nil {
		wsReadError(w, err)
		return
	}
	if connection == nil {
		wsReadError(w, sql.ErrNoRows)
		return
	}
	message, err := repo.GetWSMessage(r.Context(), id, messageID)
	if err != nil {
		wsReadError(w, err)
		return
	}
	if message == nil || message.ConnectionID != id {
		wsReadError(w, sql.ErrNoRows)
		return
	}
	if message.Direction != "client-to-server" || !message.Complete || message.Truncated ||
		(message.Encoding != "identity" && message.Encoding != "utf-8") ||
		(message.Type != "text" && message.Type != "binary") || message.Size != int64(len(message.Payload)) {
		http.Error(w, "capture cannot be used as a send draft", http.StatusUnprocessableEntity)
		return
	}
	draft := wsrepeater.Request{URL: connection.URL, Type: message.Type, PayloadFormat: "text", Payload: string(message.Payload), Headers: map[string][]string{}, Subprotocols: []string{}}
	if message.Type == "binary" {
		draft.PayloadFormat = "hex"
		draft.Payload = hex.EncodeToString(message.Payload)
	}
	if err := wsrepeater.Validate(draft, min(s.cfg.MaxBodyBytes, 1<<20)); err != nil {
		http.Error(w, "capture is not a valid bounded message draft", http.StatusUnprocessableEntity)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, draft)
}

func (s *Server) handleWSRepeaterSend(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.cfg.WSRepeater == nil {
		http.Error(w, "WebSocket repeater unavailable", 503)
		return
	}
	// encoding/json replaces malformed UTF-8 and lone surrogates. Reject them
	// before decoding so an operator's payload is never silently changed.
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 3<<20))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "JSON body too large", 413)
		} else {
			http.Error(w, "read JSON body", 400)
		}
		return
	}
	if !validWSJSONUnicode(raw) {
		http.Error(w, "invalid JSON Unicode", 400)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	var request *wsrepeater.Request
	if err := decodeJSONLimit(w, r, &request, 3<<20); err != nil {
		return
	}
	if request == nil {
		http.Error(w, "invalid JSON body", 400)
		return
	}
	result, err := s.cfg.WSRepeater.Send(r.Context(), *request)
	if err != nil {
		switch {
		case errors.Is(err, wsrepeater.ErrInvalid):
			http.Error(w, "invalid WebSocket send request", 400)
		case errors.Is(err, wsrepeater.ErrOutOfScope):
			http.Error(w, "target is outside current scope", 403)
		case errors.Is(err, wsrepeater.ErrBusy):
			http.Error(w, "WebSocket repeater is busy", 429)
		default:
			http.Error(w, "WebSocket repeater unavailable", 503)
		}
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func validWSJSONUnicode(raw []byte) bool {
	if !utf8.Valid(raw) {
		return false
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		n, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n < 0xd800 || n > 0xdbff {
			continue
		}
		if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
			return false
		}
		low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return false
		}
		i += 6
	}
	return true
}
