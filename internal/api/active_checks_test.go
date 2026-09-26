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
