package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log"
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
	"github.com/lutzifer/burpsuite-clone/internal/scope"
	"github.com/lutzifer/burpsuite-clone/internal/store"
)

func TestProxyForwardsAndStoresOutOfScopeWithoutIntercepting(t *testing.T) {
	rules, err := scope.Compile(1, []scope.Rule{{
		ID: 1, Enabled: true, Action: scope.ActionInclude, HostPattern: "allowed.test",
	}})
	if err != nil {
		t.Fatal(err)
	}
	controller := intercept.NewController(intercept.NewQueue(time.Second), true, []intercept.Rule{{Enabled: true}})
	mem := &memoryStore{}
	observer := &recordingTargetObserver{store: mem}
	srv := NewServer(Config{
		Store: mem, BodyLimitBytes: 1024, Intercept: controller,
		Scope: scope.NewManager(rules), Target: observer,
	})

	response := serveRequestThroughProxy(t, srv, outOfScopeTargetURL(t))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if len(controller.Queue().List()) != 0 {
		t.Fatal("out-of-scope request entered intercept queue")
	}
	exchange := waitForSavedExchanges(t, mem, 1)[0]
	if exchange.InScope || exchange.ScopeVersion != 1 || exchange.ScopeRuleID != nil {
		t.Fatalf("exchange = %#v", exchange)
	}
	observed := observer.snapshot()
	if len(observed) != 1 || observed[0].ID != exchange.ID || exchange.ID == 0 {
		t.Fatalf("observed = %#v, exchange ID = %d", observed, exchange.ID)
	}
}

