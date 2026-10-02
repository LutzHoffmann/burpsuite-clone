package wsrepeater

import (
	"context"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lutzifer/burpsuite-clone/internal/scope"
)

func TestRejectInvalidNegotiationBeforeSending(t *testing.T) {
	for _, tc := range []struct {
		name, responseHeaders string
		offer                 []string
	}{
		{"unoffered", "Sec-WebSocket-Protocol: other\r\n", []string{"chat"}},
		{"no offer", "Sec-WebSocket-Protocol: chat\r\n", nil},
		{"multiple tokens", "Sec-WebSocket-Protocol: chat, other\r\n", []string{"chat", "other"}},
		{"multiple headers", "Sec-WebSocket-Protocol: chat\r\nSec-WebSocket-Protocol: other\r\n", []string{"chat", "other"}},
		{"empty protocol", "Sec-WebSocket-Protocol: \r\n", []string{"chat"}},
		{"unsolicited compression", "Sec-WebSocket-Extensions: permessage-deflate; server_no_context_takeover; client_no_context_takeover\r\n", nil},
		{"unknown extension", "Sec-WebSocket-Extensions: unknown\r\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			received := make(chan int, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c, rw, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					received <- -1
					return
				}
				defer c.Close()
				accept := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
				fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n%s\r\n", base64.StdEncoding.EncodeToString(accept[:]), tc.responseHeaders)
				if err := rw.Flush(); err != nil {
					t.Error(err)
				}
				_ = c.SetReadDeadline(time.Now().Add(time.Second))
				var b [1]byte
				n, _ := rw.Read(b[:])
				received <- n
			}))
			defer server.Close()
			s := serviceFor(t, wsURL(server), 1024)
			r := draft(wsURL(server))
			r.Subprotocols = tc.offer
			out, err := s.Send(context.Background(), r)
			if err != nil || out.Sent || out.Outcome != "handshake_error" || out.Subprotocol != "" {
				t.Errorf("%+v %v", out, err)
			}
			if n := <-received; n != 0 {
				t.Errorf("sent %d bytes after invalid negotiation", n)
			}
		})
	}
}

func TestHandshakeResponseHeaderLimit(t *testing.T) {
	for _, secure := range []bool{false, true} {
		for _, terminated := range []bool{false, true} {
			t.Run(fmt.Sprintf("tls=%v/terminated=%v", secure, terminated), func(t *testing.T) {
				received := make(chan int, 1)
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					c, rw, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						received <- -1
						return
					}
					defer c.Close()
					accept := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
					head := fmt.Sprintf("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\nX-Large: %s", base64.StdEncoding.EncodeToString(accept[:]), strings.Repeat("a", 65536))
					if terminated {
						head += "\r\n\r\n"
					}
					_, _ = io.WriteString(c, head)
					_ = c.SetReadDeadline(time.Now().Add(time.Second))
					var b [1]byte
					n, _ := rw.Read(b[:])
					received <- n
				}))
				if secure {
					server.StartTLS()
				} else {
					server.Start()
				}
				defer server.Close()
				s := serviceFor(t, wsURL(server), 1024)
				s.overallTimeout = 500 * time.Millisecond
				if secure {
					s.roots = x509.NewCertPool()
					s.roots.AddCert(server.Certificate())
				}
				out, err := s.Send(context.Background(), draft(wsURL(server)))
				if err != nil || out.Sent || out.Outcome != "handshake_error" {
					t.Errorf("oversized header: %+v %v", out, err)
				}
				if n := <-received; n != 0 {
					t.Errorf("sent %d bytes after oversized header", n)
				}
			})
		}
	}
}

func TestValidateURLBoundAndUTF8(t *testing.T) {
	r := draft("ws://example.com/")
	r.URL += strings.Repeat("a", 65536-len(r.URL))
	if err := Validate(r, 1024); err != nil {
		t.Fatalf("exact URL boundary: %v", err)
	}
	r.URL += "a"
	if !errors.Is(Validate(r, 1024), ErrInvalid) {
		t.Error("oversized URL accepted")
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Request)
	}{
		{"URL", func(r *Request) { r.URL += "/\xff" }},
		{"header name", func(r *Request) { r.Headers = map[string][]string{"X-\xff": {"ok"}} }},
		{"header value", func(r *Request) { r.Headers = map[string][]string{"X-Test": {"\xff"}} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := draft("ws://example.com")
			tc.mutate(&r)
			if !errors.Is(Validate(r, 1024), ErrInvalid) {
				t.Fatal("invalid UTF-8 accepted")
			}
		})
	}
}

