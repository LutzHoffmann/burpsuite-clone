package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestActiveScanPersistenceAndRecovery(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	exchange := &Exchange{Method: "GET", Scheme: "https", Host: "example.test", Path: "/search", Query: "q=private", Status: 200, InScope: true, StartedAt: time.Now()}
	if err := s.SaveExchange(ctx, exchange); err != nil {
		t.Fatal(err)
	}
	id, err := s.CreateActiveScanRun(ctx, exchange.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendActiveScanProbe(ctx, id, 0, ActiveScanProbe{Parameter: "q", Status: 200, Reflected: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendActiveScanProbe(ctx, id, 0, ActiveScanProbe{Parameter: "q"}); err == nil {
		t.Fatal("duplicate sequence accepted")
	}
	run, err := s.GetActiveScanRun(ctx, id)
	if err != nil || run.State != "running" || run.ProbeCount != 1 || len(run.Probes) != 1 || !run.Probes[0].Reflected || run.Host != "example.test" {
		t.Fatalf("running=%+v, %v", run, err)
	}
	if err := s.FinishActiveScanRun(ctx, id, "completed", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendActiveScanProbe(ctx, id, 1, ActiveScanProbe{Parameter: "next"}); err == nil {
		t.Fatal("appended to completed run")
	}
	runs, err := s.ListActiveScanRuns(ctx)
	if err != nil || len(runs) != 1 || runs[0].ReflectedCount != 1 || runs[0].FinishedAt == nil {
		t.Fatalf("runs=%+v, %v", runs, err)
	}
	if _, err := s.GetActiveScanRun(ctx, id+999); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing run=%v", err)
	}
	second, err := s.CreateActiveScanRun(ctx, exchange.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteActiveScanRun(ctx, second); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted running scan: %v", err)
	}
	if err := s.RecoverActiveScanRuns(ctx); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.GetActiveScanRun(ctx, second)
	if err != nil || recovered.State != "interrupted" || recovered.StoppedReason != "application_restarted" {
		t.Fatalf("recovered=%+v, %v", recovered, err)
	}
	if err := s.DeleteActiveScanRun(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetActiveScanRun(ctx, id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted run=%v", err)
	}
	if _, err := s.GetExchange(ctx, exchange.ID); err != nil {
		t.Fatalf("source history deleted: %v", err)
	}
}
