package api

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lutzifer/burpsuite-clone/internal/activechecks"
	"github.com/lutzifer/burpsuite-clone/internal/store"
)

type activeChecksFake struct {
	input     activechecks.Request
	cancelled int64
}

func (f *activeChecksFake) Start(_ context.Context, input activechecks.Request) (activechecks.Report, error) {
	f.input = input
	return activechecks.Report{RunID: 4, State: "running", MaximumRequests: 2}, nil
}
func (f *activeChecksFake) Cancel(id int64) error { f.cancelled = id; return nil }

func TestActiveChecksAPI(t *testing.T) {
	fake := &activeChecksFake{}
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "checks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	s := NewServer(Config{Store: repository, APIAddr: "127.0.0.1:9080", ActiveChecks: fake})
	if rec := storageRequest(s, "POST", "/api/active-checks/runs", `{"crawlId":3,"acknowledge":true}`); rec.Code != http.StatusAccepted || fake.input.CrawlID != 3 {
		t.Fatalf("start=%d %s", rec.Code, rec.Body.String())
	}
	cookie := strings.Repeat("a", 2048)
	if rec := storageRequest(s, "POST", "/api/active-checks/runs", `{"crawlId":3,"acknowledge":true,"session":{"cookie":"`+cookie+`"}}`); rec.Code != http.StatusAccepted || fake.input.Session.Cookie != cookie {
		t.Fatalf("session start=%d", rec.Code)
	}
	if rec := storageRequest(s, "POST", "/api/active-checks/runs", `{"unknown":true}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid=%d", rec.Code)
	}
	if rec := storageRequest(s, "POST", "/api/active-checks/runs/4/cancel", `{}`); rec.Code != http.StatusNoContent || fake.cancelled != 4 {
		t.Fatalf("cancel=%d", rec.Code)
	}
	if rec := storageRequest(s, "GET", "/api/active-checks/runs", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "[]") {
		t.Fatalf("list=%d %s", rec.Code, rec.Body.String())
	}
}

func TestActiveChecksReportAPI(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "report.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	exchange := &store.Exchange{Method: "GET", Scheme: "https", Host: "example.test", Path: "/", Status: 200, InScope: true}
	if err := repository.SaveExchange(ctx, exchange); err != nil {
		t.Fatal(err)
	}
	crawlID, err := repository.CreateCrawlRun(ctx, exchange.ID, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.FinishCrawlRun(ctx, crawlID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	id, err := repository.CreateActiveCheckRun(ctx, crawlID)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.AppendActiveCheck(ctx, id, store.ActiveCheckObservation{URL: "https://example.test/<script>", Source: "query", Parameter: "q", Status: 200, Found: true, Context: "html_text"}); err != nil {
		t.Fatal(err)
	}
	if err := repository.FinishActiveCheckRun(ctx, id, "completed", ""); err != nil {
		t.Fatal(err)
	}
	s := NewServer(Config{Store: repository, APIAddr: "127.0.0.1:9080"})
	rec := storageRequest(s, "GET", "/api/active-checks/runs/1/report.html", "")
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") || !strings.Contains(rec.Header().Get("Content-Disposition"), "attachment") || strings.Contains(rec.Body.String(), "<script>") {
		t.Fatalf("report=%d headers=%v body=%s", rec.Code, rec.Header(), rec.Body.String())
	}
}
