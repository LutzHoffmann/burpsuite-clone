package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/certs"
	"github.com/lutzifer/burpsuite-clone/internal/events"
	"github.com/lutzifer/burpsuite-clone/internal/intercept"
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
	saved := waitForSavedExchanges(t, mem, 1)
	if saved[0].Method != "GET" || saved[0].Path != "/hello" || saved[0].Status != 200 {
		t.Fatalf("exchange = %+v", saved[0])
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

	exchange := waitForSavedExchanges(t, mem, 1)[0]
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
	saved := waitForSavedExchanges(t, mem, 1)
	if saved[0].Scheme != "https" || saved[0].Path != "/secure" {
		t.Fatalf("exchange = %+v", saved[0])
	}
}

func TestHTTPProxyInterceptBlocksUntilForward(t *testing.T) {
	targetReached := make(chan struct{}, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetReached <- struct{}{}
		body, _ := io.ReadAll(r.Body)
		_, _ = w.Write(body)
	}))
	defer target.Close()

	queue := intercept.NewQueue(2 * time.Second)
	controller := intercept.NewController(queue, true, []intercept.Rule{{Enabled: true}})
	srv := NewServer(Config{Store: store.NewMemoryForTests(), BodyLimitBytes: 1024, Intercept: controller})
	ln := listenForTest(t)
	defer ln.Close()
	go func() { _ = srv.Serve(ln) }()

	client := proxyClient(ln.Addr().String(), nil)
	response := make(chan *http.Response, 1)
	errs := make(chan error, 1)
	go func() {
		resp, err := client.Post(target.URL+"/paused", "text/plain", strings.NewReader("before"))
		if err != nil {
			errs <- err
			return
		}
		response <- resp
	}()

	item := waitForIntercept(t, queue)
	select {
	case <-targetReached:
		t.Fatal("upstream reached before intercept decision")
	default:
	}
	if err := queue.Forward(item.ID, intercept.RequestEdit{
		Method: item.Method, URL: item.URL, Headers: item.Headers, Body: []byte("after"),
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-errs:
		t.Fatal(err)
	case resp := <-response:
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if string(body) != "after" {
			t.Fatalf("body = %q", body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("forwarded request did not complete")
	}
}

func TestHTTPSProxyInterceptBlocksUntilDrop(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetCalls.Add(1)
	}))
	defer target.Close()

	authority, err := certs.LoadOrCreateAuthority(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	upstream := target.Client().Transport.(*http.Transport).Clone()
	queue := intercept.NewQueue(2 * time.Second)
	controller := intercept.NewController(queue, true, []intercept.Rule{{Enabled: true}})
	srv := NewServer(Config{
		Store: store.NewMemoryForTests(), BodyLimitBytes: 1024, Authority: authority,
		Transport: upstream, Intercept: controller,
	})
	ln := listenForTest(t)
	defer ln.Close()
	go func() { _ = srv.Serve(ln) }()

	client := proxyClient(ln.Addr().String(), authority)
	response := make(chan *http.Response, 1)
	errs := make(chan error, 1)
	go func() {
		resp, err := client.Get(strings.Replace(target.URL, "127.0.0.1", "localhost", 1) + "/drop")
		if err != nil {
			errs <- err
			return
		}
		response <- resp
	}()

	item := waitForIntercept(t, queue)
	if err := queue.Drop(item.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errs:
		t.Fatal(err)
	case resp := <-response:
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d", resp.StatusCode)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dropped request did not complete")
	}
	if targetCalls.Load() != 0 {
		t.Fatalf("target calls = %d", targetCalls.Load())
	}
}

