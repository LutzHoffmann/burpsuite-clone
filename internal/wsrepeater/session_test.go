package wsrepeater

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lutzifer/burpsuite-clone/internal/scope"
)

func sessionManagerFor(t *testing.T, u string, limit int64) *SessionManager {
	t.Helper()
	m := NewSessionManager(serviceFor(t, u, limit).manager, limit)
	t.Cleanup(m.Close)
	return m
}

func awaitSnapshot(t *testing.T, m *SessionManager, id string, ready func(SessionSnapshot) bool) SessionSnapshot {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s, e := m.Poll(id, 0)
		if e != nil {
			t.Fatal(e)
		}
		if ready(s) {
			return s
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("session did not reach expected state")
	return SessionSnapshot{}
}

func TestSessionPersistentExchangeAndDetachedContext(t *testing.T) {
	server := wsServer(t, func(c *websocket.Conn, _ *http.Request) {
		_ = c.WriteMessage(websocket.TextMessage, []byte("unsolicited"))
		for {
			mt, p, e := c.ReadMessage()
			if e != nil {
				return
			}
			if c.WriteMessage(mt, p) != nil {
				return
			}
		}
	})
	m := sessionManagerFor(t, wsURL(server), 1024)
	ctx, cancel := context.WithCancel(context.Background())
	s, e := m.Connect(ctx, ConnectRequest{URL: wsURL(server), Subprotocols: []string{"chat"}})
	cancel()
	if e != nil || s.State != "connected" || s.Subprotocol != "chat" || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(s.ID) {
		t.Fatalf("%+v %v", s, e)
	}
	for _, r := range []SessionSendRequest{{Type: "text", Payload: "hello", PayloadFormat: "text"}, {Type: "binary", Payload: "00ff", PayloadFormat: "hex"}} {
		if _, e = m.Send(context.Background(), s.ID, r); e != nil {
			t.Fatal(e)
		}
	}
	s = awaitSnapshot(t, m, s.ID, func(s SessionSnapshot) bool { return s.LatestSequence == 5 })
	if len(s.Messages) != 5 {
		t.Fatalf("%+v", s)
	}
	var outgoing int
	for i, msg := range s.Messages {
		if msg.Sequence != uint64(i+1) || !msg.Complete || msg.Timestamp.IsZero() {
			t.Fatalf("%+v", msg)
		}
		if msg.Direction == "client-to-server" {
			outgoing++
		}
	}
	if outgoing != 2 {
		t.Fatal(outgoing)
	}
	s, e = m.CloseSession(s.ID)
	if e != nil || s.State != "closed" || s.Reason != "operator_closed" {
		t.Fatalf("%+v %v", s, e)
	}
	if _, e = m.Send(context.Background(), s.ID, SessionSendRequest{Type: "text", PayloadFormat: "text"}); !errors.Is(e, ErrSessionConflict) {
		t.Fatal(e)
	}
	if e = m.Dispose(s.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = m.Poll(s.ID, 0); !errors.Is(e, ErrSessionNotFound) {
		t.Fatal(e)
	}
}

func TestSessionCapacityRetentionAndRaces(t *testing.T) {
	server := wsServer(t, func(c *websocket.Conn, _ *http.Request) {
		for {
			if _, _, e := c.ReadMessage(); e != nil {
				return
			}
		}
	})
	m := sessionManagerFor(t, wsURL(server), 1024)
	var ids []string
	for i := 0; i < 4; i++ {
		s, e := m.Connect(context.Background(), ConnectRequest{URL: wsURL(server)})
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, s.ID)
	}
	if _, e := m.Connect(context.Background(), ConnectRequest{URL: wsURL(server)}); !errors.Is(e, ErrBusy) {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	for _, id := range ids {
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				_, _ = m.Send(context.Background(), id, SessionSendRequest{Type: "text", PayloadFormat: "text"})
				_, _ = m.CloseSession(id)
				_, _ = m.Poll(id, 0)
			}(id)
		}
	}
	wg.Wait()
	s, e := m.Connect(context.Background(), ConnectRequest{URL: wsURL(server)})
	if e != nil || s.State != "connected" {
		t.Fatalf("%+v %v", s, e)
	}
	_, _ = m.CloseSession(s.ID)
	m.mu.Lock()
	n := len(m.sessions)
	active := m.active
	m.mu.Unlock()
	if n != 4 || active != 0 {
		t.Fatalf("retained=%d active=%d", n, active)
	}
	m.Close()
	m.Close()
	if _, e = m.Connect(context.Background(), ConnectRequest{URL: wsURL(server)}); !errors.Is(e, ErrSessionConflict) {
		t.Fatal(e)
	}
}

