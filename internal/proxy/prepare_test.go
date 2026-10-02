package proxy

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/intercept"
	"github.com/lutzifer/burpsuite-clone/internal/scope"
)

func testRule(id, direction, target, pattern, replacement string) intercept.ReplacementRule {
	return intercept.ReplacementRule{ID: id, Enabled: true, Direction: direction, Target: target, Pattern: pattern, Replacement: replacement}
}

func TestOrderedRulesEnableFilteredBodyWithoutManualInterception(t *testing.T) {
	c := intercept.NewController(nil, false, nil)
	urlRule := testRule("url", "request", "url", "/before", "/after")
	headerRule := testRule("header", "request", "header", "text/plain", "application/json")
	headerRule.Header = "Content-Type"
	bodyRule := testRule("body", "request", "body", "before", "after")
	bodyRule.PathContains = "/after"
	bodyRule.MIMEContains = "json"
	c.Update(intercept.ControllerState{ReplacementRules: []intercept.ReplacementRule{urlRule, headerRule, bodyRule}})
	s := NewServer(Config{Intercept: c, BodyLimitBytes: 100, Scope: scopeManagerForURL(t, "http://example.test", 1, 1)})
	r := httptest.NewRequest("POST", "http://example.test/before", strings.NewReader("before"))
	r.Header.Set("Content-Type", "text/plain")
	rules := s.currentScopeRules()
	p, err := s.prepareRequest(r, classifyWithRules(rules, r), rules)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(p.forward.Body)
	if err != nil {
		t.Fatal(err)
	}
	_ = p.forward.Body.Close()
	if string(data) != "after" || p.forward.URL.Path != "/after" || !reflect.DeepEqual(p.appliedRuleIDs, []string{"url", "header", "body"}) {
		t.Fatalf("%q %#v", data, p)
	}
	if len(c.Queue().List()) != 0 || p.intercepted {
		t.Fatal("manual switch ignored")
	}
}

func TestHeaderReplacementsApplyToIndividualValues(t *testing.T) {
	rule := testRule("header", "response", "header", "^a$", "b")
	rule.Regex = true
	rule.Header = "X-Test"
	r := httptest.NewRequest("GET", "http://example.test/", nil)
	h := http.Header{"X-Test": {"a", "a", "cab"}}
	ids, _, err := applyReplacements(intercept.ControllerState{ReplacementRules: []intercept.ReplacementRule{rule}}, "response", r, h, &editableBody{}, 100, 200)
	if err != nil || len(ids) != 1 || !reflect.DeepEqual(h.Values("X-Test"), []string{"b", "b", "cab"}) {
		t.Fatalf("%v %#v %v", ids, h, err)
	}
}

func TestResponseBypassPreservesBytesAndHeaders(t *testing.T) {
	for _, tc := range []struct {
		name, mime, encoding string
		data                 []byte
		length               int64
	}{
		{"encoded", "text/plain", "gzip", []byte{31, 139, 0, 255}, 4},
		{"binary", "application/octet-stream", "", []byte{0, 1, 255, 9}, 4},
		{"oversized", "text/plain", "", []byte("abcdefghijklmnop"), 16},
		{"unknown", "text/plain", "", []byte("stream"), -1},
		{"events", "text/event-stream", "", []byte("data: a\n\n"), 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := intercept.NewController(intercept.NewQueue(time.Second), false, nil)
			state := c.State()
			state.ResponseEnabled = true
			state.ReplacementRules = []intercept.ReplacementRule{testRule("body", "response", "body", "a", "edited")}
			c.Update(state)
			s := NewServer(Config{Intercept: c, BodyLimitBytes: 10})
			r := httptest.NewRequest("GET", "http://example.test", nil)
			p := &preparedRequest{forward: r, decision: scope.Decision{InScope: true}}
			headers := http.Header{"Content-Type": {tc.mime}, "X-Preserved": {"yes"}}
			if tc.encoding != "" {
				headers.Set("Content-Encoding", tc.encoding)
			}
			upstream := &http.Response{StatusCode: 200, Header: headers, Body: io.NopCloser(bytes.NewReader(tc.data)), ContentLength: tc.length}
			done := make(chan *http.Response, 1)
			go func() { done <- s.prepareResponse(p, upstream) }()
			item := waitForIntercept(t, c.ResponseQueue())
			if item.BodyEditable || item.Headers["X-Preserved"][0] != "yes" {
				t.Fatalf("%#v", item)
			}
			if err := c.ResponseQueue().Forward(item.ID, intercept.RequestEdit{StatusCode: 202, Headers: item.Headers}); err != nil {
				t.Fatal(err)
			}
			response := <-done
			data, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil || !bytes.Equal(data, tc.data) || response.Header.Get("Content-Encoding") != tc.encoding || response.StatusCode != 202 {
				t.Fatalf("data %v status %d err %v", data, response.StatusCode, err)
			}
			if len(p.appliedRuleIDs) != 0 || !p.responseIntercepted {
				t.Fatal("incorrect audit")
			}
		})
	}
}

