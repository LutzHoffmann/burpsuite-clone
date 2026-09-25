package wsrepeater

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
	"github.com/lutzifer/burpsuite-clone/internal/scope"
)

var (
	ErrSessionNotFound = errors.New("WebSocket session not found")
	ErrSessionConflict = errors.New("WebSocket session is not available")
)

type ConnectRequest struct {
	URL          string              `json:"url"`
	Headers      map[string][]string `json:"headers"`
	Subprotocols []string            `json:"subprotocols"`
}

type SessionSendRequest struct {
	Type          string `json:"type"`
	Payload       string `json:"payload"`
	PayloadFormat string `json:"payloadFormat"`
}

type SessionMessage struct {
	Message
	Sequence  uint64    `json:"sequence"`
	Direction string    `json:"direction"`
	Timestamp time.Time `json:"timestamp"`
	Complete  bool      `json:"complete"`
}

type SessionSnapshot struct {
	ID              string           `json:"id"`
	URL             string           `json:"url"`
	State           string           `json:"state"`
	Reason          string           `json:"reason"`
	Subprotocol     string           `json:"subprotocol"`
	Messages        []SessionMessage `json:"messages"`
	OldestSequence  uint64           `json:"oldestSequence"`
	LatestSequence  uint64           `json:"latestSequence"`
	NextSequence    uint64           `json:"nextSequence"`
	DroppedMessages uint64           `json:"droppedMessages"`
}

type session struct {
	id, url, state, reason, subprotocol   string
	target                                scope.Target
	conn                                  *websocket.Conn
	cancel                                context.CancelFunc
	started, lastSend, lastPoll, closedAt time.Time
	writing, reading                      bool
	messages                              []SessionMessage
	bytes                                 int64
	latest, dropped                       uint64
	work                                  sync.WaitGroup
}

// All registry and session metadata is guarded by mu. No application write or
// control write holds mu; forcibly closing a transport never waits for a writer.
type SessionManager struct {
	mu                           sync.Mutex
	scope                        *scope.Manager
	bodyLimit                    int64
	sessions                     map[string]*session
	active                       int
	closed                       bool
	done                         chan struct{}
	work                         sync.WaitGroup
	roots                        *x509.CertPool
	connectTimeout, writeTimeout time.Duration
}

func NewSessionManager(manager *scope.Manager, bodyLimit int64) *SessionManager {
	m := &SessionManager{scope: manager, bodyLimit: payloadLimit(bodyLimit), sessions: make(map[string]*session), done: make(chan struct{}), connectTimeout: 15 * time.Second, writeTimeout: 5 * time.Second}
	m.work.Add(1)
	go func() {
		defer m.work.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case now := <-ticker.C:
				m.sweep(now)
			case <-m.done:
				return
			}
		}
	}()
	return m
}

func (m *SessionManager) inScope(target scope.Target) bool {
	if m.scope == nil {
		return false
	}
	rules := m.scope.Current()
	return rules != nil && rules.Classify(target).InScope
}