func TestSessionExpiryAndScope(t *testing.T) {
	for _, reason := range []string{"idle_timeout", "lifetime_expired", "lease_expired", "scope_revoked"} {
		t.Run(reason, func(t *testing.T) {
			server := wsServer(t, func(c *websocket.Conn, _ *http.Request) { _, _, _ = c.ReadMessage() })
			m := sessionManagerFor(t, wsURL(server), 1024)
			s, e := m.Connect(context.Background(), ConnectRequest{URL: wsURL(server)})
			if e != nil {
				t.Fatal(e)
			}
			m.mu.Lock()
			session := m.sessions[s.ID]
			now := time.Now()
			switch reason {
			case "idle_timeout":
				session.lastSend = now.Add(-6 * time.Minute)
			case "lifetime_expired":
				session.started = now.Add(-31 * time.Minute)
			case "lease_expired":
				session.lastPoll = now.Add(-31 * time.Second)
			}
			m.mu.Unlock()
			if reason == "scope_revoked" {
				m.scope.Replace(scope.NewManager(nil).Current())
			}
			m.sweep(now)
			s = awaitSnapshot(t, m, s.ID, func(s SessionSnapshot) bool { return s.State == "closed" })
			if s.Reason != reason {
				t.Fatalf("%+v", s)
			}
			m.sweep(now.Add(61 * time.Second))
			if _, e = m.Poll(s.ID, 0); !errors.Is(e, ErrSessionNotFound) {
				t.Fatal(e)
			}
		})
	}
}

func TestSessionRingAndEncodedBudget(t *testing.T) {
	m := NewSessionManager(nil, maxPayload)
	defer m.Close()
	s := &session{id: "test", url: "ws://example.com", state: "connected"}
	m.mu.Lock()
	for i := 0; i < 300; i++ {
		m.appendLocked(s, Message{Type: "text", PayloadFormat: "text", Payload: "x", Size: 1}, "server-to-client", true)
	}
	snap := m.snapshotLocked(s, 0)
	if len(s.messages) != 256 || snap.DroppedMessages != 44 || snap.OldestSequence != 45 || len(snap.Messages) != 100 || snap.NextSequence != 144 {
		t.Fatalf("%+v", snap)
	}
	snap = m.snapshotLocked(s, 144)
	if snap.Messages[0].Sequence != 145 {
		t.Fatal(snap.Messages[0])
	}
	m.appendLocked(s, Message{Type: "text", PayloadFormat: "text", Payload: strings.Repeat("\x00", int(maxPayload)), Size: maxPayload}, "server-to-client", true)
	snap = m.snapshotLocked(s, 0)
	m.mu.Unlock()
	if len(snap.Messages) != 1 || snap.DroppedMessages != 300 || snap.LatestSequence != 301 {
		t.Fatalf("count=%d dropped=%d", len(snap.Messages), snap.DroppedMessages)
	}
	encoded, e := json.Marshal(snap)
	if e != nil || len(encoded) > 8<<20 {
		t.Fatalf("encoded=%d %v", len(encoded), e)
	}
}

