package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/certs"
	"github.com/lutzifer/burpsuite-clone/internal/events"
	"github.com/lutzifer/burpsuite-clone/internal/intercept"
	"github.com/lutzifer/burpsuite-clone/internal/repeater"
	"github.com/lutzifer/burpsuite-clone/internal/scope"
	"github.com/lutzifer/burpsuite-clone/internal/store"
	"github.com/lutzifer/burpsuite-clone/internal/target"
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

func TestStatusWithoutAuthorityDisablesHTTPSInterception(t *testing.T) {
	srv := NewServer(Config{
		APIAddr:   "127.0.0.1:9080",
		ProxyAddr: "127.0.0.1:8080",
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status code = %d", resp.StatusCode)
	}

	var status struct {
		CAFingerprint     string `json:"caFingerprint"`
		CATrust           string `json:"caTrust"`
		HTTPSInterception bool   `json:"httpsInterception"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.CAFingerprint != "" {
		t.Fatalf("ca fingerprint = %q", status.CAFingerprint)
	}
	if status.CATrust != "unavailable" {
		t.Fatalf("ca trust = %q", status.CATrust)
	}
	if status.HTTPSInterception {
		t.Fatal("https interception is enabled without an authority")
	}
}

func TestEmptyHistoryIsJSONArray(t *testing.T) {
	srv := NewServer(Config{Store: store.NewMemoryForTests(), APIAddr: "127.0.0.1:9080"})
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9080/api/history", nil)
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, request)
	if strings.TrimSpace(recorder.Body.String()) != "[]" {
		t.Fatalf("body = %q", recorder.Body.String())
	}
}

func TestRootServesOperatorFallback(t *testing.T) {
	srv := NewServer(Config{})
	server := httptest.NewServer(srv.Handler())
	defer server.Close()

	response, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte("BurpSuite Clone")) {
		t.Fatalf("fallback body = %q", body)
	}
}

func TestRepeaterSend(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != "request payload" {
			t.Fatalf("request body = %q", body)
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

	body, err := json.Marshal(map[string]interface{}{
		"method":  http.MethodPost,
		"url":     target.URL,
		"body":    "request payload",
		"headers": map[string][]string{},
	})
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

	var result struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Body != "sent" {
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

func TestAPIRejectsUntrustedHost(t *testing.T) {
	srv := NewServer(Config{APIAddr: "127.0.0.1:9080"})
	request := httptest.NewRequest(http.MethodGet, "http://attacker.example/api/status", nil)
	request.Host = "attacker.example"
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusMisdirectedRequest {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestAPIRejectsCrossSiteOriginForStateChange(t *testing.T) {
	srv := NewServer(Config{APIAddr: "127.0.0.1:9080"})
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9080/api/intercept/missing/drop", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://attacker.example")
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestAPIRejectsMismatchedOriginSchemeForStateChange(t *testing.T) {
	srv := NewServer(Config{APIAddr: "127.0.0.1:9080"})
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9080/api/intercept/missing/drop", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://127.0.0.1:9080")
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestAPIAllowsMatchingOriginForStateChange(t *testing.T) {
	srv := NewServer(Config{APIAddr: "127.0.0.1:9080"})
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9080/api/intercept/missing/drop", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://127.0.0.1:9080")
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestAPIRejectsCrossSiteWebSocketOrigin(t *testing.T) {
	srv := NewServer(Config{APIAddr: "127.0.0.1:9080"})
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9080/api/events", nil)
	request.Header.Set("Origin", "https://attacker.example")
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestAPIRejectsBadJSONContentType(t *testing.T) {
	srv := NewServer(Config{Repeater: repeater.NewService(http.DefaultTransport, 1024), APIAddr: "127.0.0.1:9080"})
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9080/api/repeater/sessions/7/send", strings.NewReader(`{"method":"GET","url":"http://example.test"}`))
	request.Header.Set("Content-Type", "text/plain")
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestAPIRejectsOversizedJSON(t *testing.T) {
	srv := NewServer(Config{
		Repeater: repeater.NewService(http.DefaultTransport, 1024),
		APIAddr:  "127.0.0.1:9080", MaxBodyBytes: 64,
	})
	body := `{"method":"POST","url":"http://example.test","body":"` + strings.Repeat("x", 128) + `"}`
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9080/api/repeater/sessions/7/send", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, body = %q", recorder.Code, recorder.Body.String())
	}
}

func TestHistoryDetailUsesTextSafeDTOAndMilliseconds(t *testing.T) {
	memory := store.NewMemoryForTests()
	exchange := &store.Exchange{
		Method: "POST", Scheme: "https", Host: "example.test", Path: "/submit",
		Status: 201, MIMEType: "text/plain", Duration: 1250 * time.Millisecond,
		StartedAt: time.Unix(1700000000, 0).UTC(), RequestTruncated: true,
		Request:  store.RequestData{Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"ok":true}`)},
		Response: store.ResponseData{Headers: http.Header{"Content-Type": {"text/plain"}}, Body: []byte("created")},
	}
	if err := memory.SaveExchange(context.Background(), exchange); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(Config{Store: memory, APIAddr: "127.0.0.1:9080"})
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9080/api/history/1", nil)
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	var detail struct {
		ID         int64 `json:"id"`
		DurationMS int64 `json:"durationMs"`
		Request    struct {
			Body      string `json:"body"`
			TextSafe  bool   `json:"textSafe"`
			Truncated bool   `json:"truncated"`
		} `json:"request"`
		Response struct {
			Body     string `json:"body"`
			TextSafe bool   `json:"textSafe"`
		} `json:"response"`
	}
	payload := append([]byte(nil), recorder.Body.Bytes()...)
	if err := json.Unmarshal(payload, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.ID != 1 || detail.DurationMS != 1250 {
		t.Fatalf("detail = %+v", detail)
	}
	if detail.Request.Body != `{"ok":true}` || !detail.Request.TextSafe || !detail.Request.Truncated {
		t.Fatalf("request = %+v", detail.Request)
	}
	if detail.Response.Body != "created" || !detail.Response.TextSafe {
		t.Fatalf("response = %+v", detail.Response)
	}
	if bytes.Contains(payload, []byte(`"ID"`)) || bytes.Contains(payload, []byte("eyJvayI")) {
		t.Fatalf("storage wire format leaked: %s", payload)
	}
}