func TestHeadAndBodylessResponseForward(t *testing.T) {
	for _, status := range []int{200, 204, 205, 304} {
		for _, method := range []string{"HEAD", "GET"} {
			if status == 200 && method == "GET" {
				continue
			}
			t.Run(method+http.StatusText(status), func(t *testing.T) {
				c := intercept.NewController(intercept.NewQueue(time.Second), false, nil)
				state := c.State()
				state.ResponseEnabled = true
				c.Update(state)
				s := NewServer(Config{Intercept: c, BodyLimitBytes: 100})
				r := httptest.NewRequest(method, "http://example.test/", nil)
				p := &preparedRequest{forward: r, decision: scope.Decision{InScope: true}}
				upstream := &http.Response{StatusCode: status, Header: http.Header{"Content-Length": {"12"}}, Body: http.NoBody, ContentLength: 12}
				done := make(chan *http.Response, 1)
				go func() { done <- s.prepareResponse(p, upstream) }()
				item := waitForIntercept(t, c.ResponseQueue())
				if item.BodyEditable {
					t.Fatal("bodyless editable")
				}
				if err := c.ResponseQueue().Forward(item.ID, intercept.RequestEdit{StatusCode: status}); err != nil {
					t.Fatal(err)
				}
				result := <-done
				data, _ := io.ReadAll(result.Body)
				if len(data) != 0 || result.StatusCode != status {
					t.Fatalf("%#v %q", result, data)
				}
				if method == "HEAD" && result.Header.Get("Content-Length") != "12" {
					t.Fatal("HEAD metadata lost")
				}
				if method == "GET" && status != 304 && result.Header.Get("Content-Length") != "" {
					t.Fatal("bodyless length retained")
				}
			})
		}
	}
}

func TestResponseEditsNormalizeFraming(t *testing.T) {
	c := intercept.NewController(intercept.NewQueue(time.Second), false, nil)
	state := c.State()
	state.ResponseEnabled = true
	c.Update(state)
	s := NewServer(Config{Intercept: c, BodyLimitBytes: 100})
	r := httptest.NewRequest("GET", "http://example.test", nil)
	p := &preparedRequest{forward: r, decision: scope.Decision{InScope: true}}
	upstream := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/plain"}, "Content-Length": {"6"}}, Body: io.NopCloser(strings.NewReader("before")), ContentLength: 6, TransferEncoding: []string{"chunked"}}
	done := make(chan *http.Response, 1)
	go func() { done <- s.prepareResponse(p, upstream) }()
	item := waitForIntercept(t, c.ResponseQueue())
	if err := c.ResponseQueue().Forward(item.ID, intercept.RequestEdit{StatusCode: 202, Headers: item.Headers, Body: []byte("after"), BodySet: true}); err != nil {
		t.Fatal(err)
	}
	result := <-done
	data, _ := io.ReadAll(result.Body)
	_ = result.Body.Close()
	if string(data) != "after" || result.ContentLength != 5 || result.Header.Get("Content-Length") != "5" || len(result.TransferEncoding) != 0 {
		t.Fatalf("%#v %q", result, data)
	}
}

func TestRequestBinaryHeaderOnlyAndCancellation(t *testing.T) {
	c := intercept.NewController(intercept.NewQueue(time.Second), true, []intercept.Rule{{Enabled: true}})
	s := NewServer(Config{Intercept: c, BodyLimitBytes: 100, Scope: scopeManagerForURL(t, "http://example.test", 1, 1)})
	r := httptest.NewRequest("POST", "http://example.test/", bytes.NewReader([]byte{0, 255, 12}))
	r.Header.Set("Content-Type", "application/octet-stream")
	done := make(chan *preparedRequest, 1)
	go func() {
		p, err := s.prepareRequest(r, classifyWithRules(s.currentScopeRules(), r), s.currentScopeRules())
		if err != nil {
			t.Error(err)
		}
		done <- p
	}()
	item := waitForIntercept(t, c.Queue())
	if item.BodyEditable {
		t.Fatal("binary editable")
	}
	if err := c.Queue().Forward(item.ID, intercept.RequestEdit{Headers: item.Headers}); err != nil {
		t.Fatal(err)
	}
	p := <-done
	if p == nil {
		t.Fatal("prepare failed")
	}
	data, _ := io.ReadAll(p.forward.Body)
	_ = p.forward.Body.Close()
	if !bytes.Equal(data, []byte{0, 255, 12}) {
		t.Fatalf("%v", data)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.ResponseQueue().Enqueue(ctx, intercept.Item{ID: "canceled"}); err == nil {
		t.Fatal("canceled context accepted")
	}
}

func TestStreamingInspectionDoesNotRead(t *testing.T) {
	for _, tc := range []struct {
		mime   string
		length int64
	}{{"text/event-stream", 0}, {"text/plain", -1}, {"application/octet-stream", 3}, {"text/plain", 101}} {
		var body io.ReadCloser = errorReadCloser{err: io.ErrUnexpectedEOF}
		result, err := inspectBody(&body, http.Header{"Content-Type": {tc.mime}}, tc.length, 100, true)
		if err != nil || result.editable {
			t.Fatalf("inspection read bypassed stream: %v", err)
		}
	}
}
