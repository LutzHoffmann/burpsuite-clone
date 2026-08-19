package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/lutzifer/burpsuite-clone/internal/certs"
	"github.com/lutzifer/burpsuite-clone/internal/events"
	"github.com/lutzifer/burpsuite-clone/internal/store"
)

func TestHTTPProxyCapturesExchange(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("target response"))
	}))
	defer target.Close()

	mem := &memoryStore{}
	srv := NewServer(Config{Store: mem, BodyLimitBytes: 1024})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() { _ = srv.Serve(ln) }()

	client := &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyURL(mustParseURL(t, "http://"+ln.Addr().String())),
	}}
	resp, err := client.Get(target.URL + "/hello?x=1")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if string(body) != "target response" {
		t.Fatalf("body = %q", body)
	}
	if len(mem.saved) != 1 {
		t.Fatalf("saved exchanges = %d", len(mem.saved))
	}
	if mem.saved[0].Method != "GET" || mem.saved[0].Path != "/hello" || mem.saved[0].Status != 200 {
		t.Fatalf("exchange = %+v", mem.saved[0])
	}
}

func TestHTTPProxyPublishesHistoryEntryCreatedEvent(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("target response"))
	}))
	defer target.Close()

	hub := events.NewHub()
	subscriber, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	srv := NewServer(Config{
		Store:          store.NewMemoryForTests(),
		BodyLimitBytes: 1024,
		Events:         hub,
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, target.URL+"/events", nil)
	srv.handleHTTP(recorder, request)

	select {
	case event := <-subscriber:
		if event.Type != "history.entry.created" {
			t.Fatalf("event type = %q", event.Type)
		}
		data, ok := event.Data.(map[string]interface{})
		if !ok {
			t.Fatalf("event data = %T", event.Data)
		}
		if data["id"] != int64(1) || data["path"] != "/events" || data["status"] != http.StatusOK {
			t.Fatalf("event data = %#v", data)
		}
	default:
		t.Fatal("expected history entry created event")
	}
}

func TestHTTPProxyTruncatesCapturedBodies(t *testing.T) {
	const bodyLimit = 8
	requestBody := "request body exceeds the limit"
	responseBody := "response body exceeds the limit"
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != requestBody {
			t.Fatalf("request body = %q", body)
		}
		_, _ = w.Write([]byte(responseBody))
	}))
	defer target.Close()

	mem := &memoryStore{}
	srv := NewServer(Config{Store: mem, BodyLimitBytes: bodyLimit})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() { _ = srv.Serve(ln) }()

	client := &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyURL(mustParseURL(t, "http://"+ln.Addr().String())),
	}}
	resp, err := client.Post(target.URL, "text/plain", strings.NewReader(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if string(body) != responseBody {
		t.Fatalf("response body = %q", body)
	}

	if len(mem.saved) != 1 {
		t.Fatalf("saved exchanges = %d", len(mem.saved))
	}
	exchange := mem.saved[0]
	if !exchange.RequestTruncated || !exchange.ResponseTruncated {
		t.Fatalf("truncation flags = request:%t response:%t", exchange.RequestTruncated, exchange.ResponseTruncated)
	}
	if !bytes.Equal(exchange.Request.Body, []byte(requestBody[:bodyLimit])) {
		t.Fatalf("captured request body = %q", exchange.Request.Body)
	}
	if !bytes.Equal(exchange.Response.Body, []byte(responseBody[:bodyLimit])) {
		t.Fatalf("captured response body = %q", exchange.Response.Body)
	}
}

func TestHTTPSProxyMITMCapturesExchange(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer target.Close()

	authority, err := certs.LoadOrCreateAuthority(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mem := &memoryStore{}
	upstreamTransport := target.Client().Transport.(*http.Transport).Clone()
	upstreamTLS := upstreamTransport.TLSClientConfig.Clone()
	upstreamTLS.ServerName = "example.com"
	upstreamTransport.TLSClientConfig = upstreamTLS
	srv := NewServer(Config{
		Store:          mem,
		BodyLimitBytes: 2048,
		Transport:      upstreamTransport,
		Authority:      authority,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() { _ = srv.Serve(ln) }()

	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(authority.CACertPEM()) {
		t.Fatal("failed to trust test CA")
	}
	client := &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyURL(mustParseURL(t, "http://"+ln.Addr().String())),
		TLSClientConfig: &tls.Config{
			RootCAs: roots,
		},
	}}
	resp, err := client.Get(strings.Replace(target.URL, "127.0.0.1", "localhost", 1) + "/secure")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if string(body) != `{"ok":true}` {
		t.Fatalf("body = %q", body)
	}
	if len(mem.saved) != 1 {
		t.Fatalf("saved exchanges = %d", len(mem.saved))
	}
	if mem.saved[0].Scheme != "https" || mem.saved[0].Path != "/secure" {
		t.Fatalf("exchange = %+v", mem.saved[0])
	}
}

func TestCapturingReadCloserStreamsAndBoundsCapture(t *testing.T) {
	body := "capture this body while streaming it"
	captured := newCapturingReadCloser(io.NopCloser(strings.NewReader(body)), 7)

	forwarded, err := io.ReadAll(captured)
	if err != nil {
		t.Fatal(err)
	}
	if string(forwarded) != body {
		t.Fatalf("forwarded body = %q", forwarded)
	}
	stored, truncated := captured.Captured()
	if !truncated {
		t.Fatal("capture was not marked truncated")
	}
	if string(stored) != body[:7] {
		t.Fatalf("captured body = %q", stored)
	}
}

func mustParseURL(t *testing.T, rawURL string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

type memoryStore struct {
	saved []*store.Exchange
}

func (s *memoryStore) SaveExchange(_ context.Context, exchange *store.Exchange) error {
	s.saved = append(s.saved, exchange)
	return nil
}

func (s *memoryStore) ListHistory(context.Context, store.HistoryFilter) ([]store.HistoryItem, error) {
	return nil, nil
}

func (s *memoryStore) GetExchange(context.Context, int64) (*store.Exchange, error) {
	return nil, nil
}

func (s *memoryStore) Close() error {
	return nil
}
