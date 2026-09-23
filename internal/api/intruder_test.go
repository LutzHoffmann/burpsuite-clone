package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lutzifer/burpsuite-clone/internal/intruder"
)

type intruderAPIFake struct {
	IntruderService
	createErr error
	created   intruder.Draft
}

func (f *intruderAPIFake) Create(_ context.Context, draft intruder.Draft) (intruder.Job, error) {
	f.created = draft
	if f.createErr != nil {
		return intruder.Job{}, f.createErr
	}
	return intruder.Job{ID: strings.Repeat("a", 32), State: intruder.StateDraft, Revision: 1, Config: draft.Config}, nil
}

func TestIntruderUnavailableAndRequestProtections(t *testing.T) {
	handler := NewServer(Config{APIAddr: "127.0.0.1:9080"}).Handler()
	id := strings.Repeat("a", 32)
	for _, path := range []string{"/api/intruder/jobs", "/api/intruder/jobs/" + id + "/start"} {
		method := http.MethodGet
		if strings.HasSuffix(path, "/start") {
			method = http.MethodPost
		}
		req := httptest.NewRequest(method, path, strings.NewReader(`{"revision":1}`))
		req.Host = "127.0.0.1:9080"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d headers=%v", path, rec.Code, rec.Header())
		}
	}
	fake := &intruderAPIFake{}
	handler = NewServer(Config{APIAddr: "127.0.0.1:9080", Intruder: fake}).Handler()
	for _, tc := range []struct {
		host, origin, contentType, body string
		want                            int
	}{
		{"evil.example", "", "application/json", `{}`, 421},
		{"127.0.0.1:9080", "http://evil.example", "application/json", `{}`, 403},
		{"127.0.0.1:9080", "", "text/plain", `{}`, 415},
		{"127.0.0.1:9080", "", "application/json", `{"unknown":1}`, 400},
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/intruder/jobs", strings.NewReader(tc.body))
		req.Host = tc.host
		req.Header.Set("Content-Type", tc.contentType)
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Fatalf("request %+v: %d", tc, rec.Code)
		}
	}
}

func TestIntruderCreateDecodesBinaryAndMapsErrors(t *testing.T) {
	fake := &intruderAPIFake{}
	handler := NewServer(Config{APIAddr: "127.0.0.1:9080", Intruder: fake}).Handler()
	body := []byte(`{"config":{"attack":"sniper","template":{"method":"GET","url":"http://example.test/","raw":"AAE="},"positions":[],"payloadSets":[],"requestLimit":1,"concurrency":1,"ratePerSecond":1,"timeoutMs":1000}}`)
	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/intruder/jobs", bytes.NewReader(body))
		req.Host = "127.0.0.1:9080"
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	if rec := request(); rec.Code != 201 || !bytes.Equal(fake.created.Config.Template.Raw, []byte{0, 1}) || fake.created.Config.Timeout.Milliseconds() != 1000 {
		t.Fatalf("create: %d %+v", rec.Code, fake.created)
	}
	body = bytes.Replace(body, []byte(`"timeoutMs":1000`), []byte(`"timeoutMs":9223372036854775807`), 1)
	if rec := request(); rec.Code != 422 {
		t.Fatalf("overflow timeout status=%d", rec.Code)
	}
	body = bytes.Replace(body, []byte(`"timeoutMs":9223372036854775807`), []byte(`"timeoutMs":1000`), 1)
	for _, tc := range []struct {
		err    error
		status int
	}{
		{intruder.ErrScopeDenied, 403},
		{intruder.ErrRevisionConflict, 409},
		{&intruder.FieldError{Field: "positions", Code: "overlap"}, 422},
		{errors.New("secret database path"), 500},
	} {
		fake.createErr = tc.err
		rec := request()
		if rec.Code != tc.status || strings.Contains(rec.Body.String(), "secret database path") {
			t.Fatalf("err=%v status=%d body=%s", tc.err, rec.Code, rec.Body.String())
		}
	}
}
