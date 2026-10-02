package integration_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/api"
	"github.com/lutzifer/burpsuite-clone/internal/certs"
	"github.com/lutzifer/burpsuite-clone/internal/intercept"
	"github.com/lutzifer/burpsuite-clone/internal/proxy"
	"github.com/lutzifer/burpsuite-clone/internal/scope"
	"github.com/lutzifer/burpsuite-clone/internal/store"
)

func TestTruncatedChunkedResponseReportsClientError(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprintf("https=%v", secure), func(t *testing.T) {
			h := newHarness(t, secure, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				_, _ = io.WriteString(w, "partial")
				w.(http.Flusher).Flush()
				panic(http.ErrAbortHandler)
			}))
			select {
			case got := <-h.request(http.MethodGet, "/allowed", ""):
				if got.err == nil {
					t.Errorf("truncated response accepted as complete: %q", got.body)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("client did not finish")
			}
			if !h.waitHistory(1).Error {
				t.Error("truncation missing from history")
			}
		})
	}
}

func TestStreamingEventArrivesBeforeUpstreamCloses(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprintf("https=%t", secure), func(t *testing.T) {
			release := make(chan struct{})
			h := newHarness(t, secure, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: hello\n\n")
				w.(http.Flusher).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
				}
			}))
			t.Cleanup(func() { close(release) })
			done := make(chan error, 1)
			go func() {
				response, err := h.client.Get(h.upstream + "/allowed")
				if err != nil {
					done <- err
					return
				}
				defer response.Body.Close()
				body := make([]byte, len("data: hello\n\n"))
				_, err = io.ReadFull(response.Body, body)
				if err == nil && string(body) != "data: hello\n\n" {
					err = fmt.Errorf("unexpected event %q", body)
				}
				done <- err
			}()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("first streaming event withheld while upstream remains open")
			}
			_ = h.waitHistory(1)
		})
	}
}

func TestResponseTrailersSurviveProxy(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprintf("https=%t", secure), func(t *testing.T) {
			h := newHarness(t, secure, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				w.Header().Set("Trailer", "X-Checksum")
				_, _ = io.WriteString(w, "checksum body")
				w.Header().Set("X-Checksum", "complete")
			}))
			got := await(t, h.request("GET", "/allowed", ""))
			if got.body != "checksum body" || got.trailers.Get("X-Checksum") != "complete" {
				t.Fatalf("lost body or trailer: body=%q trailers=%v", got.body, got.trailers)
			}
			_ = h.waitHistory(1)
		})
	}
}

func TestHTTPSDisconnectClearsResponseQueue(t *testing.T) {
	h := newHarness(t, true, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "pending")
	}))
	h.json(http.MethodPut, "/api/intercept/config", map[string]any{
		"enabled": false, "rules": []any{}, "responseEnabled": true,
		"responseRules": []any{map[string]any{"enabled": true}}, "replacementRules": []any{},
	}, 200, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, "GET", h.upstream+"/allowed", nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		response, err := h.client.Do(request)
		if response != nil {
			_ = response.Body.Close()
		}
		done <- err
	}()
	_ = h.waitResponse()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("client did not cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client cancellation stalled")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var queue []pending
		h.json("GET", "/api/intercept/response-queue", nil, 200, &queue)
		if len(queue) == 0 {
			_ = h.waitHistory(1)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("disconnected HTTPS response retained a queue slot")
}

func TestResponseAtCaptureLimitCanForwardUnchanged(t *testing.T) {
	const size = 1 << 20
	body := strings.Repeat("\"", size)
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprintf("https=%t", secure), func(t *testing.T) {
			h := newHarness(t, secure, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				w.Header().Set("Content-Length", fmt.Sprint(size))
				_, _ = io.WriteString(w, body)
			}))
			h.json("PUT", "/api/intercept/config", map[string]any{
				"enabled": false, "rules": []any{}, "responseEnabled": true,
				"responseRules": []any{map[string]any{"enabled": true}}, "replacementRules": []any{},
			}, 200, nil)
			result := h.request("GET", "/allowed", "")
			item := h.waitResponse()
			if !item.BodyEditable || item.Body != body {
				t.Fatal("body at capture limit was not editable")
			}
			h.json("POST", "/api/intercept/response/"+item.ID+"/forward", map[string]any{
				"statusCode": 200, "headers": item.Headers, "body": item.Body,
			}, 204, nil)
			got := await(t, result)
			if got.status != 200 || got.body != body {
				t.Fatalf("unchanged forward failed: status=%d bytes=%d", got.status, len(got.body))
			}
			_ = h.waitHistory(1)
		})
	}
}

