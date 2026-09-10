package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
)

func (s *Server) validateRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.allowedHost(r.Host) {
			http.Error(w, "untrusted Host header", http.StatusMisdirectedRequest)
			return
		}
		if requiresOriginCheck(r) && !sameOrigin(r) {
			http.Error(w, "untrusted Origin header", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) allowedHost(hostport string) bool {
	host := hostname(hostport)
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	configured := hostname(s.cfg.APIAddr)
	return configured != "" && configured != "0.0.0.0" && configured != "::" && strings.EqualFold(host, configured)
}

func hostname(hostport string) string {
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(hostport, "[]")
}

func requiresOriginCheck(r *http.Request) bool {
	return r.URL.Path == "/api/events" || r.Method == http.MethodPost || r.Method == http.MethodPut ||
		r.Method == http.MethodPatch || r.Method == http.MethodDelete
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil ||
		parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}

	requestScheme := "http"
	if r.TLS != nil {
		requestScheme = "https"
	}
	requestURL, err := url.Parse("//" + r.Host)
	if err != nil || requestURL.Hostname() == "" {
		return false
	}
	requestURL.Scheme = requestScheme

	return strings.EqualFold(parsed.Scheme, requestScheme) &&
		strings.EqualFold(parsed.Hostname(), requestURL.Hostname()) &&
		effectivePort(parsed) == effectivePort(requestURL)
}

func effectivePort(value *url.URL) string {
	if port := value.Port(); port != "" {
		return port
	}
	if strings.EqualFold(value.Scheme, "http") {
		return "80"
	}
	if strings.EqualFold(value.Scheme, "https") {
		return "443"
	}
	return ""
}

func (s *Server) decodeJSON(w http.ResponseWriter, r *http.Request, destination interface{}) error {
	return decodeJSONLimit(w, r, destination, s.cfg.MaxBodyBytes)
}

func (s *Server) decodeEditJSON(w http.ResponseWriter, r *http.Request, destination interface{}, body *string) error {
	// A raw byte can occupy six JSON bytes (\u00XX), plus bounded edit metadata.
	const metadataBytes int64 = 1 << 20
	limit := int64(math.MaxInt64)
	if s.cfg.MaxBodyBytes <= (math.MaxInt64-metadataBytes)/6 {
		limit = 6*s.cfg.MaxBodyBytes + metadataBytes
	}
	if err := decodeJSONLimit(w, r, destination, limit); err != nil {
		return err
	}
	if int64(len(*body)) > s.cfg.MaxBodyBytes {
		http.Error(w, "edited body too large", http.StatusRequestEntityTooLarge)
		return errors.New("edited body too large")
	}
	return nil
}

func decodeJSONLimit(w http.ResponseWriter, r *http.Request, destination interface{}, limit int64) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return errors.New("invalid JSON content type")
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			http.Error(w, "JSON body too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
		}
		return fmt.Errorf("decode JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			http.Error(w, "JSON body too large", http.StatusRequestEntityTooLarge)
			return fmt.Errorf("decode JSON: %w", err)
		}
		http.Error(w, "JSON body must contain one value", http.StatusBadRequest)
		return errors.New("multiple JSON values")
	}
	return nil
}