func (m *SessionManager) Connect(parent context.Context, r ConnectRequest) (SessionSnapshot, error) {
	if e := Validate(Request{URL: r.URL, Headers: r.Headers, Subprotocols: r.Subprotocols, Type: "text", PayloadFormat: "text"}, m.bodyLimit); e != nil {
		return SessionSnapshot{}, e
	}
	u, _ := url.Parse(r.URL)
	scheme := "http"
	if u.Scheme == "wss" {
		scheme = "https"
	}
	target := scope.Target{Scheme: scheme, Host: u.Host, Path: u.Path}
	if !m.inScope(target) {
		return SessionSnapshot{}, ErrOutOfScope
	}
	var random [16]byte
	if _, e := rand.Read(random[:]); e != nil {
		return SessionSnapshot{}, ErrSessionConflict
	}
	ctx, cancel := context.WithTimeout(parent, m.connectTimeout)
	defer cancel()
	s := &session{id: hex.EncodeToString(random[:]), url: r.URL, target: target, state: "connecting", cancel: cancel}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return SessionSnapshot{}, ErrSessionConflict
	}
	if m.active == 4 {
		m.mu.Unlock()
		return SessionSnapshot{}, ErrBusy
	}
	m.active++
	m.sessions[s.id] = s
	m.work.Add(1)
	m.mu.Unlock()
	defer m.work.Done()
	c, detach, reason := dialWebSocket(ctx, r, m.roots, m.connectTimeout)
	detach()
	m.mu.Lock()
	defer m.mu.Unlock()
	s.conn = c
	if s.state == "closing" {
		reason = s.reason
	}
	if reason == "" && ctx.Err() != nil {
		reason = networkOutcome(ctx, ctx.Err(), "handshake_error")
	}
	if reason == "" && !m.inScope(target) {
		reason = "scope_revoked"
	}
	if reason != "" {
		m.finishLocked(s, reason)
		return m.snapshotLocked(s, 0), nil
	}
	now := time.Now()
	s.started = now
	s.lastSend = now
	s.lastPoll = now
	s.state = "connected"
	s.subprotocol = c.Subprotocol()
	// Gorilla's automatic control writes must not inherit application deadlines.
	c.SetPingHandler(func(data string) error { return m.control(s, websocket.PongMessage, []byte(data)) })
	c.SetCloseHandler(func(code int, _ string) error {
		return m.control(s, websocket.CloseMessage, websocket.FormatCloseMessage(code, ""))
	})
	m.work.Add(1)
	s.work.Add(1)
	s.reading = true
	go m.read(s)
	return m.snapshotLocked(s, 0), nil
}

func (m *SessionManager) control(s *session, kind int, data []byte) error {
	e := s.conn.WriteControl(kind, data, time.Now().Add(time.Second))
	if e != nil {
		m.mu.Lock()
		m.terminateLocked(s, "control_failed")
		m.mu.Unlock()
	}
	return e
}

func (m *SessionManager) read(s *session) {
	defer m.work.Done()
	defer s.work.Done()
	defer func() {
		m.mu.Lock()
		s.reading = false
		m.mu.Unlock()
	}()
	for {
		mt, r, e := s.conn.NextReader()
		if e != nil {
			m.mu.Lock()
			m.terminateLocked(s, readOutcome(context.Background(), e, false))
			m.mu.Unlock()
			return
		}
		data, e := io.ReadAll(io.LimitReader(r, m.bodyLimit+1))
		observed := int64(len(data))
		limited := observed > m.bodyLimit
		if limited {
			data = data[:m.bodyLimit]
		}
		msg := messageFromBytes(mt, data)
		msg.Size = observed
		msg.Truncated = limited || e != nil
		complete := !limited && e == nil
		reason := ""
		if limited {
			reason = "size_limit"
		} else if e != nil {
			reason = "read_error"
		} else if mt == websocket.TextMessage && !utf8.Valid(data) {
			complete = false
			reason = "invalid_payload"
		}
		m.mu.Lock()
		m.appendLocked(s, msg, "server-to-client", complete)
		if reason != "" {
			m.terminateLocked(s, reason)
		}
		closed := s.state == "closed"
		m.mu.Unlock()
		if closed {
			return
		}
	}
}

func messageFromBytes(mt int, p []byte) Message {
	msg := Message{Type: "binary", PayloadFormat: "hex", Payload: hex.EncodeToString(p), Size: int64(len(p))}
	if mt == websocket.TextMessage {
		msg.Type = "text"
		if utf8.Valid(p) {
			msg.PayloadFormat = "text"
			msg.Payload = string(p)
		}
	}
	return msg
}

