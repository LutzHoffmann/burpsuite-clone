package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func wsConnection(t *testing.T, s *SQLiteStore) *WSConnection {
	t.Helper()
	c := &WSConnection{URL: "wss://example.test/é", InScope: true, OpenedAt: time.Now().UTC(), State: "open"}
	if err := s.SaveWSConnection(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return c
}

func wsMessage(c int64, seq int64) *WSMessage {
	return &WSMessage{ConnectionID: c, Sequence: seq, Direction: "clientToServer", ObservedAt: time.Now().UTC(), Type: "text", Size: 6, Payload: []byte("abcdef"), Complete: true, Encoding: "identity"}
}

func TestWSRoundTripBoundsAndJSON(t *testing.T) {
	s := openTestStore(t)
	s.bodyLimitBytes = 3
	ctx := context.Background()
	c := wsConnection(t, s)
	m := wsMessage(c.ID, 1)
	if err := s.SaveWSMessage(ctx, m); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetWSMessage(ctx, c.ID, m.ID)
	if err != nil || string(got.Payload) != "abc" || !got.Truncated || !got.Complete || got.Size != 6 || !got.ObservedAt.Equal(m.ObservedAt) {
		t.Fatalf("%+v %v", got, err)
	}
	want := CaptureRecordAllowance*2 + int64(len(c.URL)+len(m.Direction)+len(m.Type)+len(m.Encoding)+3)
	if used := quotaStatus(t, s).UsedBytes; used != want {
		t.Fatalf("charge %d want %d", used, want)
	}
	page, err := s.ListWSMessages(ctx, c.ID, WSPageRequest{})
	if err != nil || len(page.Items) != 1 || page.Items[0].Payload != nil {
		t.Fatalf("%+v %v", page, err)
	}
	data, _ := json.Marshal(got)
	if strings.Contains(string(data), "payload") || !strings.Contains(string(data), `"connectionId":`) || strings.Contains(string(data), `"ID"`) {
		t.Fatal(string(data))
	}
	cc, err := s.GetWSConnection(ctx, c.ID)
	if err != nil || cc.URL != c.URL || !cc.InScope || !cc.OpenedAt.Equal(c.OpenedAt) || cc.ClosedAt != nil {
		t.Fatalf("%+v %v", cc, err)
	}
	// Even older captures read under a smaller configured limit stay bounded.
	s.bodyLimitBytes = 1
	got, err = s.GetWSMessage(ctx, c.ID, m.ID)
	if err != nil || string(got.Payload) != "a" || !got.Truncated {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestWSQuotaRollbackResumeAndFinish(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	c := wsConnection(t, s)
	before := quotaStatus(t, s)
	if _, err := s.db.Exec(`CREATE TRIGGER fail_ws BEFORE INSERT ON ws_messages BEGIN SELECT RAISE(ABORT,'test'); END`); err != nil {
		t.Fatal(err)
	}
	m := wsMessage(c.ID, 1)
	if err := s.SaveWSMessage(ctx, m); err == nil || m.ID != 0 {
		t.Fatal("expected failed insert", err)
	}
	if got := quotaStatus(t, s); got != before {
		t.Fatalf("reservation leaked: %+v", got)
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_ws`); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveWSMessage(ctx, wsMessage(999, 1)); err == nil {
		t.Fatal("missing parent accepted")
	}
	if got := quotaStatus(t, s); got != before {
		t.Fatal("missing parent leaked quota", got)
	}
	if _, err := s.SetStorageLimit(ctx, before.UsedBytes+1); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveWSMessage(ctx, m); !errors.Is(err, ErrCaptureQuotaExceeded) {
		t.Fatal(err)
	}
	rejected := &WSConnection{URL: "ws://rejected", OpenedAt: time.Now()}
	if err := s.SaveWSConnection(ctx, rejected); !errors.Is(err, ErrCaptureQuotaExceeded) || rejected.ID != 0 {
		t.Fatal(err)
	}
	if err := s.FinishWSConnection(ctx, c.ID, "closed", 2, true); err != nil {
		t.Fatal(err)
	}
	if got := quotaStatus(t, s); got.UsedBytes != before.UsedBytes || !got.Paused || got.SkippedRecords != 2 {
		t.Fatal(got)
	}
	c2 := wsConnectionAfterResume(t, s)
	if err := s.SaveWSMessage(ctx, wsMessage(c2.ID, 1)); err != nil {
		t.Fatal(err)
	}
	cc, err := s.GetWSConnection(ctx, c.ID)
	if err != nil || cc.State != "closed" || cc.Gaps != 2 || !cc.CaptureIncomplete || cc.ClosedAt == nil {
		t.Fatalf("%+v %v", cc, err)
	}
}

func wsConnectionAfterResume(t *testing.T, s *SQLiteStore) *WSConnection {
	t.Helper()
	if _, err := s.SetStorageLimit(context.Background(), 100000); err != nil {
		t.Fatal(err)
	}
	return wsConnection(t, s)
}

func TestWSStablePagesAndOwnership(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	c := wsConnection(t, s)
	for i := int64(1); i <= 205; i++ {
		if err := s.SaveWSMessage(ctx, wsMessage(c.ID, i)); err != nil {
			t.Fatal(err)
		}
		wsConnection(t, s)
	}
	first, err := s.ListWSMessages(ctx, c.ID, WSPageRequest{})
	if err != nil || len(first.Items) != 100 || first.SnapshotID != 205 || first.NextBeforeID != 106 {
		t.Fatalf("%+v %v", first, err)
	}
	cp, err := s.ListWSConnections(ctx, WSPageRequest{})
	if err != nil || len(cp.Items) != 100 || cp.SnapshotID != 206 || cp.NextBeforeID != 107 {
		t.Fatalf("%+v %v", cp, err)
	}
	if err := s.SaveWSMessage(ctx, wsMessage(c.ID, 206)); err != nil {
		t.Fatal(err)
	}
	other := wsConnection(t, s)
	second, err := s.ListWSMessages(ctx, c.ID, WSPageRequest{BeforeID: first.NextBeforeID, SnapshotID: first.SnapshotID})
	if err != nil || len(second.Items) != 100 || second.Items[0].ID != 105 || second.NextBeforeID != 6 {
		t.Fatalf("%+v %v", second, err)
	}
	last, err := s.ListWSMessages(ctx, c.ID, WSPageRequest{BeforeID: second.NextBeforeID, SnapshotID: first.SnapshotID})
	if err != nil || len(last.Items) != 5 || last.NextBeforeID != 0 {
		t.Fatalf("%+v %v", last, err)
	}
	again, err := s.ListWSMessages(ctx, c.ID, WSPageRequest{SnapshotID: first.SnapshotID})
	if err != nil || again.Items[0].ID != 205 {
		t.Fatalf("%+v %v", again, err)
	}
	cp2, err := s.ListWSConnections(ctx, WSPageRequest{BeforeID: cp.NextBeforeID, SnapshotID: cp.SnapshotID})
	if err != nil || len(cp2.Items) != 100 || cp2.Items[0].ID != 106 {
		t.Fatalf("%+v %v", cp2, err)
	}
	if _, err := s.GetWSMessage(ctx, other.ID, first.Items[0].ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("ownership", err)
	}
	empty, err := s.ListWSMessages(ctx, other.ID, WSPageRequest{})
	if err != nil || empty.Items == nil || len(empty.Items) != 0 {
		t.Fatalf("%+v %v", empty, err)
	}
	for _, r := range []WSPageRequest{{BeforeID: -1}, {SnapshotID: -1}, {BeforeID: 1}, {BeforeID: 3, SnapshotID: 2}} {
		if _, err := s.ListWSConnections(ctx, r); err == nil {
			t.Fatal("invalid connection cursor", r)
		}
		if _, err := s.ListWSMessages(ctx, c.ID, r); err == nil {
			t.Fatal("invalid message cursor", r)
		}
	}
	if _, err := s.ListWSMessages(ctx, 9999, WSPageRequest{}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	if err := s.FinishWSConnection(ctx, 9999, "closed", 0, false); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
}

func TestWSMigrationAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	ctx := context.Background()
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations {
		if m.version >= 7 {
			break
		}
		if err := m.apply(tx); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations VALUES (?)`, m.version); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	c := wsConnection(t, s)
	closed := wsConnection(t, s)
	if err := s.SaveWSMessage(ctx, wsMessage(c.ID, 1)); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishWSConnection(ctx, closed.ID, "closed", 3, false); err != nil {
		t.Fatal(err)
	}
	before := quotaStatus(t, s)
	s.Close()
	s, err = OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, err := s.GetWSConnection(ctx, c.ID); err != nil || got.State != "open" {
		t.Fatal("opening a handle changed live state", got, err)
	}
	if err := s.RecoverWSConnections(ctx); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := s.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != 8 {
		t.Fatal(version, err)
	}
	got, err := s.GetWSConnection(ctx, c.ID)
	if err != nil || got.State != "interrupted" || !got.CaptureIncomplete || got.ClosedAt == nil {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = s.GetWSConnection(ctx, closed.ID)
	if err != nil || got.State != "closed" || got.Gaps != 3 || got.CaptureIncomplete {
		t.Fatalf("%+v %v", got, err)
	}
	if got := quotaStatus(t, s); got != before {
		t.Fatal("restart charged quota", got)
	}
	if _, err := s.GetWSMessage(ctx, c.ID, 1); err != nil {
		t.Fatal(err)
	}
}

func TestWSConcurrentQuotaAndValidation(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	c := wsConnection(t, s)
	before := quotaStatus(t, s)
	for _, m := range []*WSMessage{nil, wsMessage(0, 1), wsMessage(c.ID, -1)} {
		if err := s.SaveWSMessage(ctx, m); err == nil {
			t.Fatal("invalid message accepted")
		}
	}
	if err := s.SaveWSConnection(ctx, nil); err == nil {
		t.Fatal("nil connection")
	}
	if err := s.FinishWSConnection(ctx, c.ID, strings.Repeat("x", 1000), 0, false); err == nil {
		t.Fatal("unbounded state")
	}
	if err := s.FinishWSConnection(ctx, c.ID, "closed", -1, false); err == nil {
		t.Fatal("negative gaps")
	}
	m := wsMessage(c.ID, 1)
	charge := CaptureRecordAllowance + captureTextBytes(m.Direction, m.Type, m.Encoding) + int64(len(m.Payload))
	if _, err := s.SetStorageLimit(ctx, before.UsedBytes+3*charge); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for i := int64(1); i <= 12; i++ {
		wg.Add(1)
		go func(seq int64) { defer wg.Done(); results <- s.SaveWSMessage(ctx, wsMessage(c.ID, seq)) }(i)
	}
	wg.Wait()
	close(results)
	saved := 0
	for err := range results {
		if err == nil {
			saved++
		} else if !errors.Is(err, ErrCaptureQuotaExceeded) {
			t.Fatal(err)
		}
	}
	if got := quotaStatus(t, s); saved != 3 || got.UsedBytes != before.UsedBytes+3*charge || got.SkippedRecords != 9 {
		t.Fatal(saved, got)
	}
}