func TestTLSProtocolFailure(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected HTTP request") }))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS11}
	server.StartTLS()
	defer server.Close()
	s := serviceFor(t, wsURL(server), 1024)
	out, e := s.Send(context.Background(), draft(wsURL(server)))
	if e != nil || out.Sent || out.Outcome != "tls_error" {
		t.Fatalf("%+v %v", out, e)
	}
}

func TestCombinedHeaderBudgetBoundary(t *testing.T) {
	for _, protocols := range [][]string{{"chat"}, {"chat", "events"}} {
		t.Run(strings.Join(protocols, ","), func(t *testing.T) {
			r := draft("ws://example.com")
			r.Subprotocols = protocols
			protocolHeader := "Sec-WebSocket-Protocol: " + strings.Join(protocols, ", ") + "\r\n"
			r.Headers = map[string][]string{"X": {strings.Repeat("a", 16384-len(protocolHeader)-len("X: \r\n"))}}
			if err := Validate(r, 1024); err != nil {
				t.Fatalf("exact combined 16KiB boundary rejected: %v", err)
			}
			r.Headers["X"][0] += "a"
			if !errors.Is(Validate(r, 1024), ErrInvalid) {
				t.Fatal("combined headers exceeding 16KiB accepted")
			}
		})
	}
	r := draft("ws://example.com")
	r.Subprotocols = []string{strings.Repeat("a", 4097)}
	if !errors.Is(Validate(r, 1024), ErrInvalid) {
		t.Fatal("individual subprotocol budget exceeded")
	}
}

func TestHeaderBudgetBoundary(t *testing.T) {
	r := draft("ws://example.com")
	r.Headers = map[string][]string{"X": {strings.Repeat("a", 16384-5)}}
	if e := Validate(r, 1024); e != nil {
		t.Fatalf("exact 16KiB header rejected: %v", e)
	}
	r.Headers["X"][0] += "a"
	if !errors.Is(Validate(r, 1024), ErrInvalid) {
		t.Fatal("oversized header accepted")
	}
}

func TestHandshakeCancellationAndTimeout(t *testing.T) {
	for _, cancelRequest := range []bool{true, false} {
		t.Run(map[bool]string{true: "cancel", false: "timeout"}[cancelRequest], func(t *testing.T) {
			entered := make(chan struct{})
			closed := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c, _, e := w.(http.Hijacker).Hijack()
				if e != nil {
					return
				}
				defer c.Close()
				close(entered)
				_, _ = io.Copy(io.Discard, c)
				close(closed)
			}))
			defer server.Close()
			s := serviceFor(t, wsURL(server), 1024)
			s.overallTimeout = 150 * time.Millisecond
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan Result, 1)
			go func() {
				out, e := s.Send(ctx, draft(wsURL(server)))
				if e != nil {
					t.Error(e)
				}
				done <- out
			}()
			<-entered
			if cancelRequest {
				cancel()
			}
			want := "timeout"
			if cancelRequest {
				want = "canceled"
			}
			select {
			case out := <-done:
				if out.Sent || out.Outcome != want {
					t.Fatalf("%+v", out)
				}
			case <-time.After(time.Second):
				t.Fatal("handshake stuck")
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("transport still open")
			}
		})
	}
}

