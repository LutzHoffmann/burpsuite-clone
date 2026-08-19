package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/certs"
	"github.com/lutzifer/burpsuite-clone/internal/events"
	"github.com/lutzifer/burpsuite-clone/internal/repeater"
	"github.com/lutzifer/burpsuite-clone/internal/store"
)

func TestStatusAndCADownload(t *testing.T) {
	authority, err := certs.LoadOrCreateAuthority(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(Config{
		Store:     store.NewMemoryForTests(),
		Authority: authority,
		APIAddr:   "127.0.0.1:9080",
		ProxyAddr: "127.0.0.1:8080",
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status code = %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp, err = http.Get(ts.URL + "/api/ca.pem")
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/x-pem-file" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := resp.Header.Get("Content-Disposition"); got != "attachment; filename=intercept-ca.pem" {
		t.Fatalf("Content-Disposition = %q", got)
	}
	_ = resp.Body.Close()
}

func TestRepeaterSend(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("sent"))
	}))
	defer target.Close()

	authority, err := certs.LoadOrCreateAuthority(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hub := events.NewHub()
	subscriber, unsubscribe := hub.Subscribe()
	defer unsubscribe()
	srv := NewServer(Config{
		Authority: authority,
		Events:    hub,
		Repeater:  repeater.NewService(http.DefaultTransport, 2048),
	})
	server := httptest.NewServer(srv.Handler())
	defer server.Close()

	body, err := json.Marshal(repeater.SendRequest{Method: http.MethodPost, URL: target.URL})
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(server.URL+"/api/repeater/sessions/7/send", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		responseBody, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		t.Fatalf("status = %d, body = %q", response.StatusCode, responseBody)
	}

	var result repeater.SendResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if string(result.Body) != "sent" {
		t.Fatalf("body = %q", result.Body)
	}

	select {
	case event := <-subscriber:
		if event.Type != "repeater.send.completed" {
			t.Fatalf("event type = %q", event.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("expected repeater completion event")
	}
}