func (m *SessionManager) Send(parent context.Context, id string, r SessionSendRequest) (SessionSnapshot, error) {
	if r.Type != "text" && r.Type != "binary" {
		return SessionSnapshot{}, ErrInvalid
	}
	p, e := payload(Request{Type: r.Type, Payload: r.Payload, PayloadFormat: r.PayloadFormat}, m.bodyLimit)
	if e != nil {
		return SessionSnapshot{}, e
	}
	m.mu.Lock()
	s := m.sessions[id]
	if s == nil {
		m.mu.Unlock()
		return SessionSnapshot{}, ErrSessionNotFound
	}
	if m.closed || s.state != "connected" || s.writing {
		snap := m.snapshotLocked(s, 0)
		m.mu.Unlock()
		return snap, ErrSessionConflict
	}
	if !m.inScope(s.target) {
		m.terminateLocked(s, "scope_revoked")
		snap := m.snapshotLocked(s, 0)
		m.mu.Unlock()
		return snap, ErrOutOfScope
	}
	m.expireLocked(s, time.Now())
	if s.state != "connected" {
		snap := m.snapshotLocked(s, 0)
		m.mu.Unlock()
		return snap, ErrSessionConflict
	}
	if parent.Err() != nil {
		snap := m.snapshotLocked(s, 0)
		m.mu.Unlock()
		return snap, ErrSessionConflict
	}
	s.writing = true
	m.work.Add(1)
	s.work.Add(1)
	m.mu.Unlock()
	defer m.work.Done()
	defer s.work.Done()
	ctx, cancel := context.WithTimeout(parent, m.writeTimeout)
	defer cancel()
	// Cancellation closes the socket rather than racing SetWriteDeadline.
	callbackDone := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { defer close(callbackDone); m.mu.Lock(); m.terminateLocked(s, "send_failed"); m.mu.Unlock() })
	deadline, _ := ctx.Deadline()
	_ = s.conn.SetWriteDeadline(deadline)
	mt := websocket.TextMessage
	if r.Type == "binary" {
		mt = websocket.BinaryMessage
	}
	e = s.conn.WriteMessage(mt, p)
	if !stop() {
		<-callbackDone
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s.writing = false
	if e != nil || ctx.Err() != nil {
		m.terminateLocked(s, "send_failed")
		// The reader or an explicit close may have terminated first. The failed
		// in-flight write still needs an unambiguous unknown-delivery outcome.
		s.reason = "send_failed"
	} else {
		m.appendLocked(s, messageFromBytes(mt, p), "client-to-server", true)
		s.lastSend = time.Now()
	}
	return m.snapshotLocked(s, 0), nil
}

func decodedSize(msg Message) int64 {
	if msg.PayloadFormat == "hex" {
		return int64(len(msg.Payload) / 2)
	}
	return int64(len(msg.Payload))
}

func (m *SessionManager) appendLocked(s *session, msg Message, direction string, complete bool) {
	size := decodedSize(msg)
	for len(s.messages) > 0 && (len(s.messages) >= 256 || s.bytes+size > maxPayload) {
		s.bytes -= decodedSize(s.messages[0].Message)
		s.messages[0] = SessionMessage{}
		s.messages = s.messages[1:]
		s.dropped++
	}
	s.latest++
	s.bytes += size
	s.messages = append(s.messages, SessionMessage{Message: msg, Sequence: s.latest, Direction: direction, Timestamp: time.Now().UTC(), Complete: complete})
}

func (m *SessionManager) snapshotLocked(s *session, after uint64) SessionSnapshot {
	snap := SessionSnapshot{ID: s.id, URL: s.url, State: s.state, Reason: s.reason, Subprotocol: s.subprotocol, Messages: []SessionMessage{}, LatestSequence: s.latest, NextSequence: min(after, s.latest), DroppedMessages: s.dropped}
	// Do not advertise a final cursor while a reader/writer can still append.
	if s.state == "closed" && (s.reading || s.writing) {
		snap.State = "closing"
	}
	if len(s.messages) > 0 {
		snap.OldestSequence = s.messages[0].Sequence
	}
	for _, msg := range s.messages {
		if msg.Sequence > after {
			snap.Messages = append(snap.Messages, msg)
			snap.NextSequence = msg.Sequence
			if len(snap.Messages) == 100 {
				break
			}
		}
	}
	return snap
}