func TestSessionOversizeAndPartial(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "oversize", true: "partial"}[partial], func(t *testing.T) {
			server := wsServer(t, func(c *websocket.Conn, _ *http.Request) {
				if partial {
					_, _ = c.UnderlyingConn().Write([]byte{0x82, 10, 'a', 'b'})
					return
				}
				_ = c.WriteMessage(websocket.BinaryMessage, []byte("123456789"))
				_, _, _ = c.ReadMessage()
			})
			m := sessionManagerFor(t, wsURL(server), 8)
			s, e := m.Connect(context.Background(), ConnectRequest{URL: wsURL(server)})
			if e != nil {
				t.Fatal(e)
			}
			s = awaitSnapshot(t, m, s.ID, func(s SessionSnapshot) bool { return s.State == "closed" })
			if len(s.Messages) != 1 || s.Messages[0].Complete || !s.Messages[0].Truncated {
				t.Fatalf("%+v", s)
			}
			if !partial && s.Reason != "size_limit" {
				t.Fatal(s.Reason)
			}
		})
	}
}

func TestSessionFutureCursorRejectedWithoutRenewal(t *testing.T) {
	server := wsServer(t, func(c *websocket.Conn, _ *http.Request) { _, _, _ = c.ReadMessage() })
	m := sessionManagerFor(t, wsURL(server), 1024)
	s, e := m.Connect(context.Background(), ConnectRequest{URL: wsURL(server)})
	if e != nil {
		t.Fatal(e)
	}
	m.mu.Lock()
	before := m.sessions[s.ID].lastPoll
	m.mu.Unlock()
	if _, e = m.Poll(s.ID, 1); !errors.Is(e, ErrInvalid) {
		t.Fatalf("future cursor: %v", e)
	}
	m.mu.Lock()
	after := m.sessions[s.ID].lastPoll
	m.mu.Unlock()
	if before != after {
		t.Fatal("invalid poll renewed lease")
	}
}

func TestSessionWSAndVerifiedWSS(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(map[bool]string{false: "ws", true: "wss"}[secure], func(t *testing.T) {
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				up := websocket.Upgrader{}
				c, e := up.Upgrade(w, r, nil)
				if e != nil {
					return
				}
				defer c.Close()
				for {
					mt, p, e := c.ReadMessage()
					if e != nil {
						return
					}
					if c.WriteMessage(mt, p) != nil {
						return
					}
				}
			}))
			if secure {
				server.StartTLS()
			} else {
				server.Start()
			}
			defer server.Close()
			m := sessionManagerFor(t, wsURL(server), 1024)
			if secure {
				s, e := m.Connect(context.Background(), ConnectRequest{URL: wsURL(server)})
				if e != nil || s.Reason != "tls_error" || s.State != "closed" {
					t.Fatalf("untrusted: %+v %v", s, e)
				}
				m.roots = x509.NewCertPool()
				m.roots.AddCert(server.Certificate())
			}
			s, e := m.Connect(context.Background(), ConnectRequest{URL: wsURL(server)})
			if e != nil || s.State != "connected" {
				t.Fatalf("%+v %v", s, e)
			}
			for i := 0; i < 2; i++ {
				if _, e = m.Send(context.Background(), s.ID, SessionSendRequest{Type: "binary", PayloadFormat: "hex", Payload: "00ff"}); e != nil {
					t.Fatal(e)
				}
			}
			awaitSnapshot(t, m, s.ID, func(s SessionSnapshot) bool { return s.LatestSequence == 4 })
		})
	}
}

func TestSessionConnectCancellationTimeoutAndShutdown(t *testing.T) {
	for _, action := range []string{"cancel", "timeout", "shutdown"} {
		t.Run(action, func(t *testing.T) {
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
			m := sessionManagerFor(t, wsURL(server), 1024)
			m.connectTimeout = 100 * time.Millisecond
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan SessionSnapshot, 1)
			go func() {
				s, e := m.Connect(ctx, ConnectRequest{URL: wsURL(server)})
				if e != nil {
					t.Error(e)
				}
				result <- s
			}()
			<-entered
			want := "timeout"
			if action == "cancel" {
				cancel()
				want = "canceled"
			}
			if action == "shutdown" {
				m.Close()
				want = "shutdown"
			}
			select {
			case s := <-result:
				if s.State != "closed" || s.Reason != want {
					t.Fatalf("%+v", s)
				}
			case <-time.After(time.Second):
				t.Fatal("connect stuck")
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("transport leaked")
			}
			m.mu.Lock()
			active := m.active
			m.mu.Unlock()
			if active != 0 {
				t.Fatal(active)
			}
		})
	}
}