func TestProxyQueuesAndStoresInScopeWithWinningRule(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(target.Close)
	targetURL := mustParseURL(t, target.URL)
	rules, err := scope.Compile(7, []scope.Rule{{
		ID: 42, Enabled: true, Action: scope.ActionInclude, HostPattern: targetURL.Hostname(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	queue := intercept.NewQueue(time.Second)
	controller := intercept.NewController(queue, true, []intercept.Rule{{Enabled: true}})
	mem := &memoryStore{}
	observer := &recordingTargetObserver{store: mem}
	srv := NewServer(Config{
		Store: mem, BodyLimitBytes: 1024, Intercept: controller,
		Scope: scope.NewManager(rules), Target: observer,
	})
	proxyServer := httptest.NewServer(http.HandlerFunc(srv.handleHTTP))
	t.Cleanup(proxyServer.Close)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(mustParseURL(t, proxyServer.URL))}}
	t.Cleanup(client.CloseIdleConnections)

	responses := make(chan *http.Response, 1)
	errs := make(chan error, 1)
	go func() {
		response, err := client.Get(target.URL + "/included")
		if err != nil {
			errs <- err
			return
		}
		responses <- response
	}()
	item := waitForIntercept(t, queue)
	if err := queue.Forward(item.ID, intercept.RequestEdit{}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errs:
		t.Fatal(err)
	case response := <-responses:
		defer response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("status = %d", response.StatusCode)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("forwarded request did not complete")
	}

	exchange := waitForSavedExchanges(t, mem, 1)[0]
	if !exchange.InScope || exchange.ScopeVersion != 7 || exchange.ScopeRuleID == nil || *exchange.ScopeRuleID != 42 {
		t.Fatalf("exchange = %#v", exchange)
	}
	if !exchange.Intercepted {
		t.Fatal("in-scope exchange was not marked intercepted")
	}
	observed := observer.snapshot()
	if len(observed) != 1 || observed[0].ID != exchange.ID || exchange.ID == 0 {
		t.Fatalf("observed = %#v, exchange ID = %d", observed, exchange.ID)
	}
}

func TestProxyHTTPSMITMForwardsOutOfScopeWithoutIntercepting(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)
	authority, err := certs.LoadOrCreateAuthority(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rules, err := scope.Compile(4, []scope.Rule{{
		ID: 1, Enabled: true, Action: scope.ActionInclude, HostPattern: "allowed.test",
	}})
	if err != nil {
		t.Fatal(err)
	}
	controller := intercept.NewController(intercept.NewQueue(time.Second), true, []intercept.Rule{{Enabled: true}})
	mem := &memoryStore{}
	observer := &recordingTargetObserver{store: mem}
	upstream := target.Client().Transport.(*http.Transport).Clone()
	upstreamTLS := upstream.TLSClientConfig.Clone()
	upstreamTLS.ServerName = "example.com"
	upstream.TLSClientConfig = upstreamTLS
	srv := NewServer(Config{
		Store: mem, BodyLimitBytes: 1024, Transport: upstream,
		Authority: authority, Intercept: controller, Scope: scope.NewManager(rules), Target: observer,
	})
	listener := listenForTest(t)
	t.Cleanup(func() { _ = listener.Close() })
	go func() { _ = srv.Serve(listener) }()
	client := proxyClient(listener.Addr().String(), authority)
	t.Cleanup(client.CloseIdleConnections)

	response, err := client.Get(strings.Replace(target.URL, "127.0.0.1", "localhost", 1) + "/outside")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if len(controller.Queue().List()) != 0 {
		t.Fatal("out-of-scope HTTPS request entered intercept queue")
	}
	exchange := waitForSavedExchanges(t, mem, 1)[0]
	if exchange.InScope || exchange.ScopeVersion != 4 || exchange.ScopeRuleID != nil || exchange.Scheme != "https" {
		t.Fatalf("exchange = %#v", exchange)
	}
	observed := observer.snapshot()
	if len(observed) != 1 || observed[0].ID != exchange.ID {
		t.Fatalf("observed = %#v, exchange ID = %d", observed, exchange.ID)
	}
}

func TestProxyNilScopeFailsClosedWhileForwardingAndStoring(t *testing.T) {
	mem := &memoryStore{}
	srv := NewServer(Config{Store: mem, BodyLimitBytes: 1024})
	response := serveRequestThroughProxy(t, srv, outOfScopeTargetURL(t))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	exchange := waitForSavedExchanges(t, mem, 1)[0]
	if exchange.InScope || exchange.ScopeVersion != 0 || exchange.ScopeRuleID != nil {
		t.Fatalf("exchange = %#v", exchange)
	}
}

func TestProxyPreparationFailureStoresCaptureTimeScopeDecision(t *testing.T) {
	rules, err := scope.Compile(11, []scope.Rule{{
		ID: 5, Enabled: true, Action: scope.ActionInclude, HostPattern: "example.test",
	}})
	if err != nil {
		t.Fatal(err)
	}
	mem := &memoryStore{}
	observer := &recordingTargetObserver{store: mem}
	srv := NewServer(Config{
		Store: mem, BodyLimitBytes: 1024,
		Intercept: intercept.NewController(intercept.NewQueue(time.Second), true, []intercept.Rule{{Enabled: true}}),
		Scope:     scope.NewManager(rules), Target: observer,
	})
	request := httptest.NewRequest(http.MethodPost, "http://example.test/fail", nil)
	request.Body = errorReadCloser{err: errors.New("request body failed")}
	recorder := httptest.NewRecorder()

	srv.handleHTTP(recorder, request)

	if recorder.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d", recorder.Code)
	}
	exchange := waitForSavedExchanges(t, mem, 1)[0]
	assertScopeDecision(t, exchange, true, 11, 5)
	if !exchange.Error || !strings.Contains(exchange.ErrorMessage, "request body failed") {
		t.Fatalf("exchange = %#v", exchange)
	}
	if observed := observer.snapshot(); len(observed) != 1 || observed[0].ID != exchange.ID {
		t.Fatalf("observed = %#v, exchange ID = %d", observed, exchange.ID)
	}
}

func TestProxyUpstreamFailureStoresCaptureTimeScopeDecision(t *testing.T) {
	mem := &memoryStore{}
	observer := &recordingTargetObserver{store: mem}
	srv := NewServer(Config{
		Store: mem, BodyLimitBytes: 1024, Scope: scopeManagerForURL(t, "http://missing.invalid", 12, 6),
		Target: observer,
		Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dns lookup failed")
		}),
	})
	recorder := httptest.NewRecorder()
	srv.handleHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://missing.invalid/path", nil))

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d", recorder.Code)
	}
	exchange := waitForSavedExchanges(t, mem, 1)[0]
	assertScopeDecision(t, exchange, true, 12, 6)
	if observed := observer.snapshot(); len(observed) != 1 || observed[0].ID != exchange.ID {
		t.Fatalf("observed = %#v, exchange ID = %d", observed, exchange.ID)
	}
}

