package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/lutzifer/burpsuite-clone/internal/certs"
	"github.com/lutzifer/burpsuite-clone/internal/events"
	"github.com/lutzifer/burpsuite-clone/internal/intercept"
	"github.com/lutzifer/burpsuite-clone/internal/scope"
	"github.com/lutzifer/burpsuite-clone/internal/store"
)

const (
	serverReadHeaderTimeout  = 10 * time.Second
	serverIdleTimeout        = 2 * time.Minute
	defaultStreamIdleTimeout = 2 * time.Minute
)

type TargetObserver interface {
	Observe(context.Context, *store.Exchange) error
}

type Config struct {
	Store             store.Store
	BodyLimitBytes    int64
	Transport         http.RoundTripper
	Authority         *certs.Authority
	Events            *events.Hub
	Intercept         *intercept.Controller
	StreamIdleTimeout time.Duration
	Scope             *scope.Manager
	Target            TargetObserver
}

type Server struct {
	cfg    Config
	nextID atomic.Uint64
}

type preparedRequest struct {
	forward     *http.Request
	capture     *capturingReadCloser
	headers     http.Header
	intercepted bool
	dropped     bool
	droppedBody []byte
	decision    scope.Decision
}

func NewServer(cfg Config) *Server {
	if cfg.StreamIdleTimeout <= 0 {
		cfg.StreamIdleTimeout = defaultStreamIdleTimeout
	}
	if cfg.Transport == nil {
		cfg.Transport = defaultTransport(cfg.StreamIdleTimeout)
	}
	return &Server{cfg: cfg}
}

func defaultTransport(streamIdleTimeout time.Duration) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		connection, err := dialer.DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		return withIdleTimeout(connection, streamIdleTimeout), nil
	}
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ResponseHeaderTimeout = 30 * time.Second
	transport.IdleConnTimeout = 90 * time.Second
	return transport
}

func (s *Server) Serve(listener net.Listener) error {
	server := &http.Server{
		Handler:           http.HandlerFunc(s.handleHTTP),
		ReadHeaderTimeout: serverReadHeaderTimeout,
		IdleTimeout:       serverIdleTimeout,
		ConnContext: func(ctx context.Context, connection net.Conn) context.Context {
			return context.WithValue(ctx, clientConnectionKey{}, connection)
		},
	}
	return server.Serve(listener)
}

func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		s.handleConnect(w, r)
		return
	}
	client := clientConnection(r.Context())
	if client != nil && r.Body != nil {
		r.Body = &idleTimeoutReadCloser{ReadCloser: r.Body, connection: client, timeout: s.cfg.StreamIdleTimeout}
	}

	startedAt := time.Now().UTC()
	decision := s.classify(r)
	prepared, err := s.prepareRequest(r, decision)
	if err != nil {
		s.savePreparationFailure(r, decision, startedAt, err)
		http.Error(w, "intercept request", http.StatusGatewayTimeout)
		return
	}
	if prepared.dropped {
		s.saveDroppedExchange(r, prepared, startedAt)
		http.Error(w, "request dropped", http.StatusForbidden)
		return
	}

	response, err := s.cfg.Transport.RoundTrip(prepared.forward)
	if err != nil {
		s.saveRoundTripFailure(prepared, startedAt, err)
		http.Error(w, "forward request", http.StatusBadGateway)
		return
	}
	capturedResponse := newCapturingReadCloser(response.Body, s.cfg.BodyLimitBytes)
	responseHeaders := response.Header.Clone()
	clientHeaders := response.Header.Clone()
	stripHopByHopHeaders(clientHeaders)
	copyHeaders(w.Header(), clientHeaders)
	if client != nil {
		_ = client.SetWriteDeadline(time.Now().Add(s.cfg.StreamIdleTimeout))
		defer func() { _ = client.SetWriteDeadline(time.Time{}) }()
	}
	w.WriteHeader(response.StatusCode)
	destination := io.Writer(w)
	if client != nil {
		destination = &idleTimeoutWriter{Writer: w, connection: client, timeout: s.cfg.StreamIdleTimeout}
	}
	_, copyErr := io.Copy(destination, capturedResponse)
	closeErr := capturedResponse.Close()
	if copyErr != nil {
		log.Printf("proxy: stream upstream response: %v", copyErr)
	}
	if closeErr != nil {
		log.Printf("proxy: close upstream response: %v", closeErr)
	}
	s.saveCompletedExchange(prepared, response, responseHeaders, capturedResponse, startedAt, errors.Join(copyErr, closeErr))
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
	client = withIdleTimeout(client, s.cfg.StreamIdleTimeout)
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

	tlsClient := tls.Server(client, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
	defer tlsClient.Close()
	_ = tlsClient.SetDeadline(time.Now().Add(s.cfg.StreamIdleTimeout))
	if err := tlsClient.Handshake(); err != nil {
		log.Printf("proxy: handshake CONNECT client: %v", err)
		return
	}
	reader := bufio.NewReader(tlsClient)
	for {
		_ = tlsClient.SetReadDeadline(time.Now().Add(s.cfg.StreamIdleTimeout))
		inner, err := http.ReadRequest(reader)
		if err != nil {
			if !errors.Is(err, io.EOF) && !isTimeout(err) {
				log.Printf("proxy: read CONNECT request: %v", err)
			}
			return
		}
		inner.URL.Scheme = "https"
		inner.URL.Host = host
		inner.RequestURI = inner.URL.RequestURI()
		if !s.forwardHTTPS(tlsClient, inner) {
			return
		}
	}
}

