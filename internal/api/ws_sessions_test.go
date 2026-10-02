package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lutzifer/burpsuite-clone/internal/scope"
	"github.com/lutzifer/burpsuite-clone/internal/store"
	"github.com/lutzifer/burpsuite-clone/internal/wsrepeater"
)

func TestWSSessionRoutesRejectForeignOrigin(t *testing.T) {
	s := NewServer(Config{})
	for _, tc := range []struct{ method, suffix string }{
		{"POST", ""}, {"GET", "/" + strings.Repeat("a", 32)},
		{"POST", "/" + strings.Repeat("a", 32) + "/send"},
		{"POST", "/" + strings.Repeat("a", 32) + "/close"},
		{"DELETE", "/" + strings.Repeat("a", 32)},
	} {
		t.Run(tc.method+tc.suffix, func(t *testing.T) {
			w := serveAPIRequestWithOrigin(s, tc.method, "/api/websocket-repeater/sessions"+tc.suffix, `{}`, "application/json", "https://evil.test")
			if w.Code != http.StatusForbidden {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("missing no-store")
			}
		})
	}
}

func TestWSSessionUnavailable(t *testing.T) {
	s := NewServer(Config{})
	w := storageRequest(s, "POST", "/api/websocket-repeater/sessions", `{}`)
	if w.Code != 503 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("got %d %v", w.Code, w.Header())
	}
}

type fakeWSSessions struct {
	calls  int
	cursor uint64
	result wsrepeater.SessionSnapshot
	err    error
}

func (f *fakeWSSessions) Connect(context.Context, wsrepeater.ConnectRequest) (wsrepeater.SessionSnapshot, error) {
	f.calls++
	return f.result, f.err
}
func (f *fakeWSSessions) Send(context.Context, string, wsrepeater.SessionSendRequest) (wsrepeater.SessionSnapshot, error) {
	f.calls++
	return f.result, f.err
}
func (f *fakeWSSessions) Poll(_ string, cursor uint64) (wsrepeater.SessionSnapshot, error) {
	f.calls++
	f.cursor = cursor
	return f.result, f.err
}
func (f *fakeWSSessions) CloseSession(string) (wsrepeater.SessionSnapshot, error) {
	f.calls++
	return f.result, f.err
}
func (f *fakeWSSessions) Dispose(string) error { f.calls++; return f.err }

func TestWSSessionStrictInput(t *testing.T) {
	id := strings.Repeat("a", 32)
	base := "/api/websocket-repeater/sessions"
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"POST", base, `null`, 400},
		{"POST", base, `{"unknown":true}`, 400},
		{"POST", base, `{"url":"a","url":"b"}`, 400},
		{"POST", base, `{"headers":{"X-Test":["a"],"X-Test":["b"]}}`, 400},
		{"POST", base, `{"url":"\ud800"}`, 400},
		{"POST", base, `{} {}`, 400},
		{"POST", base, strings.Repeat(" ", 3<<20) + `{}`, 413},
		{"POST", base + "?x=1", `{}`, 400},
		{"POST", base + "/" + id + "/send", `{"url":"ws://evil.test"}`, 400},
		{"POST", base + "/" + id + "/send", `{"payload":"\udc00"}`, 400},
		{"POST", base + "/" + id + "/send", `null`, 400},
		{"GET", base + "/short", "", 400},
		{"GET", base + "/" + strings.Repeat("A", 32), "", 400},
		{"GET", base + "/" + id + "?afterSequence=1&afterSequence=2", "", 400},
		{"GET", base + "/" + id + "?afterSequence=", "", 400},
		{"GET", base + "/" + id + "?afterSequence=-1", "", 400},
		{"GET", base + "/" + id + "?afterSequence=%2B1", "", 400},
		{"GET", base + "/" + id + "?afterSequence=18446744073709551616", "", 400},
		{"GET", base + "/" + id + "?afterSequence=1;2", "", 400},
		{"GET", base + "/" + id + "?unknown=1", "", 400},
		{"GET", base + "/" + id, `{}`, 400},
		{"POST", base + "/" + id + "/close", `{}`, 400},
		{"DELETE", base + "/" + id, `{}`, 400},
	} {
		t.Run(tc.method+tc.path+tc.body[:min(30, len(tc.body))], func(t *testing.T) {
			fake := &fakeWSSessions{}
			s := NewServer(Config{WSSessions: fake})
			w := storageRequest(s, tc.method, tc.path, tc.body)
			if w.Code != tc.status || fake.calls != 0 {
				t.Fatalf("got %d calls=%d: %s", w.Code, fake.calls, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("missing no-store")
			}
		})
	}
}

func TestWSSessionStatusAndCursor(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
	}{
		{nil, 200}, {wsrepeater.ErrInvalid, 400}, {wsrepeater.ErrOutOfScope, 403},
		{wsrepeater.ErrSessionNotFound, 404}, {wsrepeater.ErrSessionConflict, 409},
		{wsrepeater.ErrBusy, 429}, {errors.New("secret peer error"), 503},
	} {
		fake := &fakeWSSessions{err: tc.err}
		s := NewServer(Config{WSSessions: fake})
		w := storageRequest(s, "GET", "/api/websocket-repeater/sessions/"+strings.Repeat("0", 32)+"?afterSequence=42", "")
		if w.Code != tc.code || fake.cursor != 42 || fake.calls != 1 {
			t.Fatalf("got %d cursor=%d calls=%d", w.Code, fake.cursor, fake.calls)
		}
		if strings.Contains(w.Body.String(), "secret") {
			t.Fatal("leaked upstream error")
		}
	}
}