func TestResponseHeaderOnlyPreservesBodiesThroughAPI(t *testing.T) {
	var encoded bytes.Buffer
	zipper := gzip.NewWriter(&encoded)
	_, _ = io.WriteString(zipper, "encoded body")
	if err := zipper.Close(); err != nil {
		t.Fatal(err)
	}
	for _, secure := range []bool{false, true} {
		for _, fixture := range []struct{ name, method, mime, encoding, body string }{
			{"binary", "GET", "application/octet-stream", "", "\x00\xffraw"},
			{"encoded", "GET", "text/plain", "gzip", encoded.String()},
			{"oversized", "GET", "text/plain", "", strings.Repeat("x", (1<<20)+1)},
			{"head", "HEAD", "text/plain", "", ""},
		} {
			t.Run(fmt.Sprintf("https=%t/%s", secure, fixture.name), func(t *testing.T) {
				h := newHarness(t, secure, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", fixture.mime)
					if fixture.encoding != "" {
						w.Header().Set("Content-Encoding", fixture.encoding)
					}
					w.Header().Set("X-Review", "before")
					_, _ = io.WriteString(w, fixture.body)
				}))
				h.json(http.MethodPut, "/api/intercept/config", map[string]any{
					"enabled": false, "rules": []any{}, "responseEnabled": true,
					"responseRules": []any{map[string]any{"enabled": true}}, "replacementRules": []any{},
				}, 200, nil)
				result := h.request(fixture.method, "/allowed", "")
				item := h.waitResponse()
				if item.BodyEditable {
					t.Fatal("unsafe body exposed for text editing")
				}
				item.Headers.Set("X-Review", "after")
				h.json(http.MethodPost, "/api/intercept/response/"+item.ID+"/forward", map[string]any{
					"statusCode": 200, "headers": item.Headers, "body": "",
				}, 204, nil)
				got := await(t, result)
				if got.status != 200 || got.body != fixture.body || got.headers.Get("X-Review") != "after" || got.headers.Get("Content-Encoding") != fixture.encoding {
					t.Fatalf("header-only edit changed message: status=%d bytes=%d want=%d header=%q encoding=%q", got.status, len(got.body), len(fixture.body), got.headers.Get("X-Review"), got.headers.Get("Content-Encoding"))
				}
				_ = h.waitHistory(1)
			})
		}
	}
}

func TestReplacementScopeBoundaryThroughAPI(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprintf("https=%t", secure), func(t *testing.T) {
			var requests atomic.Int32
			h := newHarness(t, secure, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "text/plain")
				_, _ = io.WriteString(w, "original")
			}))
			h.json(http.MethodPut, "/api/intercept/config", map[string]any{
				"enabled": true, "rules": []any{map[string]any{"enabled": true}},
				"responseEnabled": true, "responseRules": []any{map[string]any{"enabled": true}},
				"replacementRules": []any{
					map[string]any{"id": "escape", "enabled": true, "direction": "request", "target": "url", "pattern": "/allowed", "replacement": "/outside", "regex": false},
					map[string]any{"id": "body", "enabled": true, "direction": "response", "target": "body", "pattern": "original", "replacement": "changed", "regex": false},
				},
			}, 200, nil)
			outside := await(t, h.request(http.MethodGet, "/outside", ""))
			if outside.status != 200 || outside.body != "original" {
				t.Fatalf("out-of-scope traffic was modified or paused: %+v", outside)
			}
			_ = h.waitHistory(1)
			escape := await(t, h.request(http.MethodGet, "/allowed", ""))
			if escape.status != 403 || requests.Load() != 1 {
				t.Fatalf("scope escape forwarded: response=%+v upstream requests=%d", escape, requests.Load())
			}
			detail := h.waitHistory(2)
			if !detail.Error || !reflect.DeepEqual(detail.AppliedRuleIDs, []string{"escape"}) {
				t.Fatalf("scope rejection lost audit: %+v", detail)
			}
		})
	}
}

