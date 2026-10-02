package api

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/lutzifer/burpsuite-clone/internal/scope"
	"github.com/lutzifer/burpsuite-clone/internal/wsrepeater"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/store"
)

func TestWSRepeaterDraftEligibility(t *testing.T) {
	repo, err := store.OpenSQLite(filepath.Join(t.TempDir(), "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()
	c := &store.WSConnection{URL: "ws://example.test/socket"}
	if err := repo.SaveWSConnection(ctx, c); err != nil {
		t.Fatal(err)
	}
	other := &store.WSConnection{URL: "ws://other.test/"}
	if err := repo.SaveWSConnection(ctx, other); err != nil {
		t.Fatal(err)
	}
	s := NewServer(Config{Store: repo, MaxBodyBytes: 16})
	cases := []struct {
		name   string
		change func(*store.WSMessage)
		code   int
	}{
		{"text", func(m *store.WSMessage) {}, 200},
		{"binary", func(m *store.WSMessage) { m.Type = "binary"; m.Payload = []byte{0, 255}; m.Size = 2 }, 200},
		{"truncated", func(m *store.WSMessage) { m.Truncated = true }, 422},
		{"incomplete", func(m *store.WSMessage) { m.Complete = false }, 422},
		{"opaque", func(m *store.WSMessage) { m.Encoding = "opaque" }, 422},
		{"server message", func(m *store.WSMessage) { m.Direction = "server-to-client" }, 422},
		{"control", func(m *store.WSMessage) { m.Type = "ping" }, 422},
		{"invalid utf8", func(m *store.WSMessage) { m.Payload = []byte{255}; m.Size = 1 }, 422},
		{"too big", func(m *store.WSMessage) { m.Payload = []byte(strings.Repeat("x", 17)); m.Size = 17 }, 422},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &store.WSMessage{ConnectionID: c.ID, Sequence: int64(i + 1), Direction: "client-to-server", Type: "text", Payload: []byte("hello"), Size: 5, Complete: true, Encoding: "identity"}
			tc.change(m)
			if err := repo.SaveWSMessage(ctx, m); err != nil {
				t.Fatal(err)
			}
			path := fmt.Sprintf("/api/websockets/%d/messages/%d/draft", c.ID, m.ID)
			w := storageRequest(s, "GET", path, "")
			if w.Code != tc.code {
				t.Fatalf("got %d %s", w.Code, w.Body.String())
			}
			if tc.code == 200 {
				var draft struct {
					URL, Type, Payload, PayloadFormat string
					Headers                           map[string][]string
				}
				if err := json.Unmarshal(w.Body.Bytes(), &draft); err != nil {
					t.Fatal(err)
				}
				if draft.URL != c.URL || len(draft.Headers) != 0 {
					t.Fatalf("draft: %+v", draft)
				}
				if tc.name == "binary" && (draft.Payload != "00ff" || draft.PayloadFormat != "hex") {
					t.Fatalf("binary: %+v", draft)
				}
			}
			if w := storageRequest(s, "GET", fmt.Sprintf("/api/websockets/%d/messages/%d/draft", other.ID, m.ID), ""); w.Code != 404 {
				t.Fatalf("foreign ID: %d", w.Code)
			}
			if w := serveAPIRequestWithOrigin(s, "GET", path, "", "", "https://evil.test"); w.Code != 403 {
				t.Fatalf("origin: %d", w.Code)
			}
		})
	}
	if w := storageRequest(s, "GET", "/api/websockets/1/messages/9999/draft", ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
}

func TestWSRepeaterUnavailable(t *testing.T) {
	s := NewServer(Config{})
	if w := storageRequest(s, "POST", "/api/websocket-repeater/send", `{}`); w.Code != 503 {
		t.Fatalf("got %d", w.Code)
	}
}

func TestWSRepeaterStrictSendAndScope(t *testing.T) {
	s := NewServer(Config{WSRepeater: wsrepeater.NewService(scope.NewManager(nil), 16)})
	cases := []struct {
		body string
		code int
	}{
		{`null`, 400}, {`{} {}`, 400}, {`{"extra":1}`, 400},
		{`{"url":"ws://example.test/","type":"text","payload":"hello","payloadFormat":"text"}`, 403},
		{`{"url":"http://example.test/","type":"text","payload":"hello","payloadFormat":"text"}`, 400},
		{`{"url":"ws://example.test/","type":"text","payload":"\ud800","payloadFormat":"text"}`, 400},
		{`{"url":"ws://example.test/","type":"text","payload":"\udc00","payloadFormat":"text"}`, 400},
		{`{"url":"ws://example.test/","type":"text","payload":"\ud83d\ude00","payloadFormat":"text"}`, 403},
		{`{"url":"ws://example.test/","type":"binary","payload":"ff","payloadFormat":"hex"}`, 403},
		{`{"url":"ws://example.test/","type":"text","payload":"ff","payloadFormat":"hex"}`, 400},
		{"{\"payload\":\"" + string([]byte{255}) + "\"}", 400},
		{strings.Repeat(" ", 3<<20) + `{}`, 413},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			w := storageRequest(s, "POST", "/api/websocket-repeater/send", tc.body)
			if w.Code != tc.code {
				t.Fatalf("got %d want %d: %s", w.Code, tc.code, w.Body.String())
			}
		})
	}
	if w := serveAPIRequestWithOrigin(s, "POST", "/api/websocket-repeater/send", `{}`, "application/json", "https://evil.test"); w.Code != 403 {
		t.Fatal(w.Code)
	}
}