func (m *SessionManager) Poll(id string, afterSequence uint64) (SessionSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked(time.Now())
	s := m.sessions[id]
	if s == nil {
		return SessionSnapshot{}, ErrSessionNotFound
	}
	if afterSequence > s.latest {
		return SessionSnapshot{}, ErrInvalid
	}
	// Expired leases cannot be revived by a poll between sweep ticks.
	m.expireLocked(s, time.Now())
	if s.state == "connected" {
		s.lastPoll = time.Now()
	}
	return m.snapshotLocked(s, afterSequence), nil
}

func (m *SessionManager) CloseSession(id string) (SessionSnapshot, error) {
	m.mu.Lock()
	s := m.sessions[id]
	if s == nil {
		m.mu.Unlock()
		return SessionSnapshot{}, ErrSessionNotFound
	}
	if s.state != "connected" {
		snap := m.snapshotLocked(s, 0)
		m.mu.Unlock()
		return snap, nil
	}
	s.state = "closing"
	s.reason = "operator_closed"
	m.work.Add(1)
	s.work.Add(1)
	m.mu.Unlock()
	defer m.work.Done()
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	m.mu.Lock()
	m.terminateLocked(s, "operator_closed")
	m.mu.Unlock()
	s.work.Done()
	s.work.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked(s, 0), nil
}

func (m *SessionManager) Dispose(id string) error {
	m.mu.Lock()
	s := m.sessions[id]
	if s == nil {
		m.mu.Unlock()
		return ErrSessionNotFound
	}
	m.terminateLocked(s, "disposed")
	delete(m.sessions, id)
	m.mu.Unlock()
	s.work.Wait()
	return nil
}

func (m *SessionManager) terminateLocked(s *session, reason string) {
	if s.state == "closed" {
		return
	}
	if s.state == "connecting" || (s.state == "closing" && s.conn == nil) {
		s.state = "closing"
		s.reason = reason
		s.cancel()
		return
	}
	if s.state == "closing" && s.reason != "" {
		reason = s.reason
	}
	m.finishLocked(s, reason)
}

func (m *SessionManager) finishLocked(s *session, reason string) {
	if s.state == "closed" {
		return
	}
	if s.conn != nil {
		_ = s.conn.Close()
	}
	s.state = "closed"
	s.reason = reason
	s.closedAt = time.Now()
	m.active--
	if s.cancel != nil {
		s.cancel()
	}
	m.pruneLocked(s.closedAt)
}

func (m *SessionManager) pruneLocked(now time.Time) {
	var retained []*session
	for id, s := range m.sessions {
		if s.state == "closed" {
			if now.Sub(s.closedAt) >= 60*time.Second {
				delete(m.sessions, id)
			} else {
				retained = append(retained, s)
			}
		}
	}
	for len(retained) > 4 {
		oldest := 0
		for i := range retained {
			if retained[i].closedAt.Before(retained[oldest].closedAt) {
				oldest = i
			}
		}
		delete(m.sessions, retained[oldest].id)
		retained = append(retained[:oldest], retained[oldest+1:]...)
	}
}

func (m *SessionManager) expireLocked(s *session, now time.Time) {
	if s.state != "connected" {
		return
	}
	reason := ""
	switch {
	case !m.inScope(s.target):
		reason = "scope_revoked"
	case now.Sub(s.started) >= 30*time.Minute:
		reason = "lifetime_expired"
	case now.Sub(s.lastSend) >= 5*time.Minute:
		reason = "idle_timeout"
	case now.Sub(s.lastPoll) >= 30*time.Second:
		reason = "lease_expired"
	}
	if reason != "" {
		m.terminateLocked(s, reason)
	}
}

func (m *SessionManager) sweep(now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		m.expireLocked(s, now)
	}
	m.pruneLocked(now)
}

// Close prevents new work, interrupts every transport, then joins owned work.
func (m *SessionManager) Close() {
	m.mu.Lock()
	if !m.closed {
		m.closed = true
		close(m.done)
		for _, s := range m.sessions {
			m.terminateLocked(s, "shutdown")
		}
	}
	m.mu.Unlock()
	m.work.Wait()
	m.mu.Lock()
	clear(m.sessions)
	m.mu.Unlock()
}