func TestProxyScopeObserverFailureDoesNotChangeResponseOrLeakError(t *testing.T) {
	var logs bytes.Buffer
	previousLogWriter := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previousLogWriter) })
	srv := NewServer(Config{
		Store: &memoryStore{}, BodyLimitBytes: 1024,
		Target: targetObserverFunc(func(context.Context, *store.Exchange) error {
			return errors.New("secret request body")
		}),
	})

	response := serveRequestThroughProxy(t, srv, outOfScopeTargetURL(t))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if strings.Contains(logs.String(), "secret request body") {
		t.Fatalf("observer error leaked into logs: %q", logs.String())
	}
}

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
	srv := NewServer(Config{
		Store: store.NewMemoryForTests(), BodyLimitBytes: 1024, Intercept: controller,
		Scope: scopeManagerForURL(t, target.URL, 1, 1),
	})
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
	mem := &memoryStore{}
	observer := &recordingTargetObserver{store: mem}
	baseURL := strings.Replace(target.URL, "127.0.0.1", "localhost", 1)
	srv := NewServer(Config{
		Store: mem, BodyLimitBytes: 1024, Authority: authority,
		Transport: upstream, Intercept: controller, Scope: scopeManagerForURL(t, baseURL, 3, 9),
		Target: observer,
	})
	ln := listenForTest(t)
	defer ln.Close()
	go func() { _ = srv.Serve(ln) }()

	client := proxyClient(ln.Addr().String(), authority)
	response := make(chan *http.Response, 1)
	errs := make(chan error, 1)
	go func() {
		resp, err := client.Get(baseURL + "/drop")
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
	exchange := waitForSavedExchanges(t, mem, 1)[0]
	if !exchange.InScope || exchange.ScopeVersion != 3 || exchange.ScopeRuleID == nil || *exchange.ScopeRuleID != 9 {
		t.Fatalf("exchange = %#v", exchange)
	}
	observed := observer.snapshot()
	if len(observed) != 1 || observed[0].ID != exchange.ID {
		t.Fatalf("observed = %#v, exchange ID = %d", observed, exchange.ID)
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

func TestHTTPProxyTimesOutStalledUpstreamBody(t *testing.T) {
	release := make(chan struct{})
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-release
	}))
	defer func() {
		close(release)
		target.Close()
	}()

	srv := NewServer(Config{
		Store:             store.NewMemoryForTests(),
		BodyLimitBytes:    1024,
		StreamIdleTimeout: 100 * time.Millisecond,
	})
	ln := listenForTest(t)
	defer ln.Close()
	go func() { _ = srv.Serve(ln) }()

	response, err := proxyClient(ln.Addr().String(), nil).Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	started := time.Now()
	_, err = io.ReadAll(response.Body)
	if err == nil {
		t.Fatal("stalled upstream response completed without an error")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("stalled response took %s to terminate", elapsed)
	}
}

