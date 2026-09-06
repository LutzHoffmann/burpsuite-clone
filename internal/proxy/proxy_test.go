package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/api"
	"github.com/lutzifer/burpsuite-clone/internal/certs"
	"github.com/lutzifer/burpsuite-clone/internal/events"
	"github.com/lutzifer/burpsuite-clone/internal/intercept"
	"github.com/lutzifer/burpsuite-clone/internal/scope"
	"github.com/lutzifer/burpsuite-clone/internal/store"
	"github.com/lutzifer/burpsuite-clone/internal/target"
)

func TestTargetScopeEndToEnd(t *testing.T) {
	harness := newTargetHarness(t)
	harness.ReplaceScope([]scope.Rule{{
		Enabled: true, Action: scope.ActionInclude, HostPattern: "localhost", PathPrefix: "/allowed",
	}})
	harness.WaitForRebuild()

	allowedHTTP := harness.HTTPURL("/allowed?q=private-query-value")
	outsideHTTP := harness.HTTPURL("/outside?secret=private-outside-value")
	allowedHTTPS := harness.HTTPSURL("localhost", "/allowed")
	harness.ProxyGET(allowedHTTP)
	harness.ProxyGET(outsideHTTP)
	harness.AssertTargetObservationCompleted(outsideHTTP)
	harness.ProxyGET(allowedHTTPS)
	harness.WaitForTarget(allowedHTTP, "q")
	harness.WaitForTarget(allowedHTTPS)

	history := harness.History()
	assertHistoryScope(t, history, "http", "/allowed", true, 1)
	assertHistoryScope(t, history, "http", "/outside", false, 1)
	assertHistoryScope(t, history, "https", "/allowed", true, 1)
	tree := harness.TargetTree()
	assertTreeContains(t, tree, "http", "/allowed")
	assertTreeContains(t, tree, "https", "/allowed")
	assertTreeOmits(t, tree, "/outside")
	assertParameterNames(t, harness.ParametersFor(allowedHTTP), "q")

	harness.EnableInterception()
	harness.ProxyGET(outsideHTTP)
	if queued := harness.intercept.Queue().List(); len(queued) != 0 {
		t.Fatalf("out-of-scope request entered interception queue: %#v", queued)
	}
	history = harness.History()
	if len(history) != 4 || history[0].Path != "/outside" || history[0].Intercepted {
		t.Fatalf("bypassed request was not forwarded and persisted: %#v", history)
	}

	harness.ReplaceScope([]scope.Rule{{
		Enabled: true, Action: scope.ActionInclude, HostPattern: "localhost", PathPrefix: "/",
	}})
	harness.WaitForRebuild()
	tree = harness.TargetTree()
	assertTreeContains(t, tree, "http", "/allowed")
	assertTreeContains(t, tree, "https", "/allowed")
	assertTreeContains(t, tree, "http", "/outside")

	harness.ReplaceScope([]scope.Rule{{
		Enabled: true, Action: scope.ActionInclude, HostPattern: "localhost", PathPrefix: "/outside",
	}})
	harness.WaitForRebuild()
	tree = harness.TargetTree()
	assertTreeContains(t, tree, "http", "/outside")
	assertTreeOmits(t, tree, "/allowed")
	assertHistoryScope(t, harness.History(), "http", "/allowed", true, 1)
	assertHistoryScope(t, harness.History(), "http", "/outside", false, 1)
	assertHistoryScope(t, harness.History(), "https", "/allowed", true, 1)
}