func TestPartialMessagesAndOversizeStreaming(t *testing.T) {
	for _, oversize := range []bool{false, true} {
		t.Run(map[bool]string{false: "partial timeout", true: "oversized frame"}[oversize], func(t *testing.T) {
			closed := make(chan struct{})
			server := wsServer(t, func(c *websocket.Conn, _ *http.Request) {
				_, _, _ = c.ReadMessage()
				// Advertise 64KiB but send only a prefix: the client must not drain it.
				prefix := []byte{0x82, 127, 0, 0, 0, 0, 0, 1, 0, 0}
				payload := "abc"
				if oversize {
					payload = "123456789"
				}
				_, _ = c.UnderlyingConn().Write(append(prefix, []byte(payload)...))
				_, _ = io.Copy(io.Discard, c.UnderlyingConn())
				close(closed)
			})
			s := serviceFor(t, wsURL(server), 8)
			out, e := s.Send(context.Background(), draft(wsURL(server)))
			want := "window_complete"
			size := int64(3)
			if oversize {
				want = "size_limit"
				size = 9
			}
			if e != nil || out.Outcome != want || len(out.Messages) != 1 || !out.Messages[0].Truncated || out.Messages[0].Size != size {
				t.Fatalf("%+v %v", out, e)
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("oversized socket not closed")
			}
		})
	}
}

func TestNoEnvironmentProxyAndUnsolicitedMessages(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("ALL_PROXY", "http://127.0.0.1:1")
	server := wsServer(t, func(c *websocket.Conn, _ *http.Request) {
		_ = c.WriteMessage(websocket.TextMessage, []byte("event"))
		_, _, _ = c.ReadMessage()
		_ = c.WriteMessage(websocket.TextMessage, []byte{0xff})
		_ = c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(1000, ""), time.Now().Add(time.Second))
	})
	s := serviceFor(t, wsURL(server), 1024)
	out, e := s.Send(context.Background(), draft(wsURL(server)))
	if e != nil || !out.Sent || len(out.Messages) != 2 || out.Messages[0].Payload != "event" || out.Messages[1].Payload != "ff" || out.Messages[1].PayloadFormat != "hex" {
		t.Fatalf("%+v %v", out, e)
	}
}

func TestOutgoingHardCapAndZero(t *testing.T) {
	r := draft("ws://example.com")
	r.Payload = strings.Repeat("a", 1<<20)
	if e := Validate(r, 1<<22); e != nil {
		t.Fatal(e)
	}
	r.Payload += "a"
	if !errors.Is(Validate(r, 1<<22), ErrInvalid) {
		t.Fatal("hard cap bypassed")
	}
	r.Payload = ""
	if e := Validate(r, 0); e != nil {
		t.Fatal(e)
	}
	r.Payload = "x"
	if !errors.Is(Validate(r, 0), ErrInvalid) {
		t.Fatal("zero cap bypassed")
	}
}

func draft(u string) Request {
	return Request{URL: u, Type: "text", Payload: "hello", PayloadFormat: "text"}
}

func serviceFor(t *testing.T, u string, limit int64) *Service {
	t.Helper()
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	rules, err := scope.Compile(1, []scope.Rule{{Enabled: true, Action: scope.ActionInclude, HostPattern: parsed.Hostname()}})
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(scope.NewManager(rules), limit)
	s.receiveWindow = 60 * time.Millisecond
	return s
}

func wsServer(t *testing.T, fn func(*websocket.Conn, *http.Request)) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{Subprotocols: []string{"chat"}}
		c, e := up.Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer c.Close()
		fn(c, r)
	}))
	t.Cleanup(server.Close)
	return server
}
func wsURL(s *httptest.Server) string { return "ws" + strings.TrimPrefix(s.URL, "http") }