func TestHTTPProxyAllowsLongResponseWhileDataKeepsFlowing(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "4")
		for _, chunk := range []byte("flow") {
			_, _ = w.Write([]byte{chunk})
			w.(http.Flusher).Flush()
			time.Sleep(75 * time.Millisecond)
		}
	}))
	defer target.Close()

	srv := NewServer(Config{
		Store:             store.NewMemoryForTests(),
		BodyLimitBytes:    1024,
		StreamIdleTimeout: 200 * time.Millisecond,
	})
	ln := listenForTest(t)
	defer ln.Close()
	go func() { _ = srv.Serve(ln) }()

	response, err := proxyClient(ln.Addr().String(), nil).Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "flow" {
		t.Fatalf("body = %q", body)
	}
}

func TestHTTPProxyTimesOutStalledClientRequestBody(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	targetURL := mustParseURL(t, target.URL)

	srv := NewServer(Config{
		Store:             store.NewMemoryForTests(),
		BodyLimitBytes:    1024,
		StreamIdleTimeout: 100 * time.Millisecond,
	})
	ln := listenForTest(t)
	defer ln.Close()
	go func() { _ = srv.Serve(ln) }()

	connection, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	request := "POST " + target.URL + "/upload HTTP/1.1\r\n" +
		"Host: " + targetURL.Host + "\r\n" +
		"Content-Length: 4\r\nConnection: close\r\n\r\nx"
	if _, err := io.WriteString(connection, request); err != nil {
		t.Fatal(err)
	}
	if err := connection.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d", response.StatusCode)
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

func serveRequestThroughProxy(t *testing.T, srv *Server, targetURL string) *http.Response {
	t.Helper()
	proxyServer := httptest.NewServer(http.HandlerFunc(srv.handleHTTP))
	t.Cleanup(proxyServer.Close)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(mustParseURL(t, proxyServer.URL))}}
	t.Cleanup(client.CloseIdleConnections)
	response, err := client.Get(targetURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func outOfScopeTargetURL(t *testing.T) string {
	t.Helper()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)
	return target.URL + "/outside"
}

func scopeManagerForURL(t *testing.T, rawURL string, version, ruleID int64) *scope.Manager {
	t.Helper()
	targetURL := mustParseURL(t, rawURL)
	rules, err := scope.Compile(version, []scope.Rule{{
		ID: ruleID, Enabled: true, Action: scope.ActionInclude, HostPattern: targetURL.Hostname(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	return scope.NewManager(rules)
}

func assertScopeDecision(t *testing.T, exchange *store.Exchange, inScope bool, version, ruleID int64) {
	t.Helper()
	if exchange.InScope != inScope || exchange.ScopeVersion != version || exchange.ScopeRuleID == nil || *exchange.ScopeRuleID != ruleID {
		t.Fatalf("scope decision = in:%t version:%d rule:%v", exchange.InScope, exchange.ScopeVersion, exchange.ScopeRuleID)
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

type targetObserverFunc func(context.Context, *store.Exchange) error

func (fn targetObserverFunc) Observe(ctx context.Context, exchange *store.Exchange) error {
	return fn(ctx, exchange)
}

type errorReadCloser struct {
	err error
}

func (r errorReadCloser) Read([]byte) (int, error) {
	return 0, r.err
}

func (errorReadCloser) Close() error {
	return nil
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
	if exchange.ID == 0 {
		exchange.ID = int64(len(s.saved) + 1)
	}
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

type recordingTargetObserver struct {
	mu       sync.Mutex
	store    *memoryStore
	observed []*store.Exchange
}

func (o *recordingTargetObserver) Observe(_ context.Context, exchange *store.Exchange) error {
	if exchange.ID == 0 {
		return errors.New("exchange observed before persistence assigned an ID")
	}
	saved := o.store.snapshot()
	if len(saved) == 0 || saved[len(saved)-1].ID != exchange.ID {
		return errors.New("exchange observed before persistence completed")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	copy := *exchange
	o.observed = append(o.observed, &copy)
	return nil
}

func (o *recordingTargetObserver) snapshot() []*store.Exchange {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]*store.Exchange(nil), o.observed...)
}