func TestHistoryDetailOmitsBinaryBodyFromTextField(t *testing.T) {
	memory := store.NewMemoryForTests()
	exchange := &store.Exchange{
		Method: "GET", Scheme: "https", Host: "example.test", Path: "/binary",
		MIMEType: "application/octet-stream", StartedAt: time.Now().UTC(),
		Response: store.ResponseData{
			Headers: http.Header{"Content-Type": {"application/octet-stream"}},
			Body:    []byte{0xff, 0x00, 0x01},
		},
	}
	if err := memory.SaveExchange(context.Background(), exchange); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(Config{Store: memory, APIAddr: "127.0.0.1:9080"})
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9080/api/history/1", nil)
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, request)
	var detail struct {
		Response struct {
			Body     string `json:"body"`
			TextSafe bool   `json:"textSafe"`
		} `json:"response"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	if detail.Response.Body != "" || detail.Response.TextSafe {
		t.Fatalf("response = %+v", detail.Response)
	}
}

func TestInterceptQueueAPIListsAndForwardsEditedRequest(t *testing.T) {
	queue := intercept.NewQueue(time.Second)
	controller := intercept.NewController(queue, true, []intercept.Rule{{Enabled: true}})
	decision := make(chan intercept.Decision, 1)
	go func() {
		result, _ := queue.Enqueue(context.Background(), intercept.Item{
			ID: "queued-1", Method: "POST", URL: "http://example.test/original",
			Headers: http.Header{"Content-Type": {"text/plain"}}, Body: []byte("before"), BodyEditable: true,
		})
		decision <- result
	}()
	waitForAPIQueue(t, queue)

	srv := NewServer(Config{Intercept: controller, APIAddr: "127.0.0.1:9080"})
	listRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9080/api/intercept/queue", nil)
	listRecorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(listRecorder, listRequest)
	if listRecorder.Code != http.StatusOK || !strings.Contains(listRecorder.Body.String(), `"body":"before"`) {
		t.Fatalf("list status = %d, body = %s", listRecorder.Code, listRecorder.Body.String())
	}

	forwardBody := `{"method":"PUT","url":"http://example.test/edited","headers":{"Content-Type":["text/plain"]},"body":"after"}`
	forwardRequest := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9080/api/intercept/queued-1/forward", strings.NewReader(forwardBody))
	forwardRequest.Header.Set("Content-Type", "application/json")
	forwardRecorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(forwardRecorder, forwardRequest)
	if forwardRecorder.Code != http.StatusNoContent {
		t.Fatalf("forward status = %d, body = %q", forwardRecorder.Code, forwardRecorder.Body.String())
	}
	result := <-decision
	if result.Edit.Method != "PUT" || string(result.Edit.Body) != "after" {
		t.Fatalf("decision = %+v", result)
	}
}

func TestInterceptQueueChangesPublishEvents(t *testing.T) {
	queue := intercept.NewQueue(time.Second)
	controller := intercept.NewController(queue, true, []intercept.Rule{{Enabled: true}})
	hub := events.NewHub()
	subscriber, unsubscribe := hub.Subscribe()
	defer unsubscribe()
	_ = NewServer(Config{Intercept: controller, Events: hub, APIAddr: "127.0.0.1:9080"})
	done := make(chan struct{})
	go func() {
		_, _ = queue.Enqueue(context.Background(), intercept.Item{ID: "event-1", Method: "GET", URL: "http://example.test/"})
		close(done)
	}()
	queued := <-subscriber
	if queued.Type != "intercept.item.queued" {
		t.Fatalf("queued event = %+v", queued)
	}
	if err := queue.Drop("event-1"); err != nil {
		t.Fatal(err)
	}
	completed := <-subscriber
	if completed.Type != "intercept.item.completed" {
		t.Fatalf("completed event = %+v", completed)
	}
	<-done
}

func TestRepeaterSendHistoryIsExposedByAPI(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("persisted response"))
	}))
	defer target.Close()
	projectStore, err := store.OpenSQLite(filepath.Join(t.TempDir(), "project.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer projectStore.Close()
	srv := NewServer(Config{
		Store: projectStore, Repeater: repeater.NewService(http.DefaultTransport, 1024),
		Events: events.NewHub(), APIAddr: "127.0.0.1:9080",
	})
	sendBody := `{"method":"GET","url":"` + target.URL + `","headers":{},"body":""}`
	sendRequest := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9080/api/repeater/sessions/persisted/send", strings.NewReader(sendBody))
	sendRequest.Header.Set("Content-Type", "application/json")
	sendRecorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(sendRecorder, sendRequest)
	if sendRecorder.Code != http.StatusOK {
		t.Fatalf("send status = %d, body = %q", sendRecorder.Code, sendRecorder.Body.String())
	}

	historyRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9080/api/repeater/sessions/persisted/history", nil)
	historyRecorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(historyRecorder, historyRequest)
	if historyRecorder.Code != http.StatusOK {
		t.Fatalf("history status = %d", historyRecorder.Code)
	}
	var sends []store.RepeaterSend
	if err := json.NewDecoder(historyRecorder.Body).Decode(&sends); err != nil {
		t.Fatal(err)
	}
	if len(sends) != 1 || sends[0].Status != http.StatusAccepted || sends[0].ResponseBody != "persisted response" {
		t.Fatalf("sends = %+v", sends)
	}
}

func TestAPIScopeRulesStartEmpty(t *testing.T) {
	srv, _ := newTargetAPIServer(t)
	recorder := serveAPIRequest(srv, http.MethodGet, "/api/scope/rules", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", recorder.Code, recorder.Body.String())
	}
	if strings.TrimSpace(recorder.Body.String()) != `{"version":0,"rules":[]}` {
		t.Fatalf("body = %q", recorder.Body.String())
	}
}

func TestAPIReplacesScopeRulesAndReturnsAssignedIDs(t *testing.T) {
	srv, service := newTargetAPIServer(t)
	body := `{"version":0,"rules":[{"id":0,"enabled":true,"action":"include","scheme":"https","hostPattern":"example.test","port":0,"pathPrefix":"/api"}]}`
	recorder := serveAPIRequest(srv, http.MethodPut, "/api/scope/rules", body, "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", recorder.Code, recorder.Body.String())
	}
	var state scope.State
	if err := json.NewDecoder(recorder.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if state.Version != 1 || len(state.Rules) != 1 || state.Rules[0].ID == 0 {
		t.Fatalf("state = %#v", state)
	}
	waitForServiceRebuild(t, service)
}

func TestAPIScopeUpdateRejectsStaleAndInvalidRules(t *testing.T) {
	srv, service := newTargetAPIServer(t)
	valid := `{"version":0,"rules":[{"id":0,"enabled":true,"action":"include","scheme":"https","hostPattern":"example.test","port":0,"pathPrefix":"/"}]}`
	if recorder := serveAPIRequest(srv, http.MethodPut, "/api/scope/rules", valid, "application/json"); recorder.Code != http.StatusOK {
		t.Fatalf("initial status = %d, body = %q", recorder.Code, recorder.Body.String())
	}
	waitForServiceRebuild(t, service)

	stale := serveAPIRequest(srv, http.MethodPut, "/api/scope/rules", valid, "application/json")
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale status = %d, body = %q", stale.Code, stale.Body.String())
	}
	invalid := `{"version":1,"rules":[{"id":0,"enabled":true,"action":"include","scheme":"ftp","hostPattern":"example.test","port":0,"pathPrefix":"/"}]}`
	badRule := serveAPIRequest(srv, http.MethodPut, "/api/scope/rules", invalid, "application/json")
	if badRule.Code != http.StatusBadRequest {
		t.Fatalf("invalid status = %d, body = %q", badRule.Code, badRule.Body.String())
	}
}

func TestAPIScopeUpdateRetainsJSONWriteProtections(t *testing.T) {
	tests := []struct {
		name, body, contentType, origin string
		maxBytes                        int64
		want                            int
	}{
		{name: "cross origin", body: `{}`, contentType: "application/json", origin: "https://attacker.example", want: http.StatusForbidden},
		{name: "wrong content type", body: `{}`, contentType: "text/plain", want: http.StatusUnsupportedMediaType},
		{name: "oversized", body: `{"version":0,"rules":[]}`, contentType: "application/json", maxBytes: 8, want: http.StatusRequestEntityTooLarge},
		{name: "unknown field", body: `{"version":0,"rules":[],"extra":true}`, contentType: "application/json", want: http.StatusBadRequest},
		{name: "trailing data", body: `{"version":0,"rules":[]} {}`, contentType: "application/json", want: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			srv, _ := newTargetAPIServer(t)
			if test.maxBytes > 0 {
				srv.cfg.MaxBodyBytes = test.maxBytes
			}
			recorder := serveAPIRequestWithOrigin(srv, http.MethodPut, "/api/scope/rules", test.body, test.contentType, test.origin)
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d, body = %q", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}
}

func TestAPITargetRoutesRequireService(t *testing.T) {
	srv := NewServer(Config{APIAddr: "127.0.0.1:9080"})
	for _, path := range []string{"/api/scope/rules", "/api/target/tree", "/api/target/rebuild"} {
		recorder := serveAPIRequest(srv, http.MethodGet, path, "", "")
		if recorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("path %s status = %d, body = %q", path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestAPITargetTreeIsOrderedAndUsesExplicitDTOs(t *testing.T) {
	srv, service := newTargetAPIServer(t)
	historyStore := srv.cfg.Store
	for _, exchange := range []*store.Exchange{
		{
			Method: "POST", Scheme: "https", Host: "z.example.test", Path: "/api/users", Status: 201,
			MIMEType: "application/json", StartedAt: time.Unix(1700000002, 0).UTC(),
			Request:  store.RequestData{Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"username":"secret"}`)},
			Response: store.ResponseData{Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"ok":true}`)},
		},
		{
			Method: "GET", Scheme: "http", Host: "a.example.test", Path: "/", Status: 200,
			MIMEType: "text/plain", StartedAt: time.Unix(1700000001, 0).UTC(),
			Response: store.ResponseData{Headers: http.Header{"Content-Type": {"text/plain"}}, Body: []byte("ok")},
		},
	} {
		if err := historyStore.SaveExchange(context.Background(), exchange); err != nil {
			t.Fatal(err)
		}
	}
	includeAll := `{"version":0,"rules":[{"id":0,"enabled":true,"action":"include","scheme":"","hostPattern":"*.example.test","port":0,"pathPrefix":"/"}]}`
	if recorder := serveAPIRequest(srv, http.MethodPut, "/api/scope/rules", includeAll, "application/json"); recorder.Code != http.StatusOK {
		t.Fatalf("scope status = %d, body = %q", recorder.Code, recorder.Body.String())
	}
	waitForServiceRebuild(t, service)

	recorder := serveAPIRequest(srv, http.MethodGet, "/api/target/tree", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", recorder.Code, recorder.Body.String())
	}
	var tree []struct {
		ID            int64             `json:"id"`
		Scheme        string            `json:"scheme"`
		Host          string            `json:"host"`
		InScope       bool              `json:"inScope"`
		RequestMIMEs  []string          `json:"requestMimes"`
		ResponseMIMEs []string          `json:"responseMimes"`
		Children      []json.RawMessage `json:"children"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&tree); err != nil {
		t.Fatal(err)
	}
	if len(tree) != 2 || tree[0].Scheme != "http" || tree[0].Host != "a.example.test" || tree[1].Host != "z.example.test" {
		t.Fatalf("tree = %+v", tree)
	}
	if !tree[0].InScope || tree[0].Children == nil || tree[0].RequestMIMEs == nil || tree[0].ResponseMIMEs == nil {
		t.Fatalf("tree DTO = %+v", tree[0])
	}
	if bytes.Contains(recorder.Body.Bytes(), []byte(`"RequestMIMEs"`)) {
		t.Fatalf("storage field leaked: %s", recorder.Body.String())
	}
}

