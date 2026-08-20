package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/certs"
	"github.com/lutzifer/burpsuite-clone/internal/events"
	"github.com/lutzifer/burpsuite-clone/internal/intercept"
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