func TestSessionBlockedSendBusyTimeoutCancellationAndClose(t *testing.T) {
	for _, action := range []string{"timeout", "cancel", "dispose", "shutdown", "reader-first"} {
		t.Run(action, func(t *testing.T) {
			ready := make(chan struct{})
			release := make(chan struct{})
			server := wsServer(t, func(c *websocket.Conn, _ *http.Request) {
				_ = c.UnderlyingConn().(*net.TCPConn).SetReadBuffer(1024)
				close(ready)
				<-release
			})
			defer close(release)
			m := sessionManagerFor(t, wsURL(server), maxPayload)
			m.writeTimeout = 200 * time.Millisecond
			s, e := m.Connect(context.Background(), ConnectRequest{URL: wsURL(server)})
			if e != nil {
				t.Fatal(e)
			}
			<-ready
			m.mu.Lock()
			c := m.sessions[s.ID].conn.UnderlyingConn().(*handshakeConn).Conn.(*net.TCPConn)
			m.mu.Unlock()
			_ = c.SetWriteBuffer(1024)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan SessionSnapshot, 1)
			go func() {
				out, e := m.Send(ctx, s.ID, SessionSendRequest{Type: "binary", PayloadFormat: "text", Payload: strings.Repeat("x", int(maxPayload))})
				if e != nil {
					t.Error(e)
				}
				result <- out
			}()
			deadline := time.Now().Add(time.Second)
			for {
				m.mu.Lock()
				writing := m.sessions[s.ID].writing
				m.mu.Unlock()
				if writing {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("write not started")
				}
				time.Sleep(time.Millisecond)
			}
			if _, e = m.Send(context.Background(), s.ID, SessionSendRequest{Type: "text", PayloadFormat: "text"}); !errors.Is(e, ErrSessionConflict) {
				t.Fatal(e)
			}
			switch action {
			case "cancel":
				cancel()
			case "dispose":
				if e = m.Dispose(s.ID); e != nil {
					t.Fatal(e)
				}
			case "shutdown":
				m.Close()
			case "reader-first":
				m.mu.Lock()
				m.terminateLocked(m.sessions[s.ID], "read_error")
				m.mu.Unlock()
			}
			select {
			case out := <-result:
				if (out.State != "closed" && out.State != "closing") || len(out.Messages) != 0 {
					t.Fatalf("%+v", out)
				}
				if (action == "timeout" || action == "cancel" || action == "reader-first") && out.Reason != "send_failed" {
					t.Fatal(out.Reason)
				}
			case <-time.After(time.Second):
				t.Fatal("send stuck")
			}
		})
	}
}

func TestSessionTerminalSnapshotWaitsForLastWriter(t *testing.T) {
	m := NewSessionManager(nil, 1024)
	defer m.Close()
	s := &session{state: "closed", reason: "read_error", writing: true}
	m.mu.Lock()
	snapshot := m.snapshotLocked(s, 0)
	m.mu.Unlock()
	if snapshot.State != "closing" {
		t.Fatalf("terminal state published before final write outcome: %s", snapshot.State)
	}
}

type gatedSessionConn struct {
	net.Conn
	readGate         bool
	writeGate        atomic.Bool
	entered, release chan struct{}
	once             sync.Once
}

func (c *gatedSessionConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if err != nil && c.readGate {
		c.once.Do(func() { close(c.entered); <-c.release })
	}
	return n, err
}