func (s *Server) forwardHTTPS(client net.Conn, request *http.Request) bool {
	startedAt := time.Now().UTC()
	decision := s.classify(request)
	prepared, err := s.prepareRequest(request, decision)
	if err != nil {
		s.savePreparationFailure(request, decision, startedAt, err)
		return writeTunnelError(client, request, http.StatusGatewayTimeout, "intercept request")
	}
	if prepared.dropped {
		s.saveDroppedExchange(request, prepared, startedAt)
		return writeTunnelError(client, request, http.StatusForbidden, "request dropped")
	}

	response, err := s.cfg.Transport.RoundTrip(prepared.forward)
	if err != nil {
		s.saveRoundTripFailure(prepared, startedAt, err)
		return writeTunnelError(client, request, http.StatusBadGateway, "forward request")
	}
	capturedResponse := newCapturingReadCloser(response.Body, s.cfg.BodyLimitBytes)
	responseHeaders := response.Header.Clone()
	stripHopByHopHeaders(response.Header)
	response.Body = capturedResponse
	_ = client.SetWriteDeadline(time.Now().Add(s.cfg.StreamIdleTimeout))
	writeErr := response.Write(client)
	closeErr := capturedResponse.Close()
	if writeErr != nil {
		log.Printf("proxy: stream HTTPS upstream response: %v", writeErr)
	}
	if closeErr != nil {
		log.Printf("proxy: close HTTPS upstream response: %v", closeErr)
	}
	s.saveCompletedExchange(prepared, response, responseHeaders, capturedResponse, startedAt, errors.Join(writeErr, closeErr))
	return writeErr == nil && !request.Close && !response.Close
}

func (s *Server) classify(request *http.Request) scope.Decision {
	if s.cfg.Scope == nil {
		return scope.Decision{InScope: false, Reason: "no_include"}
	}
	return s.cfg.Scope.Current().Classify(scope.Target{
		Scheme: request.URL.Scheme,
		Host:   request.URL.Host,
		Path:   request.URL.Path,
	})
}

