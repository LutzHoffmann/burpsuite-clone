package proxy

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/store"
)

type Config struct {
	Store          store.Store
	BodyLimitBytes int64
	Transport      http.RoundTripper
}

type Server struct {
	cfg Config
}

func NewServer(cfg Config) *Server {
	if cfg.Transport == nil {
		cfg.Transport = http.DefaultTransport
	}
	return &Server{cfg: cfg}
}

func (s *Server) Serve(listener net.Listener) error {
	return (&http.Server{Handler: http.HandlerFunc(s.handleHTTP)}).Serve(listener)
}

func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	startedAt := time.Now().UTC()
	requestBody, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read request body", http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()

	capturedRequestBody, requestTruncated, err := s.capture(requestBody)
	if err != nil {
		http.Error(w, "capture request body", http.StatusInternalServerError)
		return
	}

	forward := r.Clone(r.Context())
	forward.RequestURI = ""
	forward.Header = r.Header.Clone()
	forward.Body = io.NopCloser(bytes.NewReader(requestBody))
	forward.ContentLength = int64(len(requestBody))

	response, err := s.cfg.Transport.RoundTrip(forward)
	if err != nil {
		http.Error(w, "forward request", http.StatusBadGateway)
		return
	}
	responseBody, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		http.Error(w, "read upstream response", http.StatusBadGateway)
		return
	}

	capturedResponseBody, responseTruncated, err := s.capture(responseBody)
	if err != nil {
		http.Error(w, "capture response body", http.StatusInternalServerError)
		return
	}

	exchange := &store.Exchange{
		Method:            r.Method,
		Scheme:            r.URL.Scheme,
		Host:              r.URL.Host,
		Path:              r.URL.Path,
		Query:             r.URL.RawQuery,
		Status:            response.StatusCode,
		MIMEType:          response.Header.Get("Content-Type"),
		RequestSize:       int64(len(requestBody)),
		ResponseSize:      int64(len(responseBody)),
		Duration:          time.Since(startedAt),
		StartedAt:         startedAt,
		RequestTruncated:  requestTruncated,
		ResponseTruncated: responseTruncated,
		Request: store.RequestData{
			Headers: r.Header.Clone(),
			Body:    capturedRequestBody,
		},
		Response: store.ResponseData{
			Headers: response.Header.Clone(),
			Body:    capturedResponseBody,
		},
	}
	if s.cfg.Store != nil {
		_ = s.cfg.Store.SaveExchange(context.Background(), exchange)
	}

	copyHeaders(w.Header(), response.Header)
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(responseBody)
}

func (s *Server) capture(body []byte) ([]byte, bool, error) {
	return readLimitedBody(io.NopCloser(bytes.NewReader(body)), s.cfg.BodyLimitBytes)
}

func copyHeaders(destination, source http.Header) {
	for key, values := range source {
		destination[key] = append([]string(nil), values...)
	}
}
