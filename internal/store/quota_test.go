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
)

func TestQuotaRevisionOrdersPauseAndResume(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	first, err := s.SetStorageLimit(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveExchange(ctx, &Exchange{}); !errors.Is(err, ErrCaptureQuotaExceeded) {
		t.Fatal(err)
	}
	paused := quotaStatus(t, s)
	if paused.Revision <= first.Revision || !paused.Paused {
		t.Fatal(paused)
	}
	_ = s.SaveExchange(ctx, &Exchange{})
	if again := quotaStatus(t, s); again.Revision != paused.Revision {
		t.Fatal("duplicate skip changed transition revision", again)
	}
	resumed, err := s.SetStorageLimit(ctx, 10000)
	if err != nil || resumed.Revision <= paused.Revision || resumed.Paused {
		t.Fatal(resumed, err)
	}
}

func TestQuotaRevisionTransitions(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "revision.db")
	a, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if got := quotaStatus(t, a).Revision; got != 0 {
		t.Fatal(got)
	}
	status, err := a.SetStorageLimit(ctx, 600)
	if err != nil || status.Revision != 1 {
		t.Fatalf("%+v %v", status, err)
	}
	if err := b.SaveExchange(ctx, &Exchange{}); err != nil {
		t.Fatal(err)
	}
	if got := quotaStatus(t, a).Revision; got != 1 {
		t.Fatal("successful capture advanced revision", got)
	}
	if err := b.SaveExchange(ctx, &Exchange{Note: strings.Repeat("x", 1000)}); !errors.Is(err, ErrCaptureQuotaExceeded) {
		t.Fatal(err)
	}
	paused := quotaStatus(t, a)
	if paused.Revision != 2 || !paused.Paused {
		t.Fatal(paused)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := b.SaveExchange(ctx, &Exchange{}); !errors.Is(err, ErrCaptureQuotaExceeded) {
				t.Errorf("skip: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := quotaStatus(t, a); got.Revision != 2 || got.SkippedRecords != 9 {
		t.Fatal(got)
	}
	status, err = a.SetStorageLimit(ctx, 600)
	if err != nil || status.Revision != 3 || !status.Paused {
		t.Fatalf("%+v %v", status, err)
	}
	status, err = a.SetStorageLimit(ctx, 1000)
	if err != nil || status.Revision != 4 || status.Paused {
		t.Fatalf("%+v %v", status, err)
	}
	if got := quotaStatus(t, b); got.Revision <= paused.Revision || got.Paused {
		t.Fatal("stale pause after resume", got)
	}
	if _, err := a.SetStorageLimit(ctx, 0); err == nil {
		t.Fatal("accepted invalid limit")
	}
	if got := quotaStatus(t, b).Revision; got != 4 {
		t.Fatal(got)
	}
	reopened, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := quotaStatus(t, reopened); got != status {
		t.Fatalf("restart: %+v want %+v", got, status)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 4 {
		t.Fatalf("revision leaked into JSON: %s", encoded)
	}
}

func TestQuotaMigrationExistingBytes(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations {
		if m.version >= 6 {
			break
		}
		if err := m.apply(tx); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations VALUES (?)`, m.version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO exchanges(method,scheme,host,path,query,status,mime_type,request_size,response_size,duration_ms,started_at_unix_nano,intercepted,error,error_message,request_truncated,response_truncated,tags_json,note)
 VALUES ('','','','','',0,'',999999,999999,0,0,0,0,'',0,0,'null',?)`, "é\x00x"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO exchange_bodies VALUES (1,'{}',x'010203',NULL,'{}',NULL,NULL);
 INSERT INTO repeater_sessions VALUES ('s',1,'s',0,0);
 INSERT INTO repeater_sends VALUES (1,'s','','','null','',0,'null','',0,999999,0,'',0)`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if status := quotaStatus(t, s); status.UsedBytes != 266+4+3+265 || status.Paused {
		t.Fatal(status)
	}
	if err := s.SaveExchange(ctx, &Exchange{Note: "é\x00x", Request: RequestData{Body: []byte{1, 2, 3}}}); err != nil {
		t.Fatal(err)
	}
	if status := quotaStatus(t, s); status.UsedBytes != 2*(266+4+3)+265 {
		t.Fatal(status)
	}
}

func quotaStatus(t *testing.T, s *SQLiteStore) StorageStatus {
	t.Helper()
	status, err := s.StorageStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func TestQuotaLegacyHistoryBounded(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	for i := 0; i < 101; i++ {
		if err := s.SaveExchange(ctx, &Exchange{}); err != nil {
			t.Fatal(err)
		}
	}
	items, err := s.ListHistory(ctx, HistoryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 100 || items[0].ID != 101 || items[99].ID != 2 {
		t.Fatalf("legacy history not bounded to newest 100: %d", len(items))
	}
}

func TestQuotaLatchRejectsSmallerFittingRecord(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	if _, err := s.SetStorageLimit(ctx, 600); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveExchange(ctx, &Exchange{Note: strings.Repeat("x", 1000)}); !errors.Is(err, ErrCaptureQuotaExceeded) {
		t.Fatal(err)
	}
	if err := s.SaveExchange(ctx, &Exchange{}); !errors.Is(err, ErrCaptureQuotaExceeded) {
		t.Fatal("smaller record escaped latch", err)
	}
	if status := quotaStatus(t, s); status.UsedBytes != 0 || status.SkippedRecords != 2 || !status.Paused {
		t.Fatal(status)
	}
}

func TestQuotaMetadataTagsAndRollback(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	e := &Exchange{}
	if err := s.SaveExchange(ctx, e); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateExchangeMetadata(ctx, e.ID, []string{"é"}, "x"); err != nil {
		t.Fatal(err)
	}
	before := quotaStatus(t, s)
	if before.UsedBytes != 269 {
		t.Fatal(before)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_metadata BEFORE UPDATE OF note ON exchanges BEGIN SELECT RAISE(ABORT, 'test'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateExchangeMetadata(ctx, e.ID, []string{"larger"}, "longer"); err == nil {
		t.Fatal("expected database failure")
	}
	if got := quotaStatus(t, s); got != before {
		t.Fatal(got)
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_metadata`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetStorageLimit(ctx, 600); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveExchange(ctx, &Exchange{Note: strings.Repeat("x", 1000)}); !errors.Is(err, ErrCaptureQuotaExceeded) {
		t.Fatal(err)
	}
	if err := s.UpdateExchangeMetadata(ctx, e.ID, []string{"é"}, "xx"); !errors.Is(err, ErrCaptureQuotaExceeded) {
		t.Fatal("growing edit escaped latch", err)
	}
	if err := s.UpdateExchangeMetadata(ctx, e.ID, []string{"é"}, "y"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateExchangeMetadata(ctx, 999, nil, ""); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	got, err := s.GetExchange(ctx, e.ID)
	if err != nil || got.Note != "y" || len(got.Tags) != 1 || got.Tags[0] != "é" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestQuotaRepeaterRollback(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.db.Exec(`CREATE TRIGGER fail_send BEFORE INSERT ON repeater_sends BEGIN SELECT RAISE(ABORT, 'test'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveRepeaterSend(ctx, &RepeaterSend{SessionID: "rollback"}); err == nil || errors.Is(err, ErrCaptureQuotaExceeded) {
		t.Fatal(err)
	}
	if status := quotaStatus(t, s); status.UsedBytes != 0 || status.Paused || status.SkippedRecords != 0 {
		t.Fatal(status)
	}
	sessions, err := s.ListRepeaterSessions(ctx)
	if err != nil || len(sessions) != 0 {
		t.Fatalf("%v %v", sessions, err)
	}
}

func TestQuotaMigrationAboveDefault(t *testing.T) {
	s := openTestStore(t)
	if err := s.SaveExchange(context.Background(), &Exchange{}); err != nil {
		t.Fatal(err)
	}
	// A virtual zero BLOB fixture exercises >1 GiB SQL accounting without
	// allocating or writing gigabytes of test data.
	if _, err := s.db.Exec(`DROP TABLE capture_quota;
		ALTER TABLE exchanges DROP COLUMN capture_bytes;
		ALTER TABLE repeater_sends DROP COLUMN capture_bytes;
		DELETE FROM schema_migrations WHERE version = 6;
		ALTER TABLE exchange_bodies RENAME TO retained_bodies;
		CREATE VIEW exchange_bodies AS SELECT exchange_id, request_headers_json,
		zeroblob(536870912) AS request_body, zeroblob(536870912) AS request_raw,
		response_headers_json, response_body, response_raw FROM retained_bodies;`); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(s.db); err != nil {
		t.Fatal(err)
	}
	if status := quotaStatus(t, s); status.UsedBytes != 1<<30+266 || !status.Paused || status.SkippedRecords != 0 {
		t.Fatal(status)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM exchanges`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("%d %v", count, err)
	}
	if err := applyMigrations(s.db); err != nil {
		t.Fatal(err)
	}
	if status := quotaStatus(t, s); status.UsedBytes != 1<<30+266 || !status.Paused {
		t.Fatal(status)
	}
}

func TestQuotaExactFitLatchRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "quota.db")
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	// Empty exchange: fixed allowance, null tags, empty rule list, two empty header objects.
	const charge = 256 + 4 + 2 + 2 + 2
	if status := quotaStatus(t, s); status.LimitBytes != 1<<30 || status.UsedBytes != 0 {
		t.Fatal(status)
	}
	if _, err := s.SetStorageLimit(ctx, charge); err != nil {
		t.Fatal(err)
	}
	e := &Exchange{}
	if err := s.SaveExchange(ctx, e); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveExchange(ctx, &Exchange{}); !errors.Is(err, ErrCaptureQuotaExceeded) {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if status := quotaStatus(t, s); !status.Paused || status.UsedBytes != charge || status.SkippedRecords != 1 {
		t.Fatal(status)
	}
	if _, err := s.SetStorageLimit(ctx, charge); err != nil {
		t.Fatal(err)
	}
	if !quotaStatus(t, s).Paused {
		t.Fatal("same limit cleared latch")
	}
	if _, err := s.SetStorageLimit(ctx, charge*2); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveExchange(ctx, &Exchange{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetExchange(ctx, e.ID); err != nil {
		t.Fatal("existing capture lost", err)
	}
	for _, limit := range []int64{0, -1, 1<<40 + 1} {
		if _, err := s.SetStorageLimit(ctx, limit); err == nil {
			t.Fatalf("accepted %d", limit)
		}
	}
	if _, err := s.SetStorageLimit(ctx, 1<<40); err != nil {
		t.Fatal(err)
	}
}

func TestQuotaRetainedBytesMetadataAndRollback(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	s.bodyLimitBytes = 3
	e := &Exchange{Note: "é", Request: RequestData{Body: []byte("abcdef"), Raw: []byte("abcdef")}}
	if err := s.SaveExchange(ctx, e); err != nil {
		t.Fatal(err)
	}
	before := quotaStatus(t, s).UsedBytes
	if before != 266+2+6 {
		t.Fatalf("retained charge %d", before)
	}
	if _, err := s.SetStorageLimit(ctx, before+1); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveExchange(ctx, &Exchange{Note: strings.Repeat("x", 100)}); !errors.Is(err, ErrCaptureQuotaExceeded) {
		t.Fatal(err)
	}
	if err := s.SaveExchange(ctx, &Exchange{}); !errors.Is(err, ErrCaptureQuotaExceeded) {
		t.Fatal(err)
	}
	if err := s.UpdateExchangeMetadata(ctx, e.ID, nil, "longer"); !errors.Is(err, ErrCaptureQuotaExceeded) {
		t.Fatal(err)
	}
	if err := s.UpdateExchangeMetadata(ctx, e.ID, nil, ""); err != nil {
		t.Fatal(err)
	}
	if status := quotaStatus(t, s); status.UsedBytes != before-2 || !status.Paused {
		t.Fatal(status)
	}
	if _, err := s.SetStorageLimit(ctx, before); err != nil {
		t.Fatal(err)
	}
	if !quotaStatus(t, s).Paused {
		t.Fatal("lower limit cleared latch")
	}
	if _, err := s.SetStorageLimit(ctx, 10000); err != nil {
		t.Fatal(err)
	}
	before = quotaStatus(t, s).UsedBytes
	if _, err := s.db.Exec(`CREATE TRIGGER fail_body BEFORE INSERT ON exchange_bodies BEGIN SELECT RAISE(ABORT, 'test'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveExchange(ctx, &Exchange{}); err == nil || errors.Is(err, ErrCaptureQuotaExceeded) {
		t.Fatal(err)
	}
	if got := quotaStatus(t, s).UsedBytes; got != before {
		t.Fatalf("reservation leaked %d -> %d", before, got)
	}
}

func TestQuotaConcurrentHandlesAndRepeater(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "quota.db")
	a, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if _, err := a.SetStorageLimit(ctx, 266*5); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := a
			if i%2 == 0 {
				s = b
			}
			results <- s.SaveExchange(ctx, &Exchange{})
		}(i)
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
	if saved != 5 {
		t.Fatalf("saved %d", saved)
	}
	if status := quotaStatus(t, a); status.UsedBytes != 266*5 || status.SkippedRecords != 15 || !status.Paused {
		t.Fatal(status)
	}
	if _, err := b.SetStorageLimit(ctx, 10000); err != nil {
		t.Fatal(err)
	}
	send := &RepeaterSend{SessionID: "s", RequestBody: "é", ResponseBody: "abc"}
	before := quotaStatus(t, a).UsedBytes
	if err := b.SaveRepeaterSend(ctx, send); err != nil {
		t.Fatal(err)
	}
	if got := quotaStatus(t, a).UsedBytes - before; got != 256+1+4+4+2+3 {
		t.Fatalf("repeater charge %d", got)
	}
	if _, err := a.SetStorageLimit(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := b.SaveRepeaterSend(ctx, &RepeaterSend{SessionID: "new"}); !errors.Is(err, ErrCaptureQuotaExceeded) {
		t.Fatal(err)
	}
	sessions, err := a.ListRepeaterSessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions %v %v", sessions, err)
	}
}
