package api

import (
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"

	"github.com/lutzifer/burpsuite-clone/internal/intercept"
	"golang.org/x/net/http/httpguts"
)

type responseEditDTO struct {
	StatusCode int                 `json:"statusCode"`
	Headers    map[string][]string `json:"headers"`
	Body       string              `json:"body"`
}

func (s *Server) handleResponseQueue(w http.ResponseWriter, _ *http.Request) {
	dtos := []interceptItemDTO{}
	if s.cfg.Intercept != nil {
		items := s.cfg.Intercept.ResponseQueue().List()
		sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
		for _, item := range items {
			body := ""
			if item.BodyEditable {
				body = string(item.Body)
			}
			dtos = append(dtos, interceptItemDTO{ID: item.ID, Phase: "response", StatusCode: item.StatusCode,
				Method: item.Method, URL: item.URL, Headers: item.Headers, Body: body,
				BodyEditable: item.BodyEditable, BodyTruncated: item.BodyTruncated})
		}
	}
	writeJSON(w, http.StatusOK, dtos)
}

func (s *Server) handleResponseForward(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Intercept == nil {
		http.Error(w, "intercept unavailable", http.StatusServiceUnavailable)
		return
	}
	queue := s.cfg.Intercept.ResponseQueue()
	item, ok := queue.Get(r.PathValue("id"))
	if !ok {
		http.Error(w, "response item not found", http.StatusNotFound)
		return
	}
	var edit responseEditDTO
	if err := s.decodeEditJSON(w, r, &edit, &edit.Body); err != nil {
		return
	}
	if err := validateResponseEdit(item, edit); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var body []byte
	if item.BodyEditable {
		body = []byte(edit.Body)
	}
	if err := queue.Forward(item.ID, intercept.RequestEdit{StatusCode: edit.StatusCode,
		Headers: edit.Headers, Body: body, BodySet: item.BodyEditable}); err != nil {
		http.Error(w, "response item not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleResponseDrop(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Intercept == nil {
		http.Error(w, "intercept unavailable", http.StatusServiceUnavailable)
		return
	}
	var empty struct{}
	if err := s.decodeJSON(w, r, &empty); err != nil {
		return
	}
	if err := s.cfg.Intercept.ResponseQueue().Drop(r.PathValue("id")); err != nil {
		http.Error(w, "response item not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validateResponseEdit(item intercept.Item, edit responseEditDTO) error {
	if edit.StatusCode < 200 || edit.StatusCode > 599 {
		return fmt.Errorf("statusCode must be between 200 and 599")
	}
	if !item.BodyEditable && edit.Body != "" {
		return fmt.Errorf("response body is not editable")
	}
	bodyless := func(status int) bool { return status == 204 || status == 205 || status == 304 }
	if (item.Method == http.MethodHead || bodyless(edit.StatusCode)) && edit.Body != "" {
		return fmt.Errorf("HEAD and bodyless responses cannot contain a body")
	}
	if !item.BodyEditable && !bodyless(item.StatusCode) && bodyless(edit.StatusCode) && item.Method != http.MethodHead {
		return fmt.Errorf("cannot discard an uneditable response body by changing status")
	}
	if edit.Headers == nil {
		return nil
	} // Omission preserves original headers.
	normalized := http.Header{}
	for name, values := range edit.Headers {
		if !httpguts.ValidHeaderFieldName(name) {
			return fmt.Errorf("invalid header name")
		}
		canonical := http.CanonicalHeaderKey(name)
		if _, exists := normalized[canonical]; exists {
			return fmt.Errorf("duplicate header name")
		}
		for _, value := range values {
			if !httpguts.ValidHeaderFieldValue(value) {
				return fmt.Errorf("invalid header value")
			}
		}
		normalized[canonical] = values
	}
	// Framing is recalculated by the engine, not controlled by manual input.
	// Encoding must describe the untouched bytes, including bypassed bodies.
	for _, name := range []string{"Host", "Content-Length", "Transfer-Encoding", "Content-Encoding", "Connection", "Trailer", "Te", "Upgrade", "Keep-Alive", "Proxy-Connection", "Proxy-Authenticate", "Proxy-Authorization"} {
		var original []string
		for key, values := range item.Headers {
			if strings.EqualFold(key, name) {
				original = append(original, values...)
			}
		}
		if !reflect.DeepEqual(normalized.Values(name), original) {
			return fmt.Errorf("%s is managed by the proxy and cannot be edited", name)
		}
	}
	return nil
}