func TestTargetObservationBarrierWaitsForOutsideCompletion(t *testing.T) {
	barrier := newTargetObservationBarrier(targetObserverFunc(func(context.Context, *store.Exchange) error {
		return nil
	}))
	t.Cleanup(barrier.ReleaseCompletionBlock)
	if err := barrier.BlockNextCompletion(); err != nil {
		t.Fatal(err)
	}

	exchange := &store.Exchange{ID: 73, Path: "/outside", InScope: false}
	observed := make(chan error, 1)
	go func() { observed <- barrier.Observe(context.Background(), exchange) }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := barrier.WaitForCompletionBlock(ctx); err != nil {
		t.Fatal(err)
	}

	waited := make(chan struct {
		result targetObservationResult
		err    error
	}, 1)
	go func() {
		result, err := barrier.Wait(ctx, exchange.ID)
		waited <- struct {
			result targetObservationResult
			err    error
		}{result, err}
	}()
	if err := barrier.WaitForWaiter(ctx); err != nil {
		t.Fatal(err)
	}
	if status, observationErr := barrier.Status(exchange.ID); status != "waiting" || observationErr != "" {
		t.Fatalf("outside exchange observation status before release = %q, %q", status, observationErr)
	}
	select {
	case result := <-waited:
		t.Fatalf("outside observation wait completed before observer completion: %#v", result)
	default:
	}

	barrier.ReleaseCompletionBlock()
	select {
	case err := <-observed:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case result := <-waited:
		if result.err != nil || result.result.Error != "" {
			t.Fatalf("outside observation result = %#v", result)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

type targetHarness struct {
	t             *testing.T
	repository    *store.SQLiteStore
	target        *target.Service
	observer      *targetObservationBarrier
	intercept     *intercept.Controller
	httpTarget    *httptest.Server
	httpsTarget   *httptest.Server
	apiServer     *httptest.Server
	apiClient     *http.Client
	proxyListener net.Listener
	proxyDone     chan error
	client        *http.Client
	scopeVersion  int64
}

func newTargetHarness(t *testing.T) *targetHarness {
	t.Helper()
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "project.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	state, err := repository.LoadScopeState(context.Background())
	if err != nil {
		_ = repository.Close()
		t.Fatal(err)
	}
	compiled, err := scope.Compile(state.Version, state.Rules)
	if err != nil {
		_ = repository.Close()
		t.Fatal(err)
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("target response"))
	})
	httpTarget := httptest.NewServer(handler)
	httpsTarget := httptest.NewTLSServer(handler)
	hub := events.NewHub()
	manager := scope.NewManager(compiled)
	targetService := target.NewService(repository, manager, hub, target.Limits{
		MaxJSONDepth: 16, MaxFields: 1000, MaxMultipartFields: 100,
	})
	if err := targetService.Recover(context.Background()); err != nil {
		httpTarget.Close()
		httpsTarget.Close()
		_ = repository.Close()
		t.Fatal(err)
	}
	authority, err := certs.LoadOrCreateAuthority(t.TempDir())
	if err != nil {
		targetService.Close()
		httpTarget.Close()
		httpsTarget.Close()
		_ = repository.Close()
		t.Fatal(err)
	}
	controller := intercept.NewController(
		intercept.NewQueue(5*time.Second), false, []intercept.Rule{{Enabled: true}},
	)
	observer := newTargetObservationBarrier(targetService)
	apiServer := httptest.NewServer(api.NewServer(api.Config{
		Store: repository, Authority: authority, Events: hub, Intercept: controller, Target: targetService,
		APIAddr: "127.0.0.1:9080", ProxyAddr: "127.0.0.1:8080", MaxBodyBytes: 4096,
	}).Handler())
	listener := listenForTest(t)
	upstream := httpsTarget.Client().Transport.(*http.Transport).Clone()
	upstreamTLS := upstream.TLSClientConfig.Clone()
	upstreamTLS.ServerName = "example.com"
	upstream.TLSClientConfig = upstreamTLS
	proxyServer := NewServer(Config{
		Store: repository, BodyLimitBytes: 4096, Transport: upstream, Authority: authority,
		Events: hub, Intercept: controller, Scope: manager, Target: observer,
	})
	done := make(chan error, 1)
	go func() { done <- proxyServer.Serve(listener) }()

	harness := &targetHarness{
		t: t, repository: repository, target: targetService, observer: observer, intercept: controller,
		httpTarget: httpTarget, httpsTarget: httpsTarget, apiServer: apiServer,
		apiClient: apiServer.Client(), proxyListener: listener, proxyDone: done,
		client: proxyClient(listener.Addr().String(), authority), scopeVersion: state.Version,
	}
	t.Cleanup(func() {
		harness.ReleaseTargetObservationCompletion()
		harness.client.CloseIdleConnections()
		harness.apiClient.CloseIdleConnections()
		harness.apiServer.Close()
		_ = harness.proxyListener.Close()
		select {
		case <-harness.proxyDone:
		case <-time.After(time.Second):
			t.Error("proxy server did not stop")
		}
		harness.httpTarget.Close()
		harness.httpsTarget.Close()
		harness.target.Close()
		if err := harness.repository.Close(); err != nil {
			t.Error(err)
		}
	})
	return harness
}

func (h *targetHarness) ReplaceScope(rules []scope.Rule) {
	h.t.Helper()
	payload, err := json.Marshal(struct {
		Version int64        `json:"version"`
		Rules   []scope.Rule `json:"rules"`
	}{Version: h.scopeVersion, Rules: rules})
	if err != nil {
		h.t.Fatal(err)
	}
	response := h.apiRequest(http.MethodPut, "/api/scope/rules", bytes.NewReader(payload))
	var state scope.State
	if err := json.Unmarshal(response, &state); err != nil {
		h.t.Fatalf("decode scope state: %v; body = %q", err, response)
	}
	h.scopeVersion = state.Version
}

func (h *targetHarness) ProxyGET(rawURL string) {
	h.t.Helper()
	if err := h.proxyGET(rawURL); err != nil {
		h.t.Fatal(err)
	}
}

func (h *targetHarness) proxyGET(rawURL string) error {
	before, err := h.historyContext(context.Background())
	if err != nil {
		return err
	}
	response, err := h.client.Get(rawURL)
	if err != nil {
		return fmt.Errorf("proxy GET %s: %w", rawURL, err)
	}
	defer response.Body.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	exchangeID, err := h.waitForNewHistoryExchange(ctx, before)
	if err != nil {
		return fmt.Errorf("wait for persisted exchange after proxy GET %s: %w; %s", rawURL, err, h.targetObservationDiagnostics(rawURL, 0))
	}
	observation, err := h.observer.Wait(ctx, exchangeID)
	if err != nil {
		return fmt.Errorf("timed out waiting for Target observation of exchange %d after proxy GET %s: %w; %s", exchangeID, rawURL, err, h.targetObservationDiagnostics(rawURL, exchangeID))
	}
	if observation.Error != "" {
		return fmt.Errorf("Target observation of exchange %d after proxy GET %s completed with error: %s; %s", exchangeID, rawURL, observation.Error, h.targetObservationDiagnostics(rawURL, exchangeID))
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		return fmt.Errorf("read proxy response for %s: read=%v close=%v", rawURL, readErr, closeErr)
	}
	if response.StatusCode != http.StatusOK || string(body) != "target response" {
		return fmt.Errorf("proxy GET %s: status=%d body=%q", rawURL, response.StatusCode, body)
	}
	return nil
}

func (h *targetHarness) HTTPURL(path string) string {
	return strings.Replace(h.httpTarget.URL, "127.0.0.1", "localhost", 1) + path
}

func (h *targetHarness) HTTPSURL(host, path string) string {
	h.t.Helper()
	parsed := mustParseURL(h.t, h.httpsTarget.URL)
	parsed.Host = net.JoinHostPort(host, parsed.Port())
	parsed.Path = path
	return parsed.String()
}

func (h *targetHarness) History() []store.HistoryItem {
	h.t.Helper()
	history, err := h.historyContext(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	return history
}

func (h *targetHarness) historyContext(ctx context.Context) ([]store.HistoryItem, error) {
	var history []store.HistoryItem
	body, err := h.apiRequestContext(ctx, http.MethodGet, "/api/history", nil)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, &history); err != nil {
		return nil, fmt.Errorf("decode History: %v; body = %q", err, body)
	}
	return history, nil
}

func (h *targetHarness) TargetTree() []store.TargetTreeNode {
	h.t.Helper()
	var tree []store.TargetTreeNode
	body := h.apiRequest(http.MethodGet, "/api/target/tree", nil)
	if err := json.Unmarshal(body, &tree); err != nil {
		h.t.Fatalf("decode Target tree: %v; body = %q", err, body)
	}
	return tree
}

func (h *targetHarness) ParametersFor(rawURL string) []store.TargetParameter {
	h.t.Helper()
	parsed := mustParseURL(h.t, rawURL)
	endpointID := targetEndpointIDForURL(h.TargetTree(), parsed)
	if endpointID == 0 {
		h.t.Fatalf("no GET endpoint for %s in Target tree: %#v", rawURL, h.TargetTree())
	}
	body := h.apiRequest(http.MethodGet, "/api/target/endpoints/"+strconv.FormatInt(endpointID, 10)+"/parameters", nil)
	var wire []map[string]json.RawMessage
	if err := json.Unmarshal(body, &wire); err != nil {
		h.t.Fatalf("decode parameter wire payload: %v; body = %q", err, body)
	}
	for _, parameter := range wire {
		if value, exists := parameter["value"]; exists {
			h.t.Fatalf("parameter value field leaked: %s (%s)", body, value)
		}
	}
	for _, values := range parsed.Query() {
		for _, value := range values {
			if value != "" && bytes.Contains(body, []byte(value)) {
				h.t.Fatalf("parameter value %q leaked: %s", value, body)
			}
		}
	}
	var parameters []store.TargetParameter
	if err := json.Unmarshal(body, &parameters); err != nil {
		h.t.Fatalf("decode parameters: %v; body = %q", err, body)
	}
	return parameters
}

func (h *targetHarness) WaitForRebuild() {
	h.t.Helper()
	var last store.RebuildStatus
	timer := time.NewTimer(3 * time.Second)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer timer.Stop()
	defer ticker.Stop()
	for {
		body := h.apiRequest(http.MethodGet, "/api/target/rebuild", nil)
		if err := json.Unmarshal(body, &last); err != nil {
			h.t.Fatalf("decode rebuild status: %v; body = %q", err, body)
		}
		if last.Status == "active" && last.ScopeVersion == h.scopeVersion && last.ActiveScopeVersion == h.scopeVersion {
			return
		}
		select {
		case <-ticker.C:
		case <-timer.C:
			h.t.Fatalf("timed out waiting for Target rebuild; status=%#v History=%#v TargetTree=%#v", last, h.History(), h.TargetTree())
		}
	}
}

func (h *targetHarness) WaitForTarget(rawURL string, parameterNames ...string) {
	h.t.Helper()
	targetURL := mustParseURL(h.t, rawURL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()

	var lastHistory []store.HistoryItem
	var lastTree []store.TargetTreeNode
	var lastStatus store.RebuildStatus
	var lastParameters []store.TargetParameter
	var lastErrors []string
	for {
		lastErrors = lastErrors[:0]
		if body, err := h.apiRequestContext(ctx, http.MethodGet, "/api/history", nil); err != nil {
			lastErrors = append(lastErrors, "History: "+err.Error())
		} else if err := json.Unmarshal(body, &lastHistory); err != nil {
			lastErrors = append(lastErrors, fmt.Sprintf("decode History: %v; body=%q", err, body))
		}
		if body, err := h.apiRequestContext(ctx, http.MethodGet, "/api/target/tree", nil); err != nil {
			lastErrors = append(lastErrors, "Target tree: "+err.Error())
		} else if err := json.Unmarshal(body, &lastTree); err != nil {
			lastErrors = append(lastErrors, fmt.Sprintf("decode Target tree: %v; body=%q", err, body))
		}
		if body, err := h.apiRequestContext(ctx, http.MethodGet, "/api/target/rebuild", nil); err != nil {
			lastErrors = append(lastErrors, "rebuild status: "+err.Error())
		} else if err := json.Unmarshal(body, &lastStatus); err != nil {
			lastErrors = append(lastErrors, fmt.Sprintf("decode rebuild status: %v; body=%q", err, body))
		}

		endpointID := targetEndpointIDForURL(lastTree, targetURL)
		lastParameters = nil
		if endpointID == 0 {
			lastErrors = append(lastErrors, "endpoint not present in Target tree")
		} else {
			path := "/api/target/endpoints/" + strconv.FormatInt(endpointID, 10) + "/parameters"
			if body, err := h.apiRequestContext(ctx, http.MethodGet, path, nil); err != nil {
				lastErrors = append(lastErrors, "parameters: "+err.Error())
			} else if err := json.Unmarshal(body, &lastParameters); err != nil {
				lastErrors = append(lastErrors, fmt.Sprintf("decode parameters: %v; body=%q", err, body))
			}
		}
		if len(lastErrors) == 0 && parameterNamesEqual(lastParameters, parameterNames) {
			return
		}

		select {
		case <-ticker.C:
		case <-ctx.Done():
			h.t.Fatalf(
				"timed out waiting for Target projection of %s; History=%#v TargetTree=%#v RebuildStatus=%#v Parameters=%#v QueryErrors=%#v",
				rawURL, lastHistory, lastTree, lastStatus, lastParameters, lastErrors,
			)
		}
	}
}

func (h *targetHarness) AssertTargetObservationCompleted(rawURL string) {
	h.t.Helper()
	parsed := mustParseURL(h.t, rawURL)
	var matches []store.HistoryItem
	for _, item := range h.History() {
		if item.Method == http.MethodGet && item.Scheme == parsed.Scheme && item.Host == parsed.Host && item.Path == parsed.Path && item.Query == parsed.RawQuery {
			matches = append(matches, item)
		}
	}
	if len(matches) != 1 {
		h.t.Fatalf("History matches for Target observation of %s = %#v; History=%#v", rawURL, matches, h.History())
	}
	if status, observationErr := h.observer.Status(matches[0].ID); status != "completed" || observationErr != "" {
		h.t.Fatalf("Target observation did not complete for out-of-scope exchange %d; %s", matches[0].ID, h.targetObservationDiagnostics(rawURL, matches[0].ID))
	}
}

func (h *targetHarness) ReleaseTargetObservationCompletion() {
	h.observer.ReleaseCompletionBlock()
}

func (h *targetHarness) waitForNewHistoryExchange(ctx context.Context, before []store.HistoryItem) (int64, error) {
	known := make(map[int64]struct{}, len(before))
	for _, item := range before {
		known[item.ID] = struct{}{}
	}
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		history, err := h.historyContext(ctx)
		if err != nil {
			return 0, err
		}
		var added []store.HistoryItem
		for _, item := range history {
			if _, exists := known[item.ID]; !exists {
				added = append(added, item)
			}
		}
		if len(added) == 1 && added[0].ID != 0 {
			return added[0].ID, nil
		}
		if len(added) > 1 {
			return 0, fmt.Errorf("history added %d exchanges, want exactly one: %#v", len(added), added)
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
}

func (h *targetHarness) targetObservationDiagnostics(rawURL string, exchangeID int64) string {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	status, observationErr := h.observer.Status(exchangeID)
	var history []store.HistoryItem
	var tree []store.TargetTreeNode
	var rebuild store.RebuildStatus
	var parameters []store.TargetParameter
	var queryErrors []string
	if result, err := h.historyContext(ctx); err != nil {
		queryErrors = append(queryErrors, "History: "+err.Error())
	} else {
		history = result
	}
	if body, err := h.apiRequestContext(ctx, http.MethodGet, "/api/target/tree", nil); err != nil {
		queryErrors = append(queryErrors, "Target tree: "+err.Error())
	} else if err := json.Unmarshal(body, &tree); err != nil {
		queryErrors = append(queryErrors, fmt.Sprintf("decode Target tree: %v; body=%q", err, body))
	}
	if body, err := h.apiRequestContext(ctx, http.MethodGet, "/api/target/rebuild", nil); err != nil {
		queryErrors = append(queryErrors, "rebuild status: "+err.Error())
	} else if err := json.Unmarshal(body, &rebuild); err != nil {
		queryErrors = append(queryErrors, fmt.Sprintf("decode rebuild status: %v; body=%q", err, body))
	}
	if rawURL != "" {
		parsed, err := url.Parse(rawURL)
		if err != nil {
			queryErrors = append(queryErrors, "parse target URL: "+err.Error())
		} else if endpointID := targetEndpointIDForURL(tree, parsed); endpointID == 0 {
			queryErrors = append(queryErrors, "endpoint not present in Target tree")
		} else {
			path := "/api/target/endpoints/" + strconv.FormatInt(endpointID, 10) + "/parameters"
			if body, err := h.apiRequestContext(ctx, http.MethodGet, path, nil); err != nil {
				queryErrors = append(queryErrors, "parameters: "+err.Error())
			} else if err := json.Unmarshal(body, &parameters); err != nil {
				queryErrors = append(queryErrors, fmt.Sprintf("decode parameters: %v; body=%q", err, body))
			}
		}
	}
	return fmt.Sprintf(
		"ExchangeID=%d ObservationStatus=%q ObservationError=%q History=%#v TargetTree=%#v RebuildStatus=%#v Parameters=%#v QueryErrors=%#v",
		exchangeID, status, observationErr, history, tree, rebuild, parameters, queryErrors,
	)
}

func (h *targetHarness) EnableInterception() {
	h.intercept.Update(intercept.ControllerState{Enabled: true, Rules: []intercept.Rule{{Enabled: true}}})
}

func (h *targetHarness) apiRequest(method, path string, body io.Reader) []byte {
	h.t.Helper()
	responseBody, err := h.apiRequestContext(context.Background(), method, path, body)
	if err != nil {
		h.t.Fatal(err)
	}
	return responseBody
}

func (h *targetHarness) apiRequestContext(ctx context.Context, method, path string, body io.Reader) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, h.apiServer.URL+path, body)
	if err != nil {
		return nil, fmt.Errorf("API %s %s: %w", method, path, err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := h.apiClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("API %s %s: %w", method, path, err)
	}
	responseBody, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		return nil, fmt.Errorf("read API %s %s: read=%v close=%v", method, path, readErr, closeErr)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("API %s %s: status=%d body=%q", method, path, response.StatusCode, responseBody)
	}
	return responseBody, nil
}

func (h *targetHarness) waitUntil(operation string, ready func() bool) {
	h.t.Helper()
	timer := time.NewTimer(3 * time.Second)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer timer.Stop()
	defer ticker.Stop()
	for {
		if ready() {
			return
		}
		select {
		case <-ticker.C:
		case <-timer.C:
			h.t.Fatalf("timed out waiting for %s; History=%#v TargetTree=%#v", operation, h.History(), h.TargetTree())
		}
	}
}

func assertHistoryScope(t *testing.T, history []store.HistoryItem, scheme, path string, inScope bool, version int64) {
	t.Helper()
	found := false
	for _, item := range history {
		if item.Scheme != scheme || item.Path != path {
			continue
		}
		found = true
		if item.InScope != inScope || item.ScopeVersion != version || (inScope && item.ScopeRuleID == nil) || (!inScope && item.ScopeRuleID != nil) {
			t.Fatalf("History scope for %s %s is incorrect: item=%#v History=%#v", scheme, path, item, history)
		}
	}
	if !found {
		t.Fatalf("History has no %s %s entry: %#v", scheme, path, history)
	}
}

func assertTreeContains(t *testing.T, tree []store.TargetTreeNode, scheme, path string) {
	t.Helper()
	observed := targetEndpointPaths(tree)
	wanted := "GET " + scheme + " " + path
	for _, endpoint := range observed {
		if endpoint == wanted {
			return
		}
	}
	t.Fatalf("Target tree does not contain %q; endpoints=%#v tree=%#v", wanted, observed, tree)
}

func assertTreeOmits(t *testing.T, tree []store.TargetTreeNode, path string) {
	t.Helper()
	observed := targetEndpointPaths(tree)
	for _, endpoint := range observed {
		if strings.HasSuffix(endpoint, " "+path) {
			t.Fatalf("Target tree contains omitted path %q; endpoints=%#v tree=%#v", path, observed, tree)
		}
	}
}

func assertParameterNames(t *testing.T, parameters []store.TargetParameter, names ...string) {
	t.Helper()
	observed := make([]string, len(parameters))
	for index, parameter := range parameters {
		observed[index] = parameter.Name
	}
	if fmt.Sprint(observed) != fmt.Sprint(names) {
		t.Fatalf("parameter names=%#v, want=%#v; parameters=%#v", observed, names, parameters)
	}
}

func parameterNamesEqual(parameters []store.TargetParameter, names []string) bool {
	if len(parameters) != len(names) {
		return false
	}
	for index := range parameters {
		if parameters[index].Name != names[index] {
			return false
		}
	}
	return true
}

func targetEndpointPaths(tree []store.TargetTreeNode) []string {
	var endpoints []string
	var visit func([]store.TargetTreeNode, string, string)
	visit = func(nodes []store.TargetTreeNode, scheme, path string) {
		for _, node := range nodes {
			nodeScheme := scheme
			if node.Scheme != "" {
				nodeScheme = node.Scheme
			}
			nodePath := path
			if node.Path != "" {
				nodePath += "/" + node.Path
			}
			if node.Method != "" {
				endpoints = append(endpoints, node.Method+" "+nodeScheme+" "+nodePath)
			}
			visit(node.Children, nodeScheme, nodePath)
		}
	}
	visit(tree, "", "")
	return endpoints
}

func targetEndpointIDForURL(tree []store.TargetTreeNode, targetURL *url.URL) int64 {
	port, _ := strconv.Atoi(targetURL.Port())
	var visit func([]store.TargetTreeNode, string, string, int) int64
	visit = func(nodes []store.TargetTreeNode, scheme, path string, inheritedPort int) int64 {
		for _, node := range nodes {
			nodeScheme, nodePort := scheme, inheritedPort
			if node.Scheme != "" {
				nodeScheme = node.Scheme
			}
			if node.Port != 0 {
				nodePort = node.Port
			}
			nodePath := path
			if node.Path != "" {
				nodePath += "/" + node.Path
			}
			if node.ID != 0 && node.Method == http.MethodGet && nodeScheme == targetURL.Scheme && nodePort == port && nodePath == targetURL.Path {
				return node.ID
			}
			if id := visit(node.Children, nodeScheme, nodePath, nodePort); id != 0 {
				return id
			}
		}
		return 0
	}
	return visit(tree, "", "", 0)
}

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

func TestProxyPublishesHistoryEventBeforeTargetObservation(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(target.Close)
	mem := &memoryStore{}
	hub := events.NewHub()
	subscriber, unsubscribe := hub.Subscribe()
	t.Cleanup(unsubscribe)
	observerCalled := false
	var observerErr error
	srv := NewServer(Config{
		Store: mem, BodyLimitBytes: 1024, Events: hub,
		Target: targetObserverFunc(func(_ context.Context, exchange *store.Exchange) error {
			observerCalled = true
			if exchange.ID == 0 {
				observerErr = errors.New("exchange has no durable ID")
				return observerErr
			}
			saved := mem.snapshot()
			if len(saved) != 1 || saved[0].ID != exchange.ID {
				observerErr = fmt.Errorf("saved exchanges = %#v", saved)
				return observerErr
			}
			select {
			case event := <-subscriber:
				data, ok := event.Data.(map[string]interface{})
				if event.Type != "history.entry.created" || !ok || data["id"] != exchange.ID {
					observerErr = fmt.Errorf("history event = %#v", event)
					return observerErr
				}
			default:
				observerErr = errors.New("history event was not observable inside target observer")
				return observerErr
			}
			return nil
		}),
	})

	recorder := httptest.NewRecorder()
	srv.handleHTTP(recorder, httptest.NewRequest(http.MethodGet, target.URL+"/ordered", nil))

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d", recorder.Code)
	}
	if !observerCalled || observerErr != nil {
		t.Fatalf("observer called = %t, error = %v", observerCalled, observerErr)
	}
}