func TestAPITargetTreeWithoutActiveGenerationIsEmpty(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "project.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	service := target.NewService(repository, scope.NewManager(nil), events.NewHub(), target.Limits{})
	t.Cleanup(service.Close)
	srv := NewServer(Config{Store: repository, Target: service, APIAddr: "127.0.0.1:9080"})
	recorder := serveAPIRequest(srv, http.MethodGet, "/api/target/tree", "", "")
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != "[]" {
		t.Fatalf("status = %d, body = %q", recorder.Code, recorder.Body.String())
	}
}

func TestAPITargetEndpointRequestsAndParametersAreProjected(t *testing.T) {
	srv, service := newTargetAPIServer(t)
	exchange := &store.Exchange{
		Method: "POST", Scheme: "https", Host: "example.test", Path: "/api/users", Query: "page=7", Status: 201,
		MIMEType: "application/json", StartedAt: time.Unix(1700000000, 0).UTC(),
		Request:  store.RequestData{Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"username":"secret"}`)},
		Response: store.ResponseData{Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"token":"never expose"}`)},
	}
	if err := srv.cfg.Store.SaveExchange(context.Background(), exchange); err != nil {
		t.Fatal(err)
	}
	body := `{"version":0,"rules":[{"id":0,"enabled":true,"action":"include","scheme":"https","hostPattern":"example.test","port":0,"pathPrefix":"/api"}]}`
	if recorder := serveAPIRequest(srv, http.MethodPut, "/api/scope/rules", body, "application/json"); recorder.Code != http.StatusOK {
		t.Fatalf("scope status = %d, body = %q", recorder.Code, recorder.Body.String())
	}
	waitForServiceRebuild(t, service)

	treeRecorder := serveAPIRequest(srv, http.MethodGet, "/api/target/tree", "", "")
	endpointID := endpointIDFromTree(t, treeRecorder.Body.Bytes())
	endpoint := serveAPIRequest(srv, http.MethodGet, "/api/target/endpoints/"+strconv.FormatInt(endpointID, 10), "", "")
	if endpoint.Code != http.StatusOK || !bytes.Contains(endpoint.Body.Bytes(), []byte(`"latestExchangeId":1`)) {
		t.Fatalf("endpoint status = %d, body = %q", endpoint.Code, endpoint.Body.String())
	}
	requests := serveAPIRequest(srv, http.MethodGet, "/api/target/endpoints/"+strconv.FormatInt(endpointID, 10)+"/requests", "", "")
	if requests.Code != http.StatusOK || !bytes.Contains(requests.Body.Bytes(), []byte(`"exchangeId":1`)) {
		t.Fatalf("requests status = %d, body = %q", requests.Code, requests.Body.String())
	}
	parameters := serveAPIRequest(srv, http.MethodGet, "/api/target/endpoints/"+strconv.FormatInt(endpointID, 10)+"/parameters", "", "")
	if parameters.Code != http.StatusOK || !bytes.Contains(parameters.Body.Bytes(), []byte(`"name":"username"`)) {
		t.Fatalf("parameters status = %d, body = %q", parameters.Code, parameters.Body.String())
	}
	if bytes.Contains(parameters.Body.Bytes(), []byte("secret")) || bytes.Contains(parameters.Body.Bytes(), []byte(`"value"`)) {
		t.Fatalf("parameter values leaked: %s", parameters.Body.String())
	}
}

