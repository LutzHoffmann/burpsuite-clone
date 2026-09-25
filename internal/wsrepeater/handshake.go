package wsrepeater

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"slices"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/net/http/httpguts"
)

// detach transfers ownership away from the dialing context. One-shot callers
// retain that context through their exchange; sessions detach before publication.
func dialWebSocket(ctx context.Context, r ConnectRequest, roots *x509.CertPool, timeout time.Duration) (*websocket.Conn, func(), string) {
	headers := http.Header{}
	for k, v := range r.Headers {
		headers[http.CanonicalHeaderKey(k)] = append([]string(nil), v...)
	}
	dialer := websocket.Dialer{HandshakeTimeout: timeout, Subprotocols: append([]string(nil), r.Subprotocols...)}
	var stop func() bool
	detach := func() {
		if stop != nil {
			stop()
		}
	}
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
	tlsFailed := false
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
		secured := tls.Client(raw, &tls.Config{RootCAs: roots, ServerName: host, MinVersion: tls.VersionTLS12})
		if e = secured.HandshakeContext(dialCtx); e != nil {
			tlsFailed = true
			_ = raw.Close()
			return nil, e
		}
		return &handshakeConn{Conn: secured, remaining: maxHandshakeBytes}, nil
	}
	c, response, e := dialer.DialContext(ctx, r.URL, headers)
	if e != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		reason := networkOutcome(ctx, e, "handshake_error")
		if reason == "handshake_error" && tlsFailed {
			reason = "tls_error"
		}
		return nil, detach, reason
	}
	selected := response.Header.Values("Sec-WebSocket-Protocol")
	if len(response.Header.Values("Sec-WebSocket-Extensions")) != 0 || len(selected) > 1 ||
		(len(selected) == 1 && (!httpguts.ValidHeaderFieldName(selected[0]) || !slices.Contains(r.Subprotocols, selected[0]))) {
		_ = c.Close()
		return nil, detach, "handshake_error"
	}
	return c, detach, ""
}

const maxHandshakeBytes = 64 << 10

var errHandshakeTooLarge = errors.New("WebSocket handshake headers too large")

// handshakeConn caps HTTP response bytes before http.ReadResponse can allocate
// unbounded header strings. After the empty header line it is a transparent relay.
// The wrapper belongs outside TLS so the bound applies equally to ws and wss.
type handshakeConn struct {
	net.Conn
	remaining int
	lineEnded bool
	done      bool
}

func (c *handshakeConn) Close() error {
	// Session expiry must not wait for a TLS close_notify write to a stalled peer.
	// Graceful WebSocket close is handled separately with its own short deadline.
	if secured, ok := c.Conn.(*tls.Conn); ok {
		return secured.NetConn().Close()
	}
	return c.Conn.Close()
}

func (c *handshakeConn) Read(p []byte) (int, error) {
	if c.done || len(p) == 0 {
		return c.Conn.Read(p)
	}
	if c.remaining == 0 {
		return 0, errHandshakeTooLarge
	}
	if len(p) > c.remaining {
		p = p[:c.remaining]
	}
	n, err := c.Conn.Read(p)
	c.remaining -= n
	for _, b := range p[:n] {
		if b == '\n' {
			if c.lineEnded {
				c.done = true
				break
			}
			c.lineEnded = true
		} else if b != '\r' {
			c.lineEnded = false
		}
	}
	return n, err
}
