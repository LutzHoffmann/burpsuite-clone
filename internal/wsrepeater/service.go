// Package wsrepeater performs bounded, one-shot sends on independent WebSockets.
package wsrepeater

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
	"github.com/lutzifer/burpsuite-clone/internal/scope"
	"golang.org/x/net/http/httpguts"
	"golang.org/x/net/idna"
)

const maxPayload int64 = 1 << 20

var (
	ErrInvalid    = errors.New("invalid WebSocket request")
	ErrOutOfScope = errors.New("WebSocket target is outside current scope")
	ErrBusy       = errors.New("WebSocket repeater is busy")
)

type Request struct {
	URL           string              `json:"url"`
	Type          string              `json:"type"`
	Payload       string              `json:"payload"`
	PayloadFormat string              `json:"payloadFormat"`
	Headers       map[string][]string `json:"headers"`
	Subprotocols  []string            `json:"subprotocols"`
}

type Result struct {
	Sent        bool      `json:"sent"`
	Outcome     string    `json:"outcome"`
	Messages    []Message `json:"messages"`
	DurationMS  int64     `json:"durationMs"`
	Subprotocol string    `json:"subprotocol"`
}

type Message struct {
	Type          string `json:"type"`
	Payload       string `json:"payload"`
	PayloadFormat string `json:"payloadFormat"`
	// Size is bytes observed, a lower bound when Truncated is true.
	Size      int64 `json:"size"`
	Truncated bool  `json:"truncated"`
}

type Service struct {
	manager        *scope.Manager
	bodyLimit      int64
	slots          chan struct{}
	overallTimeout time.Duration
	receiveWindow  time.Duration
	roots          *x509.CertPool
}

func NewService(manager *scope.Manager, bodyLimit int64) *Service {
	return &Service{manager: manager, bodyLimit: payloadLimit(bodyLimit), slots: make(chan struct{}, 4), overallTimeout: 15 * time.Second, receiveWindow: 5 * time.Second}
}

func payloadLimit(limit int64) int64 {
	if limit < 0 {
		return 0
	}
	if limit > maxPayload {
		return maxPayload
	}
	return limit
}

// Validate never includes user-authored data in its errors.
func Validate(r Request, limit int64) error {
	if len(r.URL) > 65536 || !utf8.ValidString(r.URL) {
		return ErrInvalid
	}
	u, e := url.Parse(r.URL)
	if e != nil || u.Opaque != "" || u.User != nil || strings.Contains(r.URL, "#") || (u.Scheme != "ws" && u.Scheme != "wss") || u.Hostname() == "" {
		return ErrInvalid
	}
	if strings.HasSuffix(u.Host, ":") {
		return ErrInvalid
	}
	if p := u.Port(); p != "" {
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 65535 {
			return ErrInvalid
		}
	}
	host := u.Hostname()
	if strings.Contains(u.Host, "[") {
		if net.ParseIP(host) == nil {
			return ErrInvalid
		}
	} else if strings.Contains(host, ":") {
		return ErrInvalid
	}
	if net.ParseIP(host) == nil {
		ascii, e := idna.Lookup.ToASCII(host)
		if e != nil || ascii == "" {
			return ErrInvalid
		}
		for _, c := range ascii {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_') {
				return ErrInvalid
			}
		}
	}
	if r.Type != "text" && r.Type != "binary" {
		return ErrInvalid
	}
	if _, e := payload(r, payloadLimit(limit)); e != nil {
		return e
	}
	budget := 16 * 1024
	seen := make(map[string]bool)
	for k, values := range r.Headers {
		lower := strings.ToLower(k)
		if !httpguts.ValidHeaderFieldName(k) || blockedHeader(lower) || seen[lower] {
			return ErrInvalid
		}
		seen[lower] = true
		if len(values) == 0 {
			budget -= len(k) + 4
		}
		if budget < 0 {
			return ErrInvalid
		}
		for _, v := range values {
			if !utf8.ValidString(v) || !httpguts.ValidHeaderFieldValue(v) {
				return ErrInvalid
			}
			budget -= len(k) + len(v) + 4
			if budget < 0 {
				return ErrInvalid
			}
		}
	}
	if len(r.Subprotocols) > 32 {
		return ErrInvalid
	}
	seen = make(map[string]bool)
	protocolBudget := 4096
	for _, p := range r.Subprotocols {
		protocolBudget -= len(p) + 2
		if !httpguts.ValidHeaderFieldName(p) || seen[p] || protocolBudget < 0 {
			return ErrInvalid
		}
		seen[p] = true
	}
	if len(r.Subprotocols) > 0 {
		budget -= len("Sec-WebSocket-Protocol: \r\n") + len(strings.Join(r.Subprotocols, ", "))
		if budget < 0 {
			return ErrInvalid
		}
	}
	return nil
}