func TestAPITargetEndpointRoutesRejectInvalidAndUnknownIDs(t *testing.T) {
	srv, _ := newTargetAPIServer(t)
	for _, test := range []struct {
		path string
		want int
	}{
		{path: "/api/target/endpoints/not-a-number", want: http.StatusBadRequest},
		{path: "/api/target/endpoints/999", want: http.StatusNotFound},
		{path: "/api/target/endpoints/999/requests", want: http.StatusNotFound},
		{path: "/api/target/endpoints/999/parameters", want: http.StatusNotFound},
	} {
		recorder := serveAPIRequest(srv, http.MethodGet, test.path, "", "")
		if recorder.Code != test.want {
			t.Fatalf("path %s status = %d, want %d, body = %q", test.path, recorder.Code, test.want, recorder.Body.String())
		}
	}
}

func TestAPIRebuildStatusAndRetryContract(t *testing.T) {
	srv, service := newTargetAPIServer(t)
	for index := 0; index < 250; index++ {
		exchange := &store.Exchange{
			Method: "GET", Scheme: "https", Host: "example.test", Path: "/items", Status: http.StatusOK,
			StartedAt: time.Unix(1700000000+int64(index), 0).UTC(),
			Response:  store.ResponseData{Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"ok":true}`)},
		}
		if err := srv.cfg.Store.SaveExchange(context.Background(), exchange); err != nil {
			t.Fatal(err)
		}
	}
	includeAll := `{"version":0,"rules":[{"id":0,"enabled":true,"action":"include","scheme":"https","hostPattern":"example.test","port":0,"pathPrefix":"/"}]}`
	if recorder := serveAPIRequest(srv, http.MethodPut, "/api/scope/rules", includeAll, "application/json"); recorder.Code != http.StatusOK {
		t.Fatalf("scope status = %d, body = %q", recorder.Code, recorder.Body.String())
	}
	waitForServiceRebuild(t, service)
	status := serveAPIRequest(srv, http.MethodGet, "/api/target/rebuild", "", "")
	if status.Code != http.StatusOK || !bytes.Contains(status.Body.Bytes(), []byte(`"status":"active"`)) {
		t.Fatalf("status = %d, body = %q", status.Code, status.Body.String())
	}
	retry := serveAPIRequest(srv, http.MethodPost, "/api/target/rebuild", `{}`, "application/json")
	if retry.Code != http.StatusAccepted || !bytes.Contains(retry.Body.Bytes(), []byte(`"status":"building"`)) {
		t.Fatalf("retry status = %d, body = %q", retry.Code, retry.Body.String())
	}
	conflict := serveAPIRequest(srv, http.MethodPost, "/api/target/rebuild", `{}`, "application/json")
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d, body = %q", conflict.Code, conflict.Body.String())
	}
	waitForServiceRebuild(t, service)
}

func TestAPIConcurrentRebuildRetriesAcceptOneWithoutCancellingIt(t *testing.T) {
	srv, service, repository := newConcurrentRetryAPIServer(t)
	repository.beginRace()
	responses := make(chan *httptest.ResponseRecorder, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			responses <- serveAPIRequest(srv, http.MethodPost, "/api/target/rebuild", `{}`, "application/json")
		}()
	}
	close(start)

	collected := make([]*httptest.ResponseRecorder, 0, 2)
	select {
	case <-repository.statusChecksReady:
		repository.releaseStatusChecks()
	case response := <-responses:
		collected = append(collected, response)
		repository.releaseStatusChecks()
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent retries did not reach the status/retry boundary")
	}
	for len(collected) < 2 {
		select {
		case response := <-responses:
			collected = append(collected, response)
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent retry response timed out")
		}
	}
	cancellations := repository.cancelCount()
	repository.releasePages()
	waitForServiceRebuild(t, service)

	accepted := 0
	conflicts := 0
	for _, response := range collected {
		switch response.Code {
		case http.StatusAccepted:
			accepted++
		case http.StatusConflict:
			conflicts++
		}
	}
	if accepted != 1 || conflicts != 1 || cancellations != 0 {
		t.Fatalf("statuses = [%d, %d], accepted = %d, conflicts = %d, cancellations = %d", collected[0].Code, collected[1].Code, accepted, conflicts, cancellations)
	}
}

func TestAPIRebuildRetryAcceptsOnlyEmptyJSONObject(t *testing.T) {
	for _, body := range []string{"", `null`, `[]`, `{"extra":true}`, `{} {}`} {
		t.Run(body, func(t *testing.T) {
			srv, service := newTargetAPIServer(t)
			waitForServiceRebuild(t, service)
			recorder := serveAPIRequest(srv, http.MethodPost, "/api/target/rebuild", body, "application/json")
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("body %q status = %d, response = %q", body, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestAPIHistoryResponsesExposeScopeClassification(t *testing.T) {
	memory := store.NewMemoryForTests()
	ruleID := int64(42)
	for _, exchange := range []*store.Exchange{
		{Method: "GET", Scheme: "https", Host: "in.test", Path: "/", StartedAt: time.Unix(2, 0).UTC(), InScope: true, ScopeVersion: 3, ScopeRuleID: &ruleID},
		{Method: "GET", Scheme: "https", Host: "out.test", Path: "/", StartedAt: time.Unix(1, 0).UTC(), InScope: false, ScopeVersion: 3},
	} {
		if err := memory.SaveExchange(context.Background(), exchange); err != nil {
			t.Fatal(err)
		}
	}
	srv := NewServer(Config{Store: memory, APIAddr: "127.0.0.1:9080"})
	list := serveAPIRequest(srv, http.MethodGet, "/api/history", "", "")
	if list.Code != http.StatusOK || !bytes.Contains(list.Body.Bytes(), []byte(`"scopeRuleId":null`)) || !bytes.Contains(list.Body.Bytes(), []byte(`"scopeRuleId":42`)) {
		t.Fatalf("list status = %d, body = %q", list.Code, list.Body.String())
	}
	detail := serveAPIRequest(srv, http.MethodGet, "/api/history/2", "", "")
	if detail.Code != http.StatusOK || !bytes.Contains(detail.Body.Bytes(), []byte(`"inScope":false`)) || !bytes.Contains(detail.Body.Bytes(), []byte(`"scopeVersion":3`)) || !bytes.Contains(detail.Body.Bytes(), []byte(`"scopeRuleId":null`)) {
		t.Fatalf("detail status = %d, body = %q", detail.Code, detail.Body.String())
	}
}

func newTargetAPIServer(t *testing.T) (*Server, *target.Service) {
	t.Helper()
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "project.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	state, err := repository.LoadScopeState(context.Background())
	if err != nil {
		repository.Close()
		t.Fatal(err)
	}
	rules, err := scope.Compile(state.Version, state.Rules)
	if err != nil {
		repository.Close()
		t.Fatal(err)
	}
	service := target.NewService(repository, scope.NewManager(rules), events.NewHub(), target.Limits{
		MaxJSONDepth: 16, MaxFields: 1000, MaxMultipartFields: 100,
	})
	if err := service.Recover(context.Background()); err != nil {
		repository.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		service.Close()
		_ = repository.Close()
	})
	srv := NewServer(Config{Store: repository, Target: service, APIAddr: "127.0.0.1:9080"})
	waitForServiceRebuild(t, service)
	return srv, service
}

func newConcurrentRetryAPIServer(t *testing.T) (*Server, *target.Service, *retryRaceRepository) {
	t.Helper()
	sqlite, err := store.OpenSQLite(filepath.Join(t.TempDir(), "project.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	state, err := sqlite.LoadScopeState(context.Background())
	if err != nil {
		sqlite.Close()
		t.Fatal(err)
	}
	rules, err := scope.Compile(state.Version, state.Rules)
	if err != nil {
		sqlite.Close()
		t.Fatal(err)
	}
	repository := newRetryRaceRepository(sqlite)
	service := target.NewService(repository, scope.NewManager(rules), events.NewHub(), target.Limits{
		MaxJSONDepth: 16, MaxFields: 1000, MaxMultipartFields: 100,
	})
	if err := service.Recover(context.Background()); err != nil {
		sqlite.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		repository.releaseStatusChecks()
		repository.releasePages()
		service.Close()
		_ = sqlite.Close()
	})
	waitForServiceRebuild(t, service)
	exchange := &store.Exchange{
		Method: "GET", Scheme: "https", Host: "example.test", Path: "/api", Status: http.StatusOK,
		StartedAt: time.Unix(1700000000, 0).UTC(),
		Response:  store.ResponseData{Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"ok":true}`)},
	}
	if err := sqlite.SaveExchange(context.Background(), exchange); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReplaceRules(context.Background(), 0, []scope.Rule{{
		Enabled: true, Action: scope.ActionInclude, Scheme: "https", HostPattern: "example.test", PathPrefix: "/",
	}}); err != nil {
		t.Fatal(err)
	}
	waitForServiceRebuild(t, service)
	return NewServer(Config{Store: sqlite, Target: service, APIAddr: "127.0.0.1:9080"}), service, repository
}