func (s *Server) prepareRequest(request *http.Request, captureDecision scope.Decision) (*preparedRequest, error) {
	if !captureDecision.InScope || s.cfg.Intercept == nil || !s.cfg.Intercept.Matches(intercept.MatchRequest{
		Method: request.Method,
		Host:   request.URL.Host,
		Path:   request.URL.Path,
		MIME:   request.Header.Get("Content-Type"),
	}) {
		return prepareStreamingRequest(request, s.cfg.BodyLimitBytes, captureDecision), nil
	}

	body, tooLarge, err := readInterceptBody(request.Body, s.cfg.BodyLimitBytes)
	if err != nil {
		return nil, err
	}
	if tooLarge {
		request.Body = &prefixReadCloser{Reader: io.MultiReader(bytes.NewReader(body), request.Body), closer: request.Body}
		return prepareStreamingRequest(request, s.cfg.BodyLimitBytes, captureDecision), nil
	}
	_ = request.Body.Close()

	item := intercept.Item{
		ID:           strconv.FormatUint(s.nextID.Add(1), 10),
		Method:       request.Method,
		URL:          request.URL.String(),
		Headers:      request.Header.Clone(),
		Body:         body,
		BodyEditable: isTextSafe(request.Header.Get("Content-Type"), body),
	}
	interceptDecision, err := s.cfg.Intercept.Queue().Enqueue(request.Context(), item)
	if err != nil {
		return nil, err
	}
	if interceptDecision.Action == intercept.ActionDrop {
		return &preparedRequest{
			headers: request.Header.Clone(), intercepted: true, dropped: true, droppedBody: body,
			decision: captureDecision,
		}, nil
	}

	method := interceptDecision.Edit.Method
	if method == "" {
		method = item.Method
	}
	rawURL := interceptDecision.Edit.URL
	if rawURL == "" {
		rawURL = item.URL
	}
	parsedURL, err := url.Parse(rawURL)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return nil, fmt.Errorf("invalid intercepted request URL")
	}
	headers := interceptDecision.Edit.Headers
	if headers == nil {
		headers = item.Headers
	}
	editedBody := body
	if item.BodyEditable && (interceptDecision.Edit.BodySet || interceptDecision.Edit.Body != nil) {
		editedBody = interceptDecision.Edit.Body
	}
	forward := request.Clone(request.Context())
	forward.Method = method
	forward.URL = parsedURL
	forward.Host = parsedURL.Host
	forward.RequestURI = ""
	forward.Header = cloneHeaders(headers)
	capture := newCapturingReadCloser(io.NopCloser(bytes.NewReader(editedBody)), s.cfg.BodyLimitBytes)
	forward.Body = capture
	forward.ContentLength = int64(len(editedBody))
	stripHopByHopHeaders(forward.Header)
	return &preparedRequest{
		forward: forward, capture: capture, headers: cloneHeaders(headers), intercepted: true,
		decision: captureDecision,
	}, nil
}

func prepareStreamingRequest(request *http.Request, bodyLimit int64, decision scope.Decision) *preparedRequest {
	capture := newCapturingReadCloser(request.Body, bodyLimit)
	forward := request.Clone(request.Context())
	forward.RequestURI = ""
	forward.Header = request.Header.Clone()
	forward.Body = capture
	stripHopByHopHeaders(forward.Header)
	return &preparedRequest{forward: forward, capture: capture, headers: request.Header.Clone(), decision: decision}
}

func readInterceptBody(body io.ReadCloser, limit int64) ([]byte, bool, error) {
	if body == nil || body == http.NoBody {
		return nil, false, nil
	}
	if limit < 0 {
		limit = 0
	}
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, false, fmt.Errorf("read intercepted request body: %w", err)
	}
	return data, int64(len(data)) > limit, nil
}

func (s *Server) saveCompletedExchange(prepared *preparedRequest, response *http.Response, responseHeaders http.Header, capturedResponse *capturingReadCloser, startedAt time.Time, operationErr error) {
	requestBody, requestTruncated := prepared.capture.Captured()
	responseBody, responseTruncated := capturedResponse.Captured()
	exchange := s.exchangeFromPrepared(prepared, startedAt)
	exchange.Status = response.StatusCode
	exchange.MIMEType = responseHeaders.Get("Content-Type")
	exchange.RequestSize = prepared.capture.Size()
	exchange.ResponseSize = capturedResponse.Size()
	exchange.RequestTruncated = requestTruncated
	exchange.ResponseTruncated = responseTruncated
	exchange.Request.Body = requestBody
	exchange.Response = store.ResponseData{Headers: responseHeaders, Body: responseBody}
	if operationErr != nil {
		exchange.Error = true
		exchange.ErrorMessage = operationErr.Error()
	}
	s.saveExchange(exchange)
}

func (s *Server) saveRoundTripFailure(prepared *preparedRequest, startedAt time.Time, operationErr error) {
	requestBody, requestTruncated := prepared.capture.Captured()
	exchange := s.exchangeFromPrepared(prepared, startedAt)
	exchange.RequestSize = prepared.capture.Size()
	exchange.RequestTruncated = requestTruncated
	exchange.Request.Body = requestBody
	exchange.Error = true
	exchange.ErrorMessage = operationErr.Error()
	s.saveExchange(exchange)
}

