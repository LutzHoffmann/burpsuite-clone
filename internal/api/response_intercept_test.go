package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/events"
	"github.com/lutzifer/burpsuite-clone/internal/intercept"
	"github.com/lutzifer/burpsuite-clone/internal/store"
)

func interceptAPICall(s *Server, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestResponseQueueEndpointsAndEvents(t *testing.T) {
	c := intercept.NewController(intercept.NewQueue(time.Minute), false, nil)
	hub := events.NewHub()
	sub, unsub := hub.Subscribe()
	defer unsub()
	s := NewServer(Config{Intercept: c, Events: hub})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan intercept.Decision, 1)
	go func() {
		d, _ := c.ResponseQueue().Enqueue(ctx, intercept.Item{ID: "response-1", Phase: "response", StatusCode: 200, Method: "GET", BodyEditable: true, Body: []byte("before"), Headers: http.Header{"Content-Type": {"text/plain"}}})
		result <- d
	}()
	waitForAPIQueue(t, c.ResponseQueue())
	select {
	case event := <-sub:
		if event.Type != "intercept.response.queued" {
			t.Fatal(event)
		}
	case <-time.After(time.Second):
		t.Fatal("missing queued event")
	}
	w := interceptAPICall(s, "GET", "/api/intercept/response-queue", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"phase":"response"`) || !strings.Contains(w.Body.String(), `"statusCode":200`) {
		t.Fatalf("queue = %d %s", w.Code, w.Body)
	}
	w = interceptAPICall(s, "GET", "/api/intercept/queue", "")
	if strings.Contains(w.Body.String(), "response-1") {
		t.Fatal("response leaked into request queue")
	}
	for _, action := range []string{"forward", "drop"} {
		w = interceptAPICall(s, "POST", "/api/intercept/response-1/"+action, `{}`)
		if w.Code != 404 {
			t.Fatalf("request %s response id: %d", action, w.Code)
		}
	}
	for _, body := range []string{
		`{"statusCode":99}`, `{"statusCode":101}`, `{"statusCode":600}`, `{"statusCode":204,"body":"invalid"}`,
		`{"statusCode":200,"headers":{"Bad Header":["x"]}}`, `{"statusCode":200,"headers":{"X-Test":["x\r\nInjected: yes"]}}`,
		`{"statusCode":200,"headers":{"Content-Length":["100"]}}`, `{"statusCode":200,"headers":{"Transfer-Encoding":["chunked"]}}`,
		`{"statusCode":200,"headers":{"Content-Encoding":["gzip"]}}`, `{"statusCode":200,"headers":{"X-Test":["x"],"x-test":["y"]}}`,
		`{"statusCode":200,"url":"http://example.test"}`, `{"statusCode":200} {}`, `null`,
	} {
		w = interceptAPICall(s, "POST", "/api/intercept/response/response-1/forward", body)
		if w.Code != 400 {
			t.Fatalf("invalid edit %s: %d %s", body, w.Code, w.Body)
		}
		if _, ok := c.ResponseQueue().Get("response-1"); !ok {
			t.Fatalf("invalid edit consumed response: %s", body)
		}
	}
	w = interceptAPICall(s, "POST", "/api/intercept/response/response-1/forward", `{"statusCode":201,"headers":{"Content-Type":["text/plain"]},"body":"after"}`)
	if w.Code != 204 {
		t.Fatalf("forward: %d %s", w.Code, w.Body)
	}
	select {
	case d := <-result:
		if d.Action != intercept.ActionForward || d.Edit.StatusCode != 201 || string(d.Edit.Body) != "after" || !d.Edit.BodySet {
			t.Fatalf("decision = %+v", d)
		}
	case <-time.After(time.Second):
		t.Fatal("not forwarded")
	}
	select {
	case event := <-sub:
		if event.Type != "intercept.response.completed" {
			t.Fatal(event)
		}
	case <-time.After(time.Second):
		t.Fatal("missing completed event")
	}
	go func() {
		d, _ := c.ResponseQueue().Enqueue(ctx, intercept.Item{ID: "drop-1", Phase: "response", StatusCode: 200})
		result <- d
	}()
	waitForAPIQueue(t, c.ResponseQueue())
	w = interceptAPICall(s, "POST", "/api/intercept/response/drop-1/drop", `{}`)
	if w.Code != 204 {
		t.Fatalf("drop: %d %s", w.Code, w.Body)
	}
	if d := <-result; d.Action != intercept.ActionDrop {
		t.Fatal(d)
	}
}

func TestResponseEditPreservesUneditableAndFraming(t *testing.T) {
	item := intercept.Item{StatusCode: 200, Method: "GET", Headers: http.Header{"Content-Encoding": {"gzip"}, "Content-Length": {"30"}}}
	for _, edit := range []responseEditDTO{
		{StatusCode: 200, Body: "replacement"}, {StatusCode: 204},
		{StatusCode: 200, Headers: map[string][]string{}},
		{StatusCode: 200, Headers: http.Header{"Content-Encoding": {"gzip"}, "Content-Length": {"2"}}},
	} {
		if err := validateResponseEdit(item, edit); err == nil {
			t.Fatalf("accepted %+v", edit)
		}
	}
	if err := validateResponseEdit(item, responseEditDTO{StatusCode: 201, Headers: item.Headers}); err != nil {
		t.Fatal(err)
	}
	if err := validateResponseEdit(item, responseEditDTO{StatusCode: 201}); err != nil {
		t.Fatal(err)
	}
	item.Method = "HEAD"
	item.BodyEditable = true
	if err := validateResponseEdit(item, responseEditDTO{StatusCode: 200, Body: "invalid"}); err == nil {
		t.Fatal("accepted HEAD body")
	}
}

func TestNoneditableForwardNeverSetsBody(t *testing.T) {
	for _, phase := range []string{"request", "response"} {
		for _, method := range []string{"GET", "HEAD"} {
			t.Run(phase+"/"+method, func(t *testing.T) {
				c := intercept.NewController(intercept.NewQueue(time.Minute), true, nil)
				q, path := c.Queue(), "/api/intercept/opaque/forward"
				body := `{"method":"` + method + `","url":"http://example.test/","body":""}`
				if phase == "response" {
					q = c.ResponseQueue()
					path = "/api/intercept/response/opaque/forward"
					body = `{"statusCode":200,"body":""}`
				}
				s := NewServer(Config{Intercept: c})
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				result := make(chan intercept.Decision, 1)
				go func() {
					d, _ := q.Enqueue(ctx, intercept.Item{ID: "opaque", Phase: phase, Method: method, StatusCode: 200, Body: []byte{0, 255, 1}, BodyTruncated: true})
					result <- d
				}()
				waitForAPIQueue(t, q)
				w := interceptAPICall(s, "POST", path, body)
				if w.Code != 204 {
					t.Fatalf("forward: %d %s", w.Code, w.Body)
				}
				select {
				case d := <-result:
					if d.Edit.Body != nil || d.Edit.BodySet {
						t.Fatalf("noneditable body set: %+v", d.Edit)
					}
				case <-time.After(time.Second):
					t.Fatal("not forwarded")
				}
			})
		}
	}
}

type controlledSettings struct {
	store.Store
	store.ProjectStore
	mu         sync.Mutex
	saved      string
	fail       bool
	beforeSave func()
}

func (s *controlledSettings) SetSetting(_ context.Context, _, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.beforeSave != nil {
		s.beforeSave()
	}
	if s.fail {
		return errors.New("disk failure")
	}
	s.saved = value
	return nil
}

func TestInterceptConfigurationValidationPersistenceAndSerialization(t *testing.T) {
	c := intercept.NewController(nil, false, []intercept.Rule{{Enabled: true}})
	initial := c.State()
	settings := &controlledSettings{Store: store.NewMemoryForTests(), fail: true}
	s := NewServer(Config{Intercept: c, Store: settings})
	valid := `{"enabled":true,"rules":[{"enabled":true}],"responseEnabled":true,"responseRules":[{"enabled":true,"statusCode":201}],"replacementRules":[{"id":"replace","enabled":true,"direction":"response","target":"body","pattern":"(a)","replacement":"$1b","regex":true}]}`
	settings.beforeSave = func() {
		if !reflect.DeepEqual(c.State(), initial) {
			t.Error("activated before persistence")
		}
	}
	w := interceptAPICall(s, "PUT", "/api/intercept/config", valid)
	if w.Code != 500 || !reflect.DeepEqual(c.State(), initial) {
		t.Fatalf("failed save changed state: %d %+v", w.Code, c.State())
	}
	settings.fail = false
	w = interceptAPICall(s, "PUT", "/api/intercept/config", `null`)
	if w.Code != 400 || !reflect.DeepEqual(c.State(), initial) {
		t.Fatal("null config changed state")
	}
	w = interceptAPICall(s, "PUT", "/api/intercept/config", strings.Replace(valid, `"pattern":"(a)"`, `"pattern":"("`, 1))
	if w.Code != 400 || settings.saved != "" || !reflect.DeepEqual(c.State(), initial) {
		t.Fatalf("invalid config changed state: %d", w.Code)
	}
	w = interceptAPICall(s, "PUT", "/api/intercept/config", valid)
	if w.Code != 200 || !c.State().ResponseEnabled || len(c.State().ReplacementRules) != 1 {
		t.Fatalf("valid update: %d %s", w.Code, w.Body)
	}
	settings.beforeSave = nil
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			state := initial
			state.ResponseEnabled = i%2 == 0
			data, _ := json.Marshal(state)
			w := interceptAPICall(s, "PUT", "/api/intercept/config", string(data))
			if w.Code != 200 {
				t.Errorf("concurrent update: %d", w.Code)
			}
		}(i)
	}
	wg.Wait()
	var persisted intercept.ControllerState
	if err := json.Unmarshal([]byte(settings.saved), &persisted); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(persisted, c.State()) {
		t.Fatalf("persisted %+v != active %+v", persisted, c.State())
	}
}

func TestExchangeDetailIncludesResponseAudit(t *testing.T) {
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "audit.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ex := &store.Exchange{AppliedRuleIDs: []string{"one", "two"}, ResponseIntercepted: true}
	if err := st.SaveExchange(context.Background(), ex); err != nil {
		t.Fatal(err)
	}
	w := interceptAPICall(NewServer(Config{Store: st}), "GET", "/api/history/1", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"appliedRuleIds":["one","two"]`) || !strings.Contains(w.Body.String(), `"responseIntercepted":true`) {
		t.Fatalf("detail: %d %s", w.Code, w.Body)
	}
}