func waitForServiceRebuild(t *testing.T, service *target.Service) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var last store.RebuildStatus
	for time.Now().Before(deadline) {
		status, err := service.RebuildStatus(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		last = status
		if status.Status == "active" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("target rebuild did not become active: %+v", last)
}

func serveAPIRequest(srv *Server, method, path, body, contentType string) *httptest.ResponseRecorder {
	return serveAPIRequestWithOrigin(srv, method, path, body, contentType, "")
}

func serveAPIRequestWithOrigin(srv *Server, method, path, body, contentType, origin string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://127.0.0.1:9080"+path, strings.NewReader(body))
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, request)
	return recorder
}

func endpointIDFromTree(t *testing.T, payload []byte) int64 {
	t.Helper()
	var tree []struct {
		ID       int64             `json:"id"`
		Children []json.RawMessage `json:"children"`
	}
	if err := json.Unmarshal(payload, &tree); err != nil {
		t.Fatal(err)
	}
	var find func([]json.RawMessage) int64
	find = func(nodes []json.RawMessage) int64 {
		for _, raw := range nodes {
			var node struct {
				ID       int64             `json:"id"`
				Children []json.RawMessage `json:"children"`
			}
			if err := json.Unmarshal(raw, &node); err != nil {
				t.Fatal(err)
			}
			if node.ID != 0 {
				return node.ID
			}
			if id := find(node.Children); id != 0 {
				return id
			}
		}
		return 0
	}
	rootNodes := make([]json.RawMessage, len(tree))
	for index := range tree {
		rootNodes[index], _ = json.Marshal(tree[index])
	}
	if id := find(rootNodes); id != 0 {
		return id
	}
	t.Fatalf("endpoint id not found in tree: %s", payload)
	return 0
}