func TestValidate(t *testing.T) {
	good := draft("ws://example.com/socket")
	if err := Validate(good, 1024); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*Request){
		"scheme":             func(r *Request) { r.URL = "http://example.com" },
		"userinfo":           func(r *Request) { r.URL = "ws://user:secret@example.com" },
		"fragment":           func(r *Request) { r.URL += "#" },
		"port zero":          func(r *Request) { r.URL = "ws://example.com:0" },
		"port big":           func(r *Request) { r.URL = "ws://example.com:65536" },
		"port empty":         func(r *Request) { r.URL = "ws://example.com:" },
		"host empty":         func(r *Request) { r.URL = "ws:///socket" },
		"type":               func(r *Request) { r.Type = "ping" },
		"format":             func(r *Request) { r.PayloadFormat = "base64" },
		"hex odd":            func(r *Request) { r.PayloadFormat = "hex"; r.Payload = "abc" },
		"hex whitespace":     func(r *Request) { r.PayloadFormat = "hex"; r.Payload = "aa bb" },
		"utf8":               func(r *Request) { r.Payload = "\xff" },
		"hex utf8":           func(r *Request) { r.PayloadFormat = "hex"; r.Payload = "ff" },
		"oversize":           func(r *Request) { r.Payload = strings.Repeat("x", 1025) },
		"header name":        func(r *Request) { r.Headers = map[string][]string{"bad name": {"x"}} },
		"header value":       func(r *Request) { r.Headers = map[string][]string{"Authorization": {"secret\r\nx:y"}} },
		"headers size":       func(r *Request) { r.Headers = map[string][]string{"X-Test": {strings.Repeat("a", 16384)}} },
		"protocol token":     func(r *Request) { r.Subprotocols = []string{"bad protocol"} },
		"protocol duplicate": func(r *Request) { r.Subprotocols = []string{"chat", "chat"} },
		"protocol size":      func(r *Request) { r.Subprotocols = []string{strings.Repeat("a", 16385)} },
	}
	for _, name := range []string{"Host", "sEc-WeBsOcKeT-Key", "Sec-WebSocket-Future", "Connection", "Upgrade", "Content-Length", "Transfer-Encoding", "Proxy-Authorization", "Proxy-Connection", "Keep-Alive", "TE", "Trailer"} {
		cases[name] = func(r *Request) { r.Headers = map[string][]string{name: {"x"}} }
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := good
			mutate(&r)
			if !errors.Is(Validate(r, 1024), ErrInvalid) {
				t.Fatal("expected invalid")
			}
		})
	}
	binary := good
	binary.Type = "binary"
	binary.PayloadFormat = "hex"
	binary.Payload = "00ff"
	if err := Validate(binary, 2); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(Validate(binary, 1), ErrInvalid) {
		t.Fatal("decoded limit")
	}
}

func TestTransfer(t *testing.T) {
	for _, typ := range []string{"text", "binary"} {
		t.Run(typ, func(t *testing.T) {
			server := wsServer(t, func(c *websocket.Conn, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("Cookie") != "sid=secret" {
					t.Error("headers missing")
				}
				if r.Header.Get("Sec-WebSocket-Extensions") != "" {
					t.Error("compression enabled")
				}
				mt, p, e := c.ReadMessage()
				if e != nil {
					t.Error(e)
					return
				}
				_ = c.WriteMessage(mt, p)
				_ = c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(1000, "secret close reason"), time.Now().Add(time.Second))
			})
			s := serviceFor(t, wsURL(server), 1024)
			r := draft(wsURL(server))
			r.Type = typ
			r.Subprotocols = []string{"chat"}
			r.Headers = map[string][]string{"Authorization": {"Bearer secret"}, "Cookie": {"sid=secret"}}
			if typ == "binary" {
				r.PayloadFormat = "hex"
				r.Payload = "00ff"
			}
			out, e := s.Send(context.Background(), r)
			if e != nil || !out.Sent || out.Outcome != "peer_closed" || out.Subprotocol != "chat" || len(out.Messages) != 1 {
				t.Fatalf("%+v %v", out, e)
			}
			m := out.Messages[0]
			if m.Type != typ || m.Payload != r.Payload || m.Truncated {
				t.Fatalf("%+v", m)
			}
		})
	}
}

func TestScopeAndRedirect(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "http://127.0.0.1/secret", 302)
	}))
	defer server.Close()
	s := serviceFor(t, wsURL(server), 1024)
	out, e := s.Send(context.Background(), draft(wsURL(server)))
	if e != nil || out.Outcome != "handshake_error" || hits.Load() != 1 {
		t.Fatalf("%+v %v", out, e)
	}
	s.manager.Replace(scope.NewManager(nil).Current())
	if _, e = s.Send(context.Background(), draft(wsURL(server))); !errors.Is(e, ErrOutOfScope) || hits.Load() != 1 {
		t.Fatalf("scope: %v hits %d", e, hits.Load())
	}
}

