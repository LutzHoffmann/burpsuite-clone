package integration_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lutzifer/burpsuite-clone/internal/store"
)

func TestWebSocketRelayAndHistory(t *testing.T) {
	for _, secure := range []bool{false, true} {
		for _, compressed := range []bool{false, true} {
			t.Run(fmt.Sprintf("secure=%v/compressed=%v", secure, compressed), func(t *testing.T) {
				h := newHarness(t, secure, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					up := websocket.Upgrader{Subprotocols: []string{"test"}, EnableCompression: compressed}
					c, err := up.Upgrade(w, r, nil)
					if err != nil {
						return
					}
					defer c.Close()
					for {
						typ, data, err := c.ReadMessage()
						if err != nil {
							return
						}
						if c.WriteMessage(typ, data) != nil {
							return
						}
					}
				}))
				// Neither HTTP interception switch may trap a WebSocket handshake.
				h.json("PUT", "/api/intercept/config", map[string]any{"enabled": true, "rules": []any{map[string]any{"enabled": true}}, "responseEnabled": true, "responseRules": []any{map[string]any{"enabled": true}}, "replacementRules": []any{}}, 200, nil)
				tr := h.client.Transport.(*http.Transport)
				dialer := websocket.Dialer{Proxy: tr.Proxy, TLSClientConfig: tr.TLSClientConfig, Subprotocols: []string{"test"}, HandshakeTimeout: 2 * time.Second, EnableCompression: compressed, WriteBufferSize: 128}
				c, _, err := dialer.Dial(strings.Replace(h.upstream, "http", "ws", 1)+"/allowed/ws", nil)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				if c.Subprotocol() != "test" {
					t.Fatal("subprotocol lost")
				}
				_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
				for _, typ := range []int{websocket.TextMessage, websocket.BinaryMessage} {
					payload := bytes.Repeat([]byte("hello websocket"), 100000)
					if typ == websocket.BinaryMessage {
						payload = []byte{0, 255, 4, 128}
					}
					if err := c.WriteMessage(typ, payload); err != nil {
						t.Fatal(err)
					}
					gotType, data, err := c.ReadMessage()
					if err != nil || gotType != typ || !bytes.Equal(data, payload) {
						t.Fatalf("echo %d %x %v", gotType, data, err)
					}
				}
				_ = c.Close()
				var page struct {
					Items []struct {
						ID    int64  `json:"id"`
						State string `json:"state"`
					} `json:"items"`
				}
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) {
					h.json("GET", "/api/websockets", nil, 200, &page)
					if len(page.Items) == 1 && page.Items[0].State != "open" {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				if len(page.Items) != 1 || page.Items[0].State == "open" {
					t.Fatalf("connection not finished: %+v", page)
				}
				var messages struct {
					Items []struct {
						Direction string `json:"direction"`
						Type      string `json:"type"`
						Encoding  string `json:"encoding"`
						Truncated bool   `json:"truncated"`
					} `json:"items"`
				}
				h.json("GET", fmt.Sprintf("/api/websockets/%d/messages", page.Items[0].ID), nil, 200, &messages)
				if len(messages.Items) != 4 {
					t.Fatalf("messages: %+v", messages)
				}
				for _, m := range messages.Items {
					if compressed && m.Encoding != "opaque" {
						t.Fatalf("compressed payload mislabeled: %+v", m)
					}
					if !compressed && m.Type == "text" && !m.Truncated {
						t.Fatal("large message missing truncation flag")
					}
				}
			})
		}
	}
}

func TestWebSocketRejectedUpgradeIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	h := newHarness(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, "denied once")
	}))
	tr := h.client.Transport.(*http.Transport)
	dialer := websocket.Dialer{Proxy: tr.Proxy, HandshakeTimeout: 2 * time.Second}
	_, response, err := dialer.Dial(strings.Replace(h.upstream, "http", "ws", 1), nil)
	if err == nil || response == nil {
		t.Fatalf("expected rejected upgrade: %v", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != 403 || string(body) != "denied once" || calls.Load() != 1 {
		t.Fatalf("unexpected rejection: %d %q calls=%d", response.StatusCode, body, calls.Load())
	}
	_ = h.waitHistory(1)
}

func TestWebSocketQuotaGapsAndResumeOnOpenConnection(t *testing.T) {
	h := newHarness(t, true, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			typ, data, err := c.ReadMessage()
			if err != nil || c.WriteMessage(typ, data) != nil {
				return
			}
		}
	}))
	tr := h.client.Transport.(*http.Transport)
	dialer := websocket.Dialer{Proxy: tr.Proxy, TLSClientConfig: tr.TLSClientConfig, HandshakeTimeout: 2 * time.Second}
	c, _, err := dialer.Dial(strings.Replace(h.upstream, "http", "ws", 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var page store.WSConnectionsPage
	wait := func(check func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if check() {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("capture state did not converge")
	}
	wait(func() bool { h.json("GET", "/api/websockets", nil, 200, &page); return len(page.Items) == 1 })
	id := page.Items[0].ID
	h.json("PUT", "/api/storage", map[string]any{"limitBytes": 1}, 200, nil)
	echo := func(payload string) {
		t.Helper()
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		if err := c.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
			t.Fatal(err)
		}
		_, data, err := c.ReadMessage()
		if err != nil || string(data) != payload {
			t.Fatalf("quota interrupted traffic: %q %v", data, err)
		}
	}
	echo("not captured")
	var connection store.WSConnection
	wait(func() bool {
		h.json("GET", fmt.Sprintf("/api/websockets/%d", id), nil, 200, &connection)
		return connection.Gaps == 2
	})
	if connection.State != "open" {
		t.Fatalf("connection closed: %+v", connection)
	}
	h.json("PUT", "/api/storage", map[string]any{"limitBytes": 1 << 30}, 200, nil)
	echo("captured again")
	var messages store.WSMessagesPage
	wait(func() bool {
		h.json("GET", fmt.Sprintf("/api/websockets/%d/messages", id), nil, 200, &messages)
		return len(messages.Items) == 2
	})
	if messages.Items[0].Sequence != 4 || messages.Items[1].Sequence != 3 {
		t.Fatalf("gap sequence: %+v", messages)
	}
	// Drain async connection finalization before the harness closes/removes SQLite.
	_ = c.Close()
	wait(func() bool {
		h.json("GET", fmt.Sprintf("/api/websockets/%d", id), nil, 200, &connection)
		return connection.State != "open"
	})
}
