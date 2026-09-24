package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lutzifer/burpsuite-clone/internal/activescan"
)

type activeScannerFake struct {
	input activescan.Request
	calls int
}

func (f *activeScannerFake) Scan(_ context.Context, input activescan.Request) (activescan.Report, error) {
	f.input = input
	f.calls++
	return activescan.Report{HistoryID: input.HistoryID, Probes: []activescan.Probe{}}, nil
}

func TestActiveScanAPIProtections(t *testing.T) {
	fake := &activeScannerFake{}
	handler := NewServer(Config{APIAddr: "127.0.0.1:9080", ActiveScanner: fake}).Handler()
	request := func(host, contentType, body string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/active-scan", strings.NewReader(body))
		req.Host = host
		req.Header.Set("Content-Type", contentType)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := request("127.0.0.1:9080", "application/json", `{"historyId":7,"maxProbes":2,"acknowledge":true}`); got != 200 || fake.calls != 1 || fake.input.HistoryID != 7 {
		t.Fatalf("valid status=%d calls=%d", got, fake.calls)
	}
	for _, tc := range []struct {
		host, contentType, body string
		want                    int
	}{
		{"evil.test", "application/json", `{}`, 421},
		{"127.0.0.1:9080", "text/plain", `{}`, 415},
		{"127.0.0.1:9080", "application/json", `{"unknown":1}`, 400},
	} {
		if got := request(tc.host, tc.contentType, tc.body); got != tc.want {
			t.Fatalf("%+v => %d", tc, got)
		}
	}
	if fake.calls != 1 {
		t.Fatalf("blocked requests reached scanner: %d", fake.calls)
	}
}