type retryRaceRepository struct {
	target.Repository

	mu                    sync.Mutex
	blockStatusChecks     bool
	statusCheckCount      int
	statusChecksReady     chan struct{}
	releaseStatus         chan struct{}
	releaseStatusOnce     sync.Once
	blockPageReads        bool
	pageStarted           chan struct{}
	releasePage           chan struct{}
	pageStartedOnce       sync.Once
	releasePageOnce       sync.Once
	targetGenerationStops int
}

func newRetryRaceRepository(repository target.Repository) *retryRaceRepository {
	return &retryRaceRepository{Repository: repository}
}

func (r *retryRaceRepository) beginRace() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.blockStatusChecks = true
	r.statusCheckCount = 0
	r.statusChecksReady = make(chan struct{})
	r.releaseStatus = make(chan struct{})
	r.releaseStatusOnce = sync.Once{}
	r.blockPageReads = true
	r.pageStarted = make(chan struct{})
	r.releasePage = make(chan struct{})
	r.pageStartedOnce = sync.Once{}
	r.releasePageOnce = sync.Once{}
}

func (r *retryRaceRepository) LatestTargetGeneration(ctx context.Context) (store.TargetGeneration, error) {
	r.mu.Lock()
	if !r.blockStatusChecks {
		r.mu.Unlock()
		return r.Repository.LatestTargetGeneration(ctx)
	}
	r.statusCheckCount++
	if r.statusCheckCount == 2 {
		close(r.statusChecksReady)
	}
	release := r.releaseStatus
	r.mu.Unlock()
	select {
	case <-release:
	case <-ctx.Done():
		return store.TargetGeneration{}, ctx.Err()
	}
	return r.Repository.LatestTargetGeneration(ctx)
}