func TestWSRepeaterAPISendIndependentAndNotPersisted(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			http.Error(w, "missing auth", 401)
			return
		}
		u := websocket.Upgrader{Subprotocols: []string{"test"}}
		c, err := u.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		kind, payload, err := c.ReadMessage()
		if err != nil {
			return
		}
		_ = c.WriteMessage(kind, payload)
		_ = c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	}))
	defer upstream.Close()
	rules, err := scope.Compile(1, []scope.Rule{{Enabled: true, Action: scope.ActionInclude, HostPattern: "127.0.0.1", PathPrefix: "/allowed"}})
	if err != nil {
		t.Fatal(err)
	}
	manager := scope.NewManager(rules)
	repo, err := store.OpenSQLite(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	s := NewServer(Config{Store: repo, WSRepeater: wsrepeater.NewService(manager, 1024)})
	before, err := repo.StorageStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	request := wsrepeater.Request{URL: strings.Replace(upstream.URL, "http", "ws", 1) + "/allowed", Type: "binary", Payload: "00ff", PayloadFormat: "hex", Headers: map[string][]string{"Authorization": {"Bearer test-secret"}}, Subprotocols: []string{"test"}}
	encoded, _ := json.Marshal(request)
	w := storageRequest(s, "POST", "/api/websocket-repeater/send", string(encoded))
	if w.Code != 200 {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	var result wsrepeater.Result
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Sent || len(result.Messages) != 1 || result.Messages[0].Payload != "00ff" || result.Subprotocol != "test" {
		t.Fatalf("%+v", result)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing no-store")
	}
	after, err := repo.StorageStatus(context.Background())
	if err != nil || before != after {
		t.Fatalf("unexpected persistence: %+v %v", after, err)
	}
	manager.Replace(nilSafeEmptyScope(t))
	w = storageRequest(s, "POST", "/api/websocket-repeater/send", string(encoded))
	if w.Code != 403 || calls.Load() != 1 {
		t.Fatalf("scope not checked before dial: %d calls=%d", w.Code, calls.Load())
	}
}

func nilSafeEmptyScope(t *testing.T) *scope.RuleSet {
	t.Helper()
	r, err := scope.Compile(2, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
