package proxy

import (
	"context"
	"io"
	"log"
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
	capturedRequest := newCapturingReadCloser(r.Body, s.cfg.BodyLimitBytes)

	forward := r.Clone(r.Context())
	forward.RequestURI = ""
	forward.Header = r.Header.Clone()
	forward.Body = capturedRequest

	response, err := s.cfg.Transport.RoundTrip(forward)
	if err != nil {
		http.Error(w, "forward request", http.StatusBadGateway)
		return
	}
	capturedResponse := newCapturingReadCloser(response.Body, s.cfg.BodyLimitBytes)
	copyHeaders(w.Header(), response.Header)
	w.WriteHeader(response.StatusCode)
	if _, err := io.Copy(w, capturedResponse); err != nil {
		log.Printf("proxy: stream upstream response: %v", err)
	}
	if err := capturedResponse.Close(); err != nil {
		log.Printf("proxy: close upstream response: %v", err)
	}

	requestBody, requestTruncated := capturedRequest.Captured()
	responseBody, responseTruncated := capturedResponse.Captured()
	s.saveExchange(&store.Exchange{
		Method:            r.Method,
		Scheme:            r.URL.Scheme,
		Host:              r.URL.Host,
		Path:              r.URL.Path,
		Query:             r.URL.RawQuery,
		Status:            response.StatusCode,
		MIMEType:          response.Header.Get("Content-Type"),
		RequestSize:       capturedRequest.Size(),
		ResponseSize:      capturedResponse.Size(),
		Duration:          time.Since(startedAt),
		StartedAt:         startedAt,
		RequestTruncated:  requestTruncated,
		ResponseTruncated: responseTruncated,
		Request: store.RequestData{
			Headers: r.Header.Clone(),
			Body:    requestBody,
		},
		Response: store.ResponseData{
			Headers: response.Header.Clone(),
			Body:    responseBody,
		},
	})
}

func (s *Server) saveExchange(exchange *store.Exchange) {
	if s.cfg.Store == nil {
		return
	}
	if err := s.cfg.Store.SaveExchange(context.Background(), exchange); err != nil {
		log.Printf("proxy: save exchange: %v", err)
	}
}

func copyHeaders(destination, source http.Header) {
	for key, values := range source {
		destination[key] = append([]string(nil), values...)
	}
}