func TestProxyStoresCaptureTimeScopeDecisionWhenRulesChangeDuringInterception(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(target.Close)
	targetURL := mustParseURL(t, target.URL)
	initial, err := scope.Compile(31, []scope.Rule{{
		ID: 301, Enabled: true, Action: scope.ActionInclude, HostPattern: targetURL.Hostname(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := scope.Compile(32, []scope.Rule{{
		ID: 302, Enabled: true, Action: scope.ActionInclude, HostPattern: targetURL.Hostname(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	manager := scope.NewManager(initial)
	queue := intercept.NewQueue(time.Second)
	queued := make(chan intercept.Item, 1)
	queue.SetObserver(func(change intercept.Change) {
		if change.Type == "queued" {
			queued <- change.Item
		}
	})
	mem := &memoryStore{}
	srv := NewServer(Config{
		Store: mem, BodyLimitBytes: 1024, Scope: manager,
		Intercept: intercept.NewController(queue, true, []intercept.Rule{{Enabled: true}}),
	})
	proxyServer := httptest.NewServer(http.HandlerFunc(srv.handleHTTP))
	t.Cleanup(proxyServer.Close)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(mustParseURL(t, proxyServer.URL))}}
	t.Cleanup(client.CloseIdleConnections)
	responses := make(chan *http.Response, 1)
	errs := make(chan error, 1)
	go func() {
		response, err := client.Get(target.URL + "/capture-time")
		if err != nil {
			errs <- err
			return
		}
		responses <- response
	}()

	var item intercept.Item
	select {
	case item = <-queued:
	case <-time.After(time.Second):
		t.Fatal("request did not enter interception")
	}
	manager.Replace(replacement)
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
	case <-time.After(time.Second):
		t.Fatal("forwarded request did not complete")
	}

	exchange := waitForSavedExchanges(t, mem, 1)[0]
	assertScopeDecision(t, exchange, true, 31, 301)
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

type targetObservationResult struct {
	Error string
}

type targetObservationPause struct {
	entered     chan struct{}
	release     chan struct{}
	enteredOnce sync.Once
	releaseOnce sync.Once
}

func newTargetObservationPause() *targetObservationPause {
	return &targetObservationPause{entered: make(chan struct{}), release: make(chan struct{})}
}

func (p *targetObservationPause) releaseBlock() {
	p.releaseOnce.Do(func() { close(p.release) })
}

// targetObservationBarrier adds completion tracking to the real Target service.
// It records an exchange only after the delegated synchronous Observe call returns.
type targetObservationBarrier struct {
	target TargetObserver

	mu          sync.Mutex
	completed   map[int64]targetObservationResult
	waiters     map[int64]int
	changed     chan struct{}
	nextPause   *targetObservationPause
	activePause *targetObservationPause
}

func newTargetObservationBarrier(target TargetObserver) *targetObservationBarrier {
	return &targetObservationBarrier{
		target: target, completed: make(map[int64]targetObservationResult),
		waiters: make(map[int64]int), changed: make(chan struct{}),
	}
}

func (o *targetObservationBarrier) Observe(ctx context.Context, exchange *store.Exchange) error {
	err := o.target.Observe(ctx, exchange)
	if exchange == nil || exchange.ID == 0 {
		return err
	}

	o.mu.Lock()
	pause := o.nextPause
	if pause != nil {
		o.nextPause = nil
		o.activePause = pause
	}
	o.mu.Unlock()
	if pause != nil {
		pause.enteredOnce.Do(func() { close(pause.entered) })
		<-pause.release
	}

	result := targetObservationResult{}
	if err != nil {
		result.Error = err.Error()
	}
	o.mu.Lock()
	o.completed[exchange.ID] = result
	if o.activePause == pause {
		o.activePause = nil
	}
	o.signalLocked()
	o.mu.Unlock()
	return err
}

func (o *targetObservationBarrier) Wait(ctx context.Context, exchangeID int64) (targetObservationResult, error) {
	registered := false
	defer func() {
		if !registered {
			return
		}
		o.mu.Lock()
		o.waiters[exchangeID]--
		if o.waiters[exchangeID] == 0 {
			delete(o.waiters, exchangeID)
		}
		o.signalLocked()
		o.mu.Unlock()
	}()
	for {
		o.mu.Lock()
		if result, complete := o.completed[exchangeID]; complete {
			o.mu.Unlock()
			return result, nil
		}
		if !registered {
			o.waiters[exchangeID]++
			registered = true
			o.signalLocked()
		}
		changed := o.changed
		o.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return targetObservationResult{}, ctx.Err()
		}
	}
}

func (o *targetObservationBarrier) BlockNextCompletion() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.nextPause != nil || o.activePause != nil {
		return errors.New("Target observation completion block is already active")
	}
	o.nextPause = newTargetObservationPause()
	return nil
}

func (o *targetObservationBarrier) WaitForCompletionBlock(ctx context.Context) error {
	o.mu.Lock()
	pause := o.activePause
	if pause == nil {
		pause = o.nextPause
	}
	o.mu.Unlock()
	if pause == nil {
		return errors.New("Target observation completion block is not active")
	}
	select {
	case <-pause.entered:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (o *targetObservationBarrier) WaitForWaiter(ctx context.Context) error {
	for {
		o.mu.Lock()
		for _, count := range o.waiters {
			if count > 0 {
				o.mu.Unlock()
				return nil
			}
		}
		changed := o.changed
		o.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (o *targetObservationBarrier) ReleaseCompletionBlock() {
	o.mu.Lock()
	pause := o.activePause
	if pause == nil {
		pause = o.nextPause
	}
	o.mu.Unlock()
	if pause != nil {
		pause.releaseBlock()
	}
}

func (o *targetObservationBarrier) Status(exchangeID int64) (string, string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if result, complete := o.completed[exchangeID]; complete {
		if result.Error != "" {
			return "error", result.Error
		}
		return "completed", ""
	}
	if o.waiters[exchangeID] > 0 {
		return "waiting", ""
	}
	return "pending", ""
}

func (o *targetObservationBarrier) signalLocked() {
	close(o.changed)
	o.changed = make(chan struct{})
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