func blockedHeader(k string) bool {
	if strings.HasPrefix(k, "sec-websocket-") {
		return true
	}
	switch k {
	case "host", "connection", "upgrade", "content-length", "transfer-encoding", "proxy-authorization", "proxy-authenticate", "proxy-connection", "keep-alive", "te", "trailer":
		return true
	}
	return false
}

func payload(r Request, limit int64) ([]byte, error) {
	var p []byte
	switch r.PayloadFormat {
	case "text":
		if int64(len(r.Payload)) > limit || !utf8.ValidString(r.Payload) {
			return nil, ErrInvalid
		}
		p = []byte(r.Payload)
	case "hex":
		if int64(len(r.Payload)) > limit*2 {
			return nil, ErrInvalid
		}
		var e error
		p, e = hex.DecodeString(r.Payload)
		if e != nil {
			return nil, ErrInvalid
		}
	default:
		return nil, ErrInvalid
	}
	if r.Type == "text" && !utf8.Valid(p) {
		return nil, ErrInvalid
	}
	return p, nil
}

func (s *Service) Send(parent context.Context, r Request) (result Result, err error) {
	if err = Validate(r, s.bodyLimit); err != nil {
		return result, err
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return result, ErrBusy
	}
	started := time.Now()
	result.Messages = []Message{}
	defer func() { result.DurationMS = time.Since(started).Milliseconds() }()
	ctx, cancel := context.WithTimeout(parent, s.overallTimeout)
	defer cancel()
	p, _ := payload(r, s.bodyLimit)
	headers := http.Header{}
	for k, values := range r.Headers {
		headers[http.CanonicalHeaderKey(k)] = append([]string(nil), values...)
	}
	dialer := websocket.Dialer{HandshakeTimeout: s.overallTimeout, Subprotocols: append([]string(nil), r.Subprotocols...), TLSClientConfig: &tls.Config{RootCAs: s.roots, MinVersion: tls.VersionTLS12}}
	// Closing the transport also interrupts a server stalled mid-handshake.
	var stop func() bool
	dialTCP := func(dialCtx context.Context, network, address string) (net.Conn, error) {
		c, e := (&net.Dialer{}).DialContext(dialCtx, network, address)
		if e == nil {
			stop = context.AfterFunc(ctx, func() { _ = c.Close() })
		}
		return c, e
	}
	dialer.NetDialContext = func(dialCtx context.Context, network, address string) (net.Conn, error) {
		c, e := dialTCP(dialCtx, network, address)
		if e != nil {
			return nil, e
		}
		return &handshakeConn{Conn: c, remaining: maxHandshakeBytes}, nil
	}
	defer func() {
		if stop != nil {
			stop()
		}
	}()
	tlsFailed := false
	// Bound decrypted HTTP header bytes, not TLS records or certificate traffic.
	dialer.NetDialTLSContext = func(dialCtx context.Context, network, address string) (net.Conn, error) {
		raw, e := dialTCP(dialCtx, network, address)
		if e != nil {
			return nil, e
		}
		host, _, e := net.SplitHostPort(address)
		if e != nil {
			_ = raw.Close()
			return nil, e
		}
		secured := tls.Client(raw, &tls.Config{RootCAs: s.roots, ServerName: host, MinVersion: tls.VersionTLS12})
		if e = secured.HandshakeContext(dialCtx); e != nil {
			tlsFailed = true
			_ = secured.Close()
			return nil, e
		}
		return &handshakeConn{Conn: secured, remaining: maxHandshakeBytes}, nil
	}
	u, _ := url.Parse(r.URL)
	scheme := "http"
	if u.Scheme == "wss" {
		scheme = "https"
	}
	if s.manager == nil {
		return result, ErrOutOfScope
	}
	rules := s.manager.Current()
	if rules == nil || !rules.Classify(scope.Target{Scheme: scheme, Host: u.Host, Path: u.Path}).InScope {
		return result, ErrOutOfScope
	}
	c, response, e := dialer.DialContext(ctx, r.URL, headers)
	if e != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		result.Outcome = networkOutcome(ctx, e, "handshake_error")
		if result.Outcome == "handshake_error" && tlsFailed {
			result.Outcome = "tls_error"
		}
		return result, nil
	}
	defer c.Close()
	// Gorilla accepts unoffered protocols and can enable unsolicited compression.
	// Validate all response fields before writing any application data.
	selected := response.Header.Values("Sec-WebSocket-Protocol")
	if len(response.Header.Values("Sec-WebSocket-Extensions")) != 0 || len(selected) > 1 ||
		(len(selected) == 1 && (!httpguts.ValidHeaderFieldName(selected[0]) || !slices.Contains(r.Subprotocols, selected[0]))) {
		result.Outcome = "handshake_error"
		return result, nil
	}
	result.Subprotocol = c.Subprotocol()
	deadline, _ := ctx.Deadline()
	_ = c.SetWriteDeadline(deadline)
	mt := websocket.TextMessage
	if r.Type == "binary" {
		mt = websocket.BinaryMessage
	}
	if e = c.WriteMessage(mt, p); e != nil {
		result.Outcome = networkOutcome(ctx, e, "write_error")
		return result, nil
	}
	result.Sent = true
	windowEnd := time.Now().Add(s.receiveWindow)
	windowWins := windowEnd.Before(deadline)
	if !windowWins {
		windowEnd = deadline
	}
	_ = c.SetReadDeadline(windowEnd)
	_ = c.SetWriteDeadline(windowEnd)
	var total int64
	for {
		mt, reader, e := c.NextReader()
		if e != nil {
			result.Outcome = readOutcome(ctx, e, windowWins)
			return result, nil
		}
		bound := min(s.bodyLimit, maxPayload-total)
		data, e := io.ReadAll(io.LimitReader(reader, bound+1))
		observed := int64(len(data))
		limited := observed > bound
		if limited {
			data = data[:bound]
		}
		msg := Message{Type: "binary", PayloadFormat: "hex", Payload: hex.EncodeToString(data), Size: observed, Truncated: limited || e != nil}
		if mt == websocket.TextMessage {
			msg.Type = "text"
			if utf8.Valid(data) {
				msg.PayloadFormat = "text"
				msg.Payload = string(data)
			}
		}
		result.Messages = append(result.Messages, msg)
		total += int64(len(data))
		if limited {
			result.Outcome = "size_limit"
			return result, nil
		}
		if e != nil {
			result.Outcome = readOutcome(ctx, e, windowWins)
			return result, nil
		}
		if len(result.Messages) == 20 {
			result.Outcome = "message_limit"
			return result, nil
		}
		if total == maxPayload {
			result.Outcome = "size_limit"
			return result, nil
		}
	}
}

func networkOutcome(ctx context.Context, e error, fallback string) string {
	if errors.Is(ctx.Err(), context.Canceled) {
		return "canceled"
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(e, context.DeadlineExceeded) {
		return "timeout"
	}
	var ne net.Error
	if errors.As(e, &ne) && ne.Timeout() {
		return "timeout"
	}
	return fallback
}

func readOutcome(ctx context.Context, e error, windowWins bool) string {
	outcome := networkOutcome(ctx, e, "read_error")
	if outcome == "timeout" && ctx.Err() == nil && windowWins {
		return "window_complete"
	}
	var closeError *websocket.CloseError
	if outcome == "read_error" && errors.As(e, &closeError) && closeError.Code != websocket.CloseAbnormalClosure {
		return "peer_closed"
	}
	return outcome
}