func TestLimits(t *testing.T) {
	for _, tc := range []struct {
		name        string
		limit       int64
		count, size int
		outcome     string
		want        int
	}{
		{"per message", 8, 1, 40, "size_limit", 1},
		{"message count", 1024, 25, 1, "message_limit", 20},
		{"aggregate", 1 << 20, 3, 600000, "size_limit", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := wsServer(t, func(c *websocket.Conn, _ *http.Request) {
				_, _, _ = c.ReadMessage()
				for i := 0; i < tc.count; i++ {
					if c.WriteMessage(websocket.BinaryMessage, []byte(strings.Repeat("x", tc.size))) != nil {
						return
					}
				}
				_, _, _ = c.ReadMessage()
			})
			s := serviceFor(t, wsURL(server), tc.limit)
			s.receiveWindow = time.Second
			out, e := s.Send(context.Background(), draft(wsURL(server)))
			if e != nil || out.Outcome != tc.outcome || len(out.Messages) != tc.want {
				t.Fatalf("%+v %v", out, e)
			}
			var retained int
			for _, m := range out.Messages {
				p, e := hex.DecodeString(m.Payload)
				if e != nil {
					t.Fatal(e)
				}
				retained += len(p)
				if int64(len(p)) > tc.limit {
					t.Fatal("message over limit")
				}
			}
			if retained > 1<<20 {
				t.Fatal("aggregate over limit")
			}
			if tc.outcome == "size_limit" && !out.Messages[len(out.Messages)-1].Truncated {
				t.Fatal("missing truncation")
			}
		})
	}
}

func TestWindowTimeoutCancellationAndBusy(t *testing.T) {
	closed := make(chan struct{}, 16)
	server := wsServer(t, func(c *websocket.Conn, _ *http.Request) {
		defer func() { closed <- struct{}{} }()
		for {
			if _, _, e := c.ReadMessage(); e != nil {
				return
			}
		}
	})
	s := serviceFor(t, wsURL(server), 1024)
	out, e := s.Send(context.Background(), draft(wsURL(server)))
	if e != nil || out.Outcome != "window_complete" || !out.Sent {
		t.Fatalf("%+v %v", out, e)
	}
	s.overallTimeout = 20 * time.Millisecond
	out, e = s.Send(context.Background(), draft(wsURL(server)))
	if e != nil || out.Outcome != "timeout" {
		t.Fatalf("%+v %v", out, e)
	}
	s.overallTimeout = time.Second
	s.receiveWindow = time.Second
	ctx, cancel := context.WithCancel(context.Background())
	results := make(chan Result, 4)
	for i := 0; i < 4; i++ {
		go func() { r, _ := s.Send(ctx, draft(wsURL(server))); results <- r }()
	}
	deadline := time.Now().Add(time.Second)
	for len(s.slots) != 4 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if _, e = s.Send(context.Background(), draft(wsURL(server))); !errors.Is(e, ErrBusy) {
		t.Fatalf("busy: %v", e)
	}
	cancel()
	for i := 0; i < 4; i++ {
		select {
		case r := <-results:
			if r.Outcome != "canceled" {
				t.Fatalf("%+v", r)
			}
		case <-time.After(time.Second):
			t.Fatal("cancel stuck")
		}
	}
	for i := 0; i < 2; i++ {
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatal("socket not closed")
		}
	}
}

func TestTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{}
		c, e := up.Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer c.Close()
		mt, p, e := c.ReadMessage()
		if e == nil {
			_ = c.WriteMessage(mt, p)
		}
	}))
	defer server.Close()
	s := serviceFor(t, wsURL(server), 1024)
	out, e := s.Send(context.Background(), draft(wsURL(server)))
	if e != nil || out.Sent || out.Outcome != "tls_error" {
		t.Fatalf("%+v %v", out, e)
	}
	s.roots = x509.NewCertPool()
	s.roots.AddCert(server.Certificate())
	out, e = s.Send(context.Background(), draft(wsURL(server)))
	if e != nil || !out.Sent || len(out.Messages) != 1 {
		t.Fatalf("%+v %v", out, e)
	}
}
