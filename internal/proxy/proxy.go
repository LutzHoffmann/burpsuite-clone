package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/certs"
	"github.com/lutzifer/burpsuite-clone/internal/store"
)

type Config struct {
	Store          store.Store
	BodyLimitBytes int64
	Transport      http.RoundTripper
	Authority      *certs.Authority
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
	if r.Method == http.MethodConnect {
		s.handleConnect(w, r)
		return
	}

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

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Authority == nil {
		http.Error(w, "HTTPS interception unavailable", http.StatusBadGateway)
		return
	}

	client, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		log.Printf("proxy: hijack CONNECT connection: %v", err)
		return
	}
	defer client.Close()

	host := r.Host
	certificateHost, _, err := net.SplitHostPort(host)
	if err != nil {
		certificateHost = host
	}
	certificate, err := s.cfg.Authority.CertificateForHost(certificateHost)
	if err != nil {
		log.Printf("proxy: create CONNECT certificate: %v", err)
		return
	}
	if _, err := fmt.Fprint(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		log.Printf("proxy: establish CONNECT tunnel: %v", err)
		return
	}

	tlsClient := tls.Server(client, &tls.Config{Certificates: []tls.Certificate{certificate}})
	defer tlsClient.Close()
	inner, err := http.ReadRequest(bufio.NewReader(tlsClient))
	if err != nil {
		log.Printf("proxy: read CONNECT request: %v", err)
		return
	}
	inner.URL.Scheme = "https"
	inner.URL.Host = host

	s.forwardHTTPS(tlsClient, inner)
}

func (s *Server) forwardHTTPS(client net.Conn, request *http.Request) {
	startedAt := time.Now().UTC()
	capturedRequest := newCapturingReadCloser(request.Body, s.cfg.BodyLimitBytes)

	forward := request.Clone(request.Context())
	forward.RequestURI = ""
	forward.Header = request.Header.Clone()
	forward.Body = capturedRequest

	response, err := s.cfg.Transport.RoundTrip(forward)
	if err != nil {
		log.Printf("proxy: forward HTTPS request: %v", err)
		return
	}
	capturedResponse := newCapturingReadCloser(response.Body, s.cfg.BodyLimitBytes)
	response.Body = capturedResponse
	if err := response.Write(client); err != nil {
		log.Printf("proxy: stream HTTPS upstream response: %v", err)
	}
	if err := capturedResponse.Close(); err != nil {
		log.Printf("proxy: close HTTPS upstream response: %v", err)
	}

	requestBody, requestTruncated := capturedRequest.Captured()
	responseBody, responseTruncated := capturedResponse.Captured()
	s.saveExchange(&store.Exchange{
		Method:            request.Method,
		Scheme:            request.URL.Scheme,
		Host:              request.URL.Host,
		Path:              request.URL.Path,
		Query:             request.URL.RawQuery,
		Status:            response.StatusCode,
		MIMEType:          response.Header.Get("Content-Type"),
		RequestSize:       capturedRequest.Size(),
		ResponseSize:      capturedResponse.Size(),
		Duration:          time.Since(startedAt),
		StartedAt:         startedAt,
		RequestTruncated:  requestTruncated,
		ResponseTruncated: responseTruncated,
		Request: store.RequestData{
			Headers: request.Header.Clone(),
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
