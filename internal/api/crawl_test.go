package api

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lutzifer/burpsuite-clone/internal/crawl"
	"github.com/lutzifer/burpsuite-clone/internal/store"
)

type crawlerFake struct {
	input   crawl.Request
	cancels []int64
}

func (f *crawlerFake) Start(_ context.Context, input crawl.Request) (crawl.Report, error) {
	f.input = input
	return crawl.Report{RunID: 7, State: "running"}, nil
}
func (f *crawlerFake) Cancel(id int64) error { f.cancels = append(f.cancels, id); return nil }

func TestCrawlAPI(t *testing.T) {
	fake := &crawlerFake{}
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "crawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	s := NewServer(Config{Store: repository, APIAddr: "127.0.0.1:9080", Crawler: fake})
	if rec := storageRequest(s, "POST", "/api/crawl/runs", `{"historyId":3,"maxPages":5,"maxDepth":1,"acknowledge":true}`); rec.Code != http.StatusAccepted || fake.input.MaxPages != 5 {
		t.Fatalf("start=%d %s", rec.Code, rec.Body.String())
	}
	if rec := storageRequest(s, "POST", "/api/crawl/runs", `{"unexpected":true}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid=%d", rec.Code)
	}
	if rec := storageRequest(s, "POST", "/api/crawl/runs/7/cancel", `{}`); rec.Code != http.StatusNoContent || len(fake.cancels) != 1 {
		t.Fatalf("cancel=%d %+v", rec.Code, fake.cancels)
	}
	if rec := storageRequest(s, "GET", "/api/crawl/runs", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "[]") {
		t.Fatalf("list=%d %s", rec.Code, rec.Body.String())
	}
}