func TestResponseWorkflowThroughAPI(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprintf("https=%t", secure), func(t *testing.T) {
			seen := make(chan string, 1)
			h := newHarness(t, secure, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
				seen <- string(body)
				w.Header().Set("Content-Type", "text/plain")
				w.Header().Set("X-Upstream", "retained")
				_, _ = io.WriteString(w, "server-42")
			}))
			config := map[string]any{
				"enabled": false, "rules": []any{map[string]any{"enabled": true}},
				"responseEnabled": true, "responseRules": []any{map[string]any{"enabled": true}},
				"replacementRules": []any{
					map[string]any{"id": "req", "enabled": true, "direction": "request", "target": "body", "pattern": "client-([0-9]+)", "replacement": "request-${1}", "regex": true},
					map[string]any{"id": "res", "enabled": true, "direction": "response", "target": "body", "pattern": "server-([0-9]+)", "replacement": "automatic-${1}", "regex": true},
				},
			}
			h.json(http.MethodPut, "/api/intercept/config", config, 200, nil)
			result := h.request(http.MethodPost, "/allowed", "client-42")
			item := h.waitResponse()
			if item.Body != "automatic-42" || item.Headers.Get("X-Upstream") != "retained" {
				t.Fatalf("queued response before manual edit: %+v", item)
			}
			select {
			case body := <-seen:
				if body != "request-42" {
					t.Fatalf("upstream body = %q", body)
				}
			default:
				t.Fatal("response queued before upstream request observed")
			}
			var requests []any
			h.json(http.MethodGet, "/api/intercept/queue", nil, 200, &requests)
			if len(requests) != 0 {
				t.Fatalf("request interception enabled unexpectedly: %v", requests)
			}
			// A rejected edit must leave the pending response available for correction.
			h.json(http.MethodPost, "/api/intercept/response/"+item.ID+"/forward", map[string]any{"statusCode": 9999, "headers": item.Headers, "body": "bad"}, 400, nil)
			h.json(http.MethodPost, "/api/intercept/response/"+item.ID+"/forward", map[string]any{"statusCode": 202, "headers": item.Headers, "body": "manual-42"}, 204, nil)
			got := await(t, result)
			if got.status != 202 || got.body != "manual-42" || got.headers.Get("X-Upstream") != "retained" {
				t.Fatalf("client response = %+v", got)
			}
			detail := h.waitHistory(1)
			if detail.Response.Body != "manual-42" || detail.Request.Body != "request-42" || !detail.ResponseIntercepted || !reflect.DeepEqual(detail.AppliedRuleIDs, []string{"req", "res"}) {
				t.Fatalf("history does not match final messages/audit: %+v", detail)
			}
			// Disabling transformations must preserve independent response interception.
			config["replacementRules"] = []any{}
			h.json(http.MethodPut, "/api/intercept/config", config, 200, nil)
			result = h.request(http.MethodGet, "/allowed/drop", "")
			item = h.waitResponse()
			h.json(http.MethodPost, "/api/intercept/response/"+item.ID+"/drop", map[string]any{}, 204, nil)
			got = await(t, result)
			if got.status != 502 || !strings.Contains(got.body, "drop") {
				t.Fatalf("dropped response = %+v", got)
			}
			detail = h.waitHistory(2)
			if !detail.Error || !detail.ResponseIntercepted || detail.Status != 502 || detail.Response.Body != got.body {
				t.Fatalf("drop audit does not match client: %+v", detail)
			}
		})
	}
}

type harness struct {
	t        *testing.T
	api      *httptest.Server
	client   *http.Client
	upstream string
}