func TestWSSessionResponseWorstCaseEscaping(t *testing.T) {
	fake := &fakeWSSessions{result: wsrepeater.SessionSnapshot{
		ID: strings.Repeat("a", 32), URL: strings.Repeat("<", 64<<10), State: "connected", Subprotocol: strings.Repeat("x", 4<<10),
		Messages:       []wsrepeater.SessionMessage{{Message: wsrepeater.Message{Type: "text", Payload: strings.Repeat("\x00", 1<<20), PayloadFormat: "text", Size: 1 << 20}, Sequence: 1, Complete: true}},
		LatestSequence: 1, NextSequence: 1, OldestSequence: 1,
	}}
	s := NewServer(Config{WSSessions: fake})
	w := storageRequest(s, "GET", "/api/websocket-repeater/sessions/"+fake.result.ID, "")
	if w.Code != 200 || w.Body.Len() > 8<<20 || w.Body.Len() < 6<<20 {
		t.Fatalf("code=%d bytes=%d", w.Code, w.Body.Len())
	}
	var result wsrepeater.SessionSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || len(result.Messages) != 1 || result.Messages[0].Payload != fake.result.Messages[0].Payload {
		t.Fatalf("response corrupted: %v", err)
	}
	fake.result.Messages[0].Payload = strings.Repeat("\x00", 2<<20)
	w = storageRequest(s, "GET", "/api/websocket-repeater/sessions/"+fake.result.ID, "")
	if w.Code != 500 {
		t.Fatalf("oversize response: %d", w.Code)
	}
}

func TestWSSessionCloseAndDispose(t *testing.T) {
	fake := &fakeWSSessions{}
	s := NewServer(Config{WSSessions: fake})
	base := "/api/websocket-repeater/sessions/" + strings.Repeat("a", 32)
	if w := storageRequest(s, "POST", base+"/close", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := storageRequest(s, "DELETE", base, ""); w.Code != 204 || w.Body.Len() != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	if fake.calls != 2 {
		t.Fatal(fake.calls)
	}
}

func TestWSSessionAPIIndependentExchange(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer local-test" {
			http.Error(w, "auth", 401)
			return
		}
		upgrader := websocket.Upgrader{}
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			kind, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			if err := c.WriteMessage(kind, data); err != nil {
				return
			}
		}
	}))
	defer peer.Close()
	rules, err := scope.Compile(1, []scope.Rule{{Enabled: true, Action: scope.ActionInclude, HostPattern: "127.0.0.1", PathPrefix: "/"}})
	if err != nil {
		t.Fatal(err)
	}
	scopeManager := scope.NewManager(rules)
	manager := wsrepeater.NewSessionManager(scopeManager, 1024)
	defer manager.Close()
	repo, err := store.OpenSQLite(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	before, err := repo.StorageStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(Config{WSSessions: manager, Store: repo})
	payload, _ := json.Marshal(wsrepeater.ConnectRequest{URL: strings.Replace(peer.URL, "http", "ws", 1), Headers: map[string][]string{"Authorization": {"Bearer local-test"}}})
	base := "/api/websocket-repeater/sessions"
	w := storageRequest(server, "POST", base, string(payload))
	if w.Code != 200 {
		t.Fatalf("connect %d %s", w.Code, w.Body.String())
	}
	var snapshot wsrepeater.SessionSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.State != "connected" || len(snapshot.ID) != 32 || len(snapshot.Messages) != 0 {
		t.Fatalf("unexpected connect: %+v", snapshot)
	}
	path := base + "/" + snapshot.ID
	for _, message := range []string{"login", "next command"} {
		payload, _ = json.Marshal(wsrepeater.SessionSendRequest{Type: "text", Payload: message, PayloadFormat: "text"})
		w = storageRequest(server, "POST", path+"/send", string(payload))
		if w.Code != 200 {
			t.Fatalf("send %d %s", w.Code, w.Body.String())
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		w = storageRequest(server, "GET", path+"?afterSequence=0", "")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil {
			t.Fatal(err)
		}
		if len(snapshot.Messages) == 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("missing records: %+v", snapshot)
		}
		time.Sleep(5 * time.Millisecond)
	}
	var sent, received []string
	for _, message := range snapshot.Messages {
		if message.Direction == "client-to-server" {
			sent = append(sent, message.Payload)
		} else {
			received = append(received, message.Payload)
		}
	}
	if strings.Join(sent, ",") != "login,next command" || strings.Join(received, ",") != "login,next command" {
		t.Fatalf("sent=%v received=%v", sent, received)
	}
	after, err := repo.StorageStatus(context.Background())
	if err != nil || before != after {
		t.Fatalf("unexpected capture persistence: %v", err)
	}
	w = storageRequest(server, "POST", path+"/close", "")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	w = storageRequest(server, "POST", path+"/close", "")
	if w.Code != 200 {
		t.Fatal("close not idempotent", w.Code)
	}
	w = storageRequest(server, "POST", path+"/send", string(payload))
	if w.Code != 409 {
		t.Fatalf("closed send %d", w.Code)
	}
	w = storageRequest(server, "DELETE", path, "")
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
	w = storageRequest(server, "GET", path, "")
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
	scopeManager.Replace(nilSafeEmptyScope(t))
	w = storageRequest(server, "POST", base, `{"url":"ws://127.0.0.1/"}`)
	if w.Code != 403 {
		t.Fatalf("scope %d", w.Code)
	}
}