func TestHTTPSProxyHandlesMultipleRequestsOnOneTunnel(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.URL.Path))
	}))
	defer target.Close()
	authority, err := certs.LoadOrCreateAuthority(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mem := &memoryStore{}
	srv := NewServer(Config{
		Store: mem, BodyLimitBytes: 1024, Authority: authority,
		Transport: target.Client().Transport.(*http.Transport).Clone(),
	})
	rawListener := listenForTest(t)
	ln := &countingListener{Listener: rawListener}
	defer ln.Close()
	go func() { _ = srv.Serve(ln) }()
	client := proxyClient(ln.Addr().String(), authority)
	baseURL := strings.Replace(target.URL, "127.0.0.1", "localhost", 1)
	for _, path := range []string{"/one", "/two"} {
		resp, err := client.Get(baseURL + path)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	waitForSavedExchanges(t, mem, 2)
	if ln.accepted.Load() != 1 {
		t.Fatalf("proxy connections = %d; expected one CONNECT tunnel", ln.accepted.Load())
	}
}

func TestProxyStripsProxyAndHopByHopRequestHeaders(t *testing.T) {
	received := make(chan http.Header, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	srv := NewServer(Config{Store: store.NewMemoryForTests(), BodyLimitBytes: 1024})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, target.URL, nil)
	request.Header.Set("Proxy-Authorization", "Basic secret")
	request.Header.Set("Connection", "X-Internal, keep-alive")
	request.Header.Set("X-Internal", "secret")
	request.Header.Set("Keep-Alive", "timeout=5")
	srv.handleHTTP(recorder, request)

	headers := <-received
	for _, name := range []string{"Proxy-Authorization", "Connection", "X-Internal", "Keep-Alive"} {
		if headers.Get(name) != "" {
			t.Errorf("%s reached target: %q", name, headers.Get(name))
		}
	}
}

func TestProxyPersistsUpstreamFailures(t *testing.T) {
	mem := &memoryStore{}
	srv := NewServer(Config{
		Store: mem, BodyLimitBytes: 1024,
		Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dns lookup failed")
		}),
	})
	recorder := httptest.NewRecorder()
	srv.handleHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://missing.invalid/path", nil))
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d", recorder.Code)
	}
	saved := waitForSavedExchanges(t, mem, 1)
	if !saved[0].Error || !strings.Contains(saved[0].ErrorMessage, "dns lookup failed") {
		t.Fatalf("saved exchange = %#v", saved)
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

func listenForTest(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

func proxyClient(proxyAddr string, authority *certs.Authority) *http.Client {
	transport := &http.Transport{Proxy: http.ProxyURL(mustParseURLWithoutTest("http://" + proxyAddr))}
	if authority != nil {
		roots := x509.NewCertPool()
		roots.AppendCertsFromPEM(authority.CACertPEM())
		transport.TLSClientConfig = &tls.Config{RootCAs: roots}
	}
	return &http.Client{Transport: transport, Timeout: 4 * time.Second}
}

func mustParseURLWithoutTest(rawURL string) *url.URL {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		panic(err)
	}
	return parsed
}

func waitForIntercept(t *testing.T, queue *intercept.Queue) intercept.Item {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if items := queue.List(); len(items) == 1 {
			return items[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("intercept item was not queued")
	return intercept.Item{}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type countingListener struct {
	net.Listener
	accepted atomic.Int32
}

func (l *countingListener) Accept() (net.Conn, error) {
	connection, err := l.Listener.Accept()
	if err == nil {
		l.accepted.Add(1)
	}
	return connection, err
}

type memoryStore struct {
	mu    sync.Mutex
	saved []*store.Exchange
}

func (s *memoryStore) SaveExchange(_ context.Context, exchange *store.Exchange) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saved = append(s.saved, exchange)
	return nil
}

func (s *memoryStore) snapshot() []*store.Exchange {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*store.Exchange(nil), s.saved...)
}

func waitForSavedExchanges(t *testing.T, memory *memoryStore, count int) []*store.Exchange {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if saved := memory.snapshot(); len(saved) >= count {
			return saved
		}
		time.Sleep(time.Millisecond)
	}
	saved := memory.snapshot()
	t.Fatalf("saved exchanges = %d, want %d", len(saved), count)
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
