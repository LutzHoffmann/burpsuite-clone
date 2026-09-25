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
	createErr   error
	created     intruder.Draft
	resultQuery intruder.ResultQuery
	previewed   intruder.Config
	baseline    int64
}

func (f *intruderAPIFake) Preview(_ context.Context, cfg intruder.Config) (intruder.Preview, error) {
	f.previewed = cfg
	return intruder.Preview{TotalRequests: 2, SampleMethod: "GET", SampleURL: cfg.Template.URL}, nil
}

func (f *intruderAPIFake) SetBaseline(_ context.Context, id string, revision, sequence int64) (intruder.Job, error) {
	f.baseline = sequence
	return intruder.Job{ID: id, State: intruder.StateCompleted, Revision: revision + 1, BaselineSequence: &sequence}, nil
}

func TestIntruderPreviewAndBaselineRoutes(t *testing.T) {
	fake := &intruderAPIFake{}
	handler := NewServer(Config{APIAddr: "127.0.0.1:9080", Intruder: fake}).Handler()
	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Host = "127.0.0.1:9080"
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	config := `{"config":{"attack":"sniper","template":{"method":"GET","url":"http://example.test/","raw":"AAE="},"positions":[],"payloadSets":[],"requestLimit":2,"concurrency":1,"ratePerSecond":1,"timeoutMs":1000}}`
	if rec := post("/api/intruder/preview", config); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"totalRequests":2`) || fake.previewed.Template.URL != "http://example.test/" {
		t.Fatalf("preview = %d %s", rec.Code, rec.Body.String())
	}
	if rec := post("/api/intruder/preview", `{"config":{},"unexpected":true}`); rec.Code != 400 {
		t.Fatalf("unknown field = %d", rec.Code)
	}
	id := strings.Repeat("a", 32)
	if rec := post("/api/intruder/jobs/"+id+"/baseline", `{"revision":3,"sequence":1}`); rec.Code != 200 || fake.baseline != 1 || !strings.Contains(rec.Body.String(), `"baselineSequence":1`) {
		t.Fatalf("baseline = %d %s", rec.Code, rec.Body.String())
	}
	if rec := post("/api/intruder/jobs/"+id+"/baseline", `{"revision":3,"sequence":-1}`); rec.Code != 422 {
		t.Fatalf("invalid sequence = %d", rec.Code)
	}
}

func (f *intruderAPIFake) ListResults(_ context.Context, _ string, query intruder.ResultQuery) (intruder.ResultPage, error) {
	f.resultQuery = query
	return intruder.ResultPage{Results: []intruder.Result{}}, nil
}

func TestIntruderResultFiltersAreValidatedAndForwarded(t *testing.T) {
	fake := &intruderAPIFake{}
	handler := NewServer(Config{APIAddr: "127.0.0.1:9080", Intruder: fake}).Handler()
	base := "/api/intruder/jobs/" + strings.Repeat("a", 32) + "/results"
	request := func(query string) int {
		req := httptest.NewRequest(http.MethodGet, base+query, nil)
		req.Host = "127.0.0.1:9080"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := request("?limit=25&beforeSequence=9&status=404&minSize=3&maxSize=20&minDurationMs=5&maxDurationMs=90&minSimilarity=10&maxSimilarity=80&payloadSearch=needle&errorCategory=network&mimeType=text%2Fplain"); got != 200 {
		t.Fatalf("valid filter status=%d", got)
	}
	q := fake.resultQuery
	if q.Limit != 25 || q.BeforeSequence == nil || *q.BeforeSequence != 9 || q.Status != 404 || q.MinSize != 3 || q.MaxSize != 20 || q.MinDurationMS != 5 || q.MaxDurationMS != 90 || q.MinSimilarity != 10 || q.MaxSimilarity != 80 || q.PayloadSearch != "needle" || q.ErrorCategory != "network" || q.MIMEType != "text/plain" {
		t.Fatalf("forwarded query=%+v", q)
	}
	for _, value := range []string{"?limit=101", "?status=999", "?minSize=20&maxSize=3", "?minSimilarity=101", "?payloadSearch=", "?unknown=1", "?limit=2&limit=3"} {
		if got := request(value); got != 400 {
			t.Fatalf("%s: status=%d", value, got)
		}
	}
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
