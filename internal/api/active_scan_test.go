package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lutzifer/burpsuite-clone/internal/activescan"
	"github.com/lutzifer/burpsuite-clone/internal/store"
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

func TestActiveScanHistoryAPI(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "scan.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	exchange := &store.Exchange{Method: "GET", Scheme: "https", Host: "example.test", Path: "/", Status: 200, InScope: true}
	if err := repository.SaveExchange(context.Background(), exchange); err != nil {
		t.Fatal(err)
	}
	id, err := repository.CreateActiveScanRun(context.Background(), exchange.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.AppendActiveScanProbe(context.Background(), id, 0, store.ActiveScanProbe{Parameter: "q", Status: 200, Reflected: true}); err != nil {
		t.Fatal(err)
	}
	if err := repository.FinishActiveScanRun(context.Background(), id, "completed", ""); err != nil {
		t.Fatal(err)
	}
	s := NewServer(Config{Store: repository, APIAddr: "127.0.0.1:9080"})
	if rec := storageRequest(s, "GET", "/api/active-scan/runs", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"reflectedCount":1`) {
		t.Fatalf("list=%d %s", rec.Code, rec.Body.String())
	}
	if rec := storageRequest(s, "GET", "/api/active-scan/runs/1", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"parameter":"q"`) {
		t.Fatalf("detail=%d %s", rec.Code, rec.Body.String())
	}
	for _, path := range []string{"/api/active-scan/runs/no", "/api/active-scan/runs/0"} {
		if rec := storageRequest(s, "GET", path, ""); rec.Code != 400 {
			t.Fatalf("%s=%d", path, rec.Code)
		}
	}
	if rec := storageRequest(s, "GET", "/api/active-scan/runs/999", ""); rec.Code != 404 {
		t.Fatalf("missing=%d", rec.Code)
	}
	if rec := storageRequest(s, "DELETE", "/api/active-scan/runs/1", ""); rec.Code != 204 {
		t.Fatalf("delete=%d %s", rec.Code, rec.Body.String())
	}
	if rec := storageRequest(s, "GET", "/api/active-scan/runs/1", ""); rec.Code != 404 {
		t.Fatalf("deleted detail=%d", rec.Code)
	}
}