func newHarness(t *testing.T, secure bool, handler http.Handler) *harness {
	t.Helper()
	upstream := httptest.NewUnstartedServer(handler)
	if secure {
		upstream.StartTLS()
	} else {
		upstream.Start()
	}
	t.Cleanup(upstream.Close)
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	authority, err := certs.LoadOrCreateAuthority(filepath.Join(t.TempDir(), "ca"))
	if err != nil {
		t.Fatal(err)
	}
	rules, err := scope.Compile(1, []scope.Rule{{Enabled: true, Action: scope.ActionInclude, HostPattern: "127.0.0.1", PathPrefix: "/allowed"}})
	if err != nil {
		t.Fatal(err)
	}
	controller := intercept.NewController(intercept.NewQueue(10*time.Second), false, []intercept.Rule{{Enabled: true}})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	proxyServer := proxy.NewServer(proxy.Config{Store: repository, Intercept: controller, Scope: scope.NewManager(rules), Authority: authority, BodyLimitBytes: 1 << 20, Transport: upstream.Client().Transport})
	go func() { _ = proxyServer.Serve(listener) }()
	apiServer := httptest.NewServer(api.NewServer(api.Config{Store: repository, Intercept: controller}).Handler())
	t.Cleanup(apiServer.Close)
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(authority.CACertPEM()) {
		t.Fatal("parse proxy CA")
	}
	proxyURL := &url.URL{Scheme: "http", Host: listener.Addr().String()}
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	t.Cleanup(transport.CloseIdleConnections)
	return &harness{t: t, api: apiServer, client: &http.Client{Transport: transport, Timeout: 15 * time.Second}, upstream: upstream.URL}
}

func (h *harness) json(method, path string, payload any, want int, out any) {
	h.t.Helper()
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			h.t.Fatal(err)
		}
		body = bytes.NewReader(encoded)
	}
	r, err := http.NewRequest(method, h.api.URL+path, body)
	if err != nil {
		h.t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	response, err := h.api.Client().Do(r)
	if err != nil {
		h.t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != want {
		data, _ := io.ReadAll(response.Body)
		h.t.Fatalf("%s %s: status %d want %d, %s", method, path, response.StatusCode, want, data)
	}
	if out != nil {
		if err := json.NewDecoder(response.Body).Decode(out); err != nil {
			h.t.Fatal(err)
		}
	}
}

type pending struct {
	ID           string      `json:"id"`
	Body         string      `json:"body"`
	Headers      http.Header `json:"headers"`
	BodyEditable bool        `json:"bodyEditable"`
}

func (h *harness) waitResponse() pending {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var items []pending
		h.json(http.MethodGet, "/api/intercept/response-queue", nil, 200, &items)
		if len(items) > 0 {
			return items[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatal("response did not enter queue")
	return pending{}
}

type historyDetail struct {
	Status              int      `json:"status"`
	Error               bool     `json:"error"`
	ResponseIntercepted bool     `json:"responseIntercepted"`
	AppliedRuleIDs      []string `json:"appliedRuleIds"`
	Request             struct {
		Body string `json:"body"`
	} `json:"request"`
	Response struct {
		Body string `json:"body"`
	} `json:"response"`
}

func (h *harness) waitHistory(count int) historyDetail {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var items []struct {
			ID int64 `json:"id"`
		}
		h.json(http.MethodGet, "/api/history", nil, 200, &items)
		if len(items) >= count {
			var detail historyDetail
			h.json(http.MethodGet, fmt.Sprintf("/api/history/%d", items[0].ID), nil, 200, &detail)
			return detail
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatal("history did not complete")
	return historyDetail{}
}

type clientResult struct {
	status   int
	body     string
	headers  http.Header
	trailers http.Header
	err      error
}

func (h *harness) request(method, path, body string) <-chan clientResult {
	result := make(chan clientResult, 1)
	ctx, cancel := context.WithCancel(context.Background())
	h.t.Cleanup(cancel)
	go func() {
		r, err := http.NewRequestWithContext(ctx, method, h.upstream+path, strings.NewReader(body))
		if err != nil {
			result <- clientResult{err: err}
			return
		}
		r.Header.Set("Content-Type", "text/plain")
		r.Header.Set("Accept-Encoding", "identity")
		response, err := h.client.Do(r)
		if err != nil {
			result <- clientResult{err: err}
			return
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		result <- clientResult{status: response.StatusCode, body: string(data), headers: response.Header, trailers: response.Trailer.Clone(), err: err}
	}()
	return result
}

func await(t *testing.T, result <-chan clientResult) clientResult {
	t.Helper()
	select {
	case got := <-result:
		if got.err != nil {
			t.Fatal(got.err)
		}
		return got
	case <-time.After(15 * time.Second):
		t.Fatal("client request timed out")
	}
	return clientResult{}
}