func (r *retryRaceRepository) ListExchangesPage(ctx context.Context, afterID, throughID int64, limit int) ([]store.Exchange, error) {
	r.mu.Lock()
	if !r.blockPageReads {
		r.mu.Unlock()
		return r.Repository.ListExchangesPage(ctx, afterID, throughID, limit)
	}
	r.pageStartedOnce.Do(func() { close(r.pageStarted) })
	release := r.releasePage
	r.mu.Unlock()
	select {
	case <-release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return r.Repository.ListExchangesPage(ctx, afterID, throughID, limit)
}

func (r *retryRaceRepository) CancelTargetGeneration(ctx context.Context, generationID int64) error {
	err := r.Repository.CancelTargetGeneration(ctx, generationID)
	if err == nil {
		r.mu.Lock()
		r.targetGenerationStops++
		r.mu.Unlock()
	}
	return err
}

func (r *retryRaceRepository) releaseStatusChecks() {
	r.mu.Lock()
	if r.releaseStatus == nil {
		r.mu.Unlock()
		return
	}
	r.blockStatusChecks = false
	release := r.releaseStatus
	r.mu.Unlock()
	r.releaseStatusOnce.Do(func() { close(release) })
}

func (r *retryRaceRepository) releasePages() {
	r.mu.Lock()
	if r.releasePage == nil {
		r.mu.Unlock()
		return
	}
	r.blockPageReads = false
	release := r.releasePage
	r.mu.Unlock()
	r.releasePageOnce.Do(func() { close(release) })
}

func (r *retryRaceRepository) cancelCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.targetGenerationStops
}

func waitForAPIQueue(t *testing.T, queue *intercept.Queue) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(queue.List()) == 1 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("intercept item not queued")
}
