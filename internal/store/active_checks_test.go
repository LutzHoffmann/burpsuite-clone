package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestActiveCheckRunLifecycle(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	exchange := &Exchange{Method: "GET", Scheme: "https", Host: "example.test", Path: "/", Status: 200, InScope: true, StartedAt: time.Now()}
	if err := s.SaveExchange(ctx, exchange); err != nil {
		t.Fatal(err)
	}
	crawlID, err := s.CreateCrawlRun(ctx, exchange.ID, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateActiveCheckRun(ctx, crawlID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("accepted running crawl: %v", err)
	}
	if err := s.FinishCrawlRun(ctx, crawlID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	id, err := s.CreateActiveCheckRun(ctx, crawlID)
	if err != nil {
		t.Fatal(err)
	}
	item := ActiveCheckObservation{URL: "https://example.test/?key=secret", Source: "query", Parameter: "key", Status: 200, Found: true, Context: "html_text"}
	if err := s.AppendActiveCheck(ctx, id, item); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendActiveCheck(ctx, id, item); err == nil {
		t.Fatal("duplicate observation accepted")
	}
	run, err := s.GetActiveCheckRun(ctx, id)
	if err != nil || run.State != "running" || len(run.Observations) != 1 || run.Observations[0].URL != "https://example.test/" {
		t.Fatalf("run=%+v err=%v", run, err)
	}
	if err := s.RecoverActiveCheckRuns(ctx); err != nil {
		t.Fatal(err)
	}
	run, err = s.GetActiveCheckRun(ctx, id)
	if err != nil || run.State != "interrupted" {
		t.Fatalf("recovered=%+v err=%v", run, err)
	}
	if err := s.DeleteActiveCheckRun(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetActiveCheckRun(ctx, id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted=%v", err)
	}
	if _, err := s.GetCrawlRun(ctx, crawlID); err != nil {
		t.Fatalf("crawl deleted: %v", err)
	}
}