func (c *gatedSessionConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if err == nil && c.writeGate.CompareAndSwap(true, false) {
		close(c.entered)
		<-c.release
	}
	return n, err
}

func TestSessionFinalRecordsSurviveConcurrentClose(t *testing.T) {
	for _, mode := range []string{"partial read", "successful write"} {
		t.Run(mode, func(t *testing.T) {
			peerDone := make(chan struct{})
			peerWritten := make(chan struct{})
			server := wsServer(t, func(c *websocket.Conn, _ *http.Request) {
				if mode == "partial read" {
					_, _ = c.UnderlyingConn().Write([]byte{0x82, 10, 'a', 'b'})
					close(peerWritten)
					<-peerDone
				} else {
					_, _, _ = c.ReadMessage()
				}
			})
			t.Cleanup(func() { close(peerDone) })
			var transport *gatedSessionConn
			dialer := websocket.Dialer{NetDialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
				if err != nil {
					return nil, err
				}
				transport = &gatedSessionConn{Conn: conn, readGate: mode == "partial read", entered: make(chan struct{}), release: make(chan struct{})}
				return transport, nil
			}}
			conn, _, err := dialer.Dial(wsURL(server), nil)
			if err != nil {
				t.Fatal(err)
			}
			var release sync.Once
			unblock := func() { release.Do(func() { close(transport.release) }) }
			defer unblock()
			m := sessionManagerFor(t, wsURL(server), 1024)
			u, _ := url.Parse(wsURL(server))
			now := time.Now()
			s := &session{id: strings.Repeat("a", 32), url: wsURL(server), state: "connected", conn: conn, reading: true, started: now, lastSend: now, lastPoll: now, target: scope.Target{Scheme: "http", Host: u.Host, Path: u.Path}}
			m.mu.Lock()
			m.sessions[s.id] = s
			m.active++
			m.work.Add(1)
			s.work.Add(1)
			m.mu.Unlock()
			if mode == "partial read" {
				<-peerWritten
			}
			go m.read(s)
			result := make(chan SessionSnapshot, 1)
			if mode == "partial read" {
				go func() { snapshot, _ := m.CloseSession(s.id); result <- snapshot }()
			} else {
				transport.writeGate.Store(true)
				go func() {
					snapshot, _ := m.Send(context.Background(), s.id, SessionSendRequest{Type: "text", Payload: "final message", PayloadFormat: "text"})
					result <- snapshot
				}()
			}
			select {
			case <-transport.entered:
			case <-time.After(2 * time.Second):
				t.Fatal("operation did not enter transport gate")
			}
			if mode == "successful write" {
				m.mu.Lock()
				m.terminateLocked(s, "scope_revoked")
				m.mu.Unlock()
			}
			snapshot, err := m.Poll(s.id, 0)
			if err != nil || snapshot.State != "closing" {
				t.Fatalf("premature terminal publication: %+v %v", snapshot, err)
			}
			select {
			case <-result:
				t.Fatal("operation returned before final outcome")
			default:
			}
			unblock()
			select {
			case <-result:
			case <-time.After(time.Second):
				t.Fatal("operation did not join")
			}
			snapshot = awaitSnapshot(t, m, s.id, func(s SessionSnapshot) bool { return s.State == "closed" })
			if len(snapshot.Messages) != 1 {
				t.Fatalf("final record lost: %+v", snapshot)
			}
			message := snapshot.Messages[0]
			if mode == "partial read" {
				if message.Complete || !message.Truncated {
					t.Fatalf("partial read claimed complete: %+v", message)
				}
			} else if message.Payload != "final message" || !message.Complete || message.Direction != "client-to-server" {
				t.Fatalf("successful write lost: %+v", message)
			}
		})
	}
}