func (s *Server) savePreparationFailure(request *http.Request, decision scope.Decision, startedAt time.Time, operationErr error) {
	s.saveExchange(&store.Exchange{
		Method: request.Method, Scheme: request.URL.Scheme, Host: request.URL.Host,
		Path: request.URL.Path, Query: request.URL.RawQuery, Duration: time.Since(startedAt),
		StartedAt: startedAt, Error: true, ErrorMessage: operationErr.Error(),
		InScope: decision.InScope, ScopeVersion: decision.Version, ScopeRuleID: decision.RuleID,
		Request: store.RequestData{Headers: request.Header.Clone()},
	})
}

func (s *Server) saveDroppedExchange(request *http.Request, prepared *preparedRequest, startedAt time.Time) {
	s.saveExchange(&store.Exchange{
		Method: request.Method, Scheme: request.URL.Scheme, Host: request.URL.Host,
		Path: request.URL.Path, Query: request.URL.RawQuery, Duration: time.Since(startedAt),
		StartedAt: startedAt, Intercepted: true, Error: true, ErrorMessage: "request dropped by operator",
		InScope: prepared.decision.InScope, ScopeVersion: prepared.decision.Version, ScopeRuleID: prepared.decision.RuleID,
		RequestSize: int64(len(prepared.droppedBody)),
		Request:     store.RequestData{Headers: prepared.headers, Body: append([]byte(nil), prepared.droppedBody...)},
	})
}

func (s *Server) exchangeFromPrepared(prepared *preparedRequest, startedAt time.Time) *store.Exchange {
	request := prepared.forward
	return &store.Exchange{
		Method: request.Method, Scheme: request.URL.Scheme, Host: request.URL.Host,
		Path: request.URL.Path, Query: request.URL.RawQuery, Duration: time.Since(startedAt),
		StartedAt: startedAt, Intercepted: prepared.intercepted,
		InScope: prepared.decision.InScope, ScopeVersion: prepared.decision.Version, ScopeRuleID: prepared.decision.RuleID,
		Request: store.RequestData{Headers: prepared.headers},
	}
}

func (s *Server) saveExchange(exchange *store.Exchange) {
	if s.cfg.Store == nil {
		return
	}
	if err := s.cfg.Store.SaveExchange(context.Background(), exchange); err != nil {
		log.Printf("proxy: save exchange: %v", err)
		return
	}
	if s.cfg.Events != nil {
		s.cfg.Events.Publish(events.Event{
			Type: "history.entry.created",
			Data: map[string]interface{}{
				"id": exchange.ID, "method": exchange.Method, "host": exchange.Host,
				"path": exchange.Path, "status": exchange.Status,
			},
		})
	}
	if s.cfg.Target != nil {
		if err := s.cfg.Target.Observe(context.Background(), exchange); err != nil {
			log.Printf("proxy: project exchange %d: failed", exchange.ID)
		}
	}
}

func stripHopByHopHeaders(headers http.Header) {
	for _, token := range strings.Split(headers.Get("Connection"), ",") {
		if name := strings.TrimSpace(token); name != "" {
			headers.Del(name)
		}
	}
	for _, name := range []string{
		"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"Proxy-Connection", "TE", "Trailer", "Transfer-Encoding", "Upgrade",
	} {
		headers.Del(name)
	}
}

func cloneHeaders(headers map[string][]string) http.Header {
	cloned := make(http.Header, len(headers))
	for name, values := range headers {
		cloned[name] = append([]string(nil), values...)
	}
	return cloned
}

func copyHeaders(destination, source http.Header) {
	for key, values := range source {
		destination[key] = append([]string(nil), values...)
	}
}

func isTextSafe(contentType string, body []byte) bool {
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

func writeTunnelError(client net.Conn, request *http.Request, status int, message string) bool {
	body := []byte(message + "\n")
	response := &http.Response{
		StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: http.Header{"Content-Type": {"text/plain; charset=utf-8"}},
		Body:   io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: request,
	}
	if err := response.Write(client); err != nil {
		return false
	}
	return !request.Close
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

type prefixReadCloser struct {
	io.Reader
	closer io.Closer
}

func (r *prefixReadCloser) Close() error {
	return r.closer.Close()
}
