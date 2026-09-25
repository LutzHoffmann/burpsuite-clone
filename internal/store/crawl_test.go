package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestCrawlRunLifecycle(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	exchange := &Exchange{Method: "GET", Scheme: "https", Host: "example.test", Path: "/", Status: 200, InScope: true, StartedAt: time.Now()}
	if err := s.SaveExchange(ctx, exchange); err != nil {
		t.Fatal(err)
	}
	id, err := s.CreateCrawlRun(ctx, exchange.ID, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendCrawlPage(ctx, id, CrawlPage{URL: "https://example.test/?token=secret", Status: 200}, []CrawlForm{{PageURL: "https://example.test/?token=secret", ActionURL: "https://example.test/post?key=secret", Method: "POST", Fields: []CrawlField{{Name: "q", Type: "text"}}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendCrawlPage(ctx, id, CrawlPage{URL: "https://example.test/?token=secret"}, nil); err == nil {
		t.Fatal("duplicate page accepted")
	}
	run, err := s.GetCrawlRun(ctx, id)
	if err != nil || run.State != "running" || run.PageCount != 1 || len(run.Pages) != 1 || len(run.Forms) != 1 || len(run.Forms[0].Fields) != 1 {
		t.Fatalf("run=%+v err=%v", run, err)
	}
	if run.Pages[0].URL != "https://example.test/" || run.Forms[0].ActionURL != "https://example.test/post" {
		t.Fatalf("stored query values: %+v", run)
	}
	if err := s.DeleteCrawlRun(ctx, id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted running job: %v", err)
	}
	if err := s.RecoverCrawlRuns(ctx); err != nil {
		t.Fatal(err)
	}
	run, err = s.GetCrawlRun(ctx, id)
	if err != nil || run.State != "interrupted" {
		t.Fatalf("recovered=%+v err=%v", run, err)
	}
	if err := s.DeleteCrawlRun(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCrawlRun(ctx, id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted run=%v", err)
	}
	if _, err := s.GetExchange(ctx, exchange.ID); err != nil {
		t.Fatalf("history deleted: %v", err)
	}
}