func TestSessionConcurrentCloseDisposeAndShutdown(t *testing.T) {
	server := wsServer(t, func(c *websocket.Conn, _ *http.Request) { _, _, _ = c.ReadMessage() })
	m := sessionManagerFor(t, wsURL(server), 1024)
	s, err := m.Connect(context.Background(), ConnectRequest{URL: wsURL(server)})
	if err != nil {
		t.Fatal(err)
	}
	var work sync.WaitGroup
	for i := 0; i < 12; i++ {
		work.Add(1)
		go func(i int) {
			defer work.Done()
			switch i % 3 {
			case 0:
				_, _ = m.CloseSession(s.ID)
			case 1:
				_ = m.Dispose(s.ID)
			case 2:
				m.Close()
			}
		}(i)
	}
	done := make(chan struct{})
	go func() { work.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("concurrent cleanup stuck")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active != 0 || len(m.sessions) != 0 {
		t.Fatalf("cleanup retained capacity: active=%d sessions=%d", m.active, len(m.sessions))
	}
}

func TestSessionSendRejectsExpiredAndRevoked(t *testing.T) {
	server := wsServer(t, func(c *websocket.Conn, _ *http.Request) { _, _, _ = c.ReadMessage() })
	m := sessionManagerFor(t, wsURL(server), 1024)
	s, e := m.Connect(context.Background(), ConnectRequest{URL: wsURL(server)})
	if e != nil {
		t.Fatal(e)
	}
	m.mu.Lock()
	m.sessions[s.ID].lastSend = time.Now().Add(-6 * time.Minute)
	m.mu.Unlock()
	out, e := m.Send(context.Background(), s.ID, SessionSendRequest{Type: "text", PayloadFormat: "text"})
	if !errors.Is(e, ErrSessionConflict) || out.Reason != "idle_timeout" {
		t.Fatalf("%+v %v", out, e)
	}
	s, e = m.Connect(context.Background(), ConnectRequest{URL: wsURL(server)})
	if e != nil {
		t.Fatal(e)
	}
	m.scope.Replace(scope.NewManager(nil).Current())
	out, e = m.Send(context.Background(), s.ID, SessionSendRequest{Type: "text", PayloadFormat: "text"})
	if !errors.Is(e, ErrOutOfScope) || out.Reason != "scope_revoked" {
		t.Fatalf("%+v %v", out, e)
	}
	if _, e = m.Connect(context.Background(), ConnectRequest{URL: wsURL(server)}); !errors.Is(e, ErrOutOfScope) {
		t.Fatal(e)
	}
}

func TestSessionSilentScopeRevocationAndHandshakeRecheck(t *testing.T) {
	for _, duringHandshake := range []bool{false, true} {
		t.Run(map[bool]string{false: "silent peer", true: "handshake"}[duringHandshake], func(t *testing.T) {
			entered := make(chan struct{})
			proceed := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				<-proceed
				up := websocket.Upgrader{}
				c, e := up.Upgrade(w, r, nil)
				if e != nil {
					return
				}
				defer c.Close()
				_, _, _ = c.ReadMessage()
			}))
			defer server.Close()
			m := sessionManagerFor(t, wsURL(server), 1024)
			result := make(chan SessionSnapshot, 1)
			go func() {
				s, e := m.Connect(context.Background(), ConnectRequest{URL: wsURL(server)})
				if e != nil {
					t.Error(e)
				}
				result <- s
			}()
			<-entered
			if duringHandshake {
				m.scope.Replace(scope.NewManager(nil).Current())
			}
			close(proceed)
			s := <-result
			if !duringHandshake {
				m.scope.Replace(scope.NewManager(nil).Current())
				deadline := time.Now().Add(2 * time.Second)
				for {
					m.mu.Lock()
					closed := m.sessions[s.ID].state == "closed"
					m.mu.Unlock()
					if closed {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("silent session not revoked by background sweep")
					}
					time.Sleep(time.Millisecond)
				}
			}
			s = awaitSnapshot(t, m, s.ID, func(s SessionSnapshot) bool { return s.State == "closed" })
			if s.Reason != "scope_revoked" {
				t.Fatalf("%+v", s)
			}
		})
	}
}
