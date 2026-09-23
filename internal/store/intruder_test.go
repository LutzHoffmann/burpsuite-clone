package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/intruder"
)

func TestIntruderMigrationCreatesProjectOwnedSchema(t *testing.T) {
	store := openTestStore(t)
	var version int
	if err := store.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 8 {
		t.Fatalf("schema version = %d, want 8", version)
	}
	for _, table := range []string{"intruder_jobs", "intruder_positions", "intruder_payload_sets", "intruder_payloads", "intruder_results"} {
		var name string
		if err := store.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Fatalf("table %s: %v", table, err)
		}
	}
	if err := insertIntruderJob(store.db, "job-1", "sniper", "draft"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO intruder_payload_sets (job_id, id, set_order) VALUES ('job-1', 'set-1', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO intruder_payloads (job_id, set_id, payload_index, payload) VALUES ('job-1', 'set-1', 0, x'00ff')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO intruder_positions (job_id, id, position_order, start_offset, end_offset, payload_set_id) VALUES ('job-1', 'p1', 0, 1, 2, 'set-1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(intruderResultInsertSQL, "job-1", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DELETE FROM intruder_jobs WHERE id = 'job-1'`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"intruder_positions", "intruder_payload_sets", "intruder_payloads", "intruder_results"} {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("cascade %s count = %d, %v", table, count, err)
		}
	}
}

func TestIntruderMigrationPreservesExistingData(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.db.Exec(`INSERT INTO settings (key, value) VALUES ('intruder-migration', 'preserved')`); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(store.db); err != nil {
		t.Fatal(err)
	}
	var value string
	if err := store.db.QueryRow(`SELECT value FROM settings WHERE key = 'intruder-migration'`).Scan(&value); err != nil || value != "preserved" {
		t.Fatalf("existing data = %q, %v", value, err)
	}
}

func TestIntruderConstraintsRejectInvalidJobsAndDuplicateResults(t *testing.T) {
	store := openTestStore(t)
	if err := insertIntruderJob(store.db, "bad-attack", "invalid", "draft"); err == nil {
		t.Fatal("invalid attack accepted")
	}
	if err := insertIntruderJob(store.db, "bad-state", "sniper", "invalid"); err == nil {
		t.Fatal("invalid state accepted")
	}
	if err := insertIntruderJob(store.db, "job-1", "sniper", "draft"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(intruderResultInsertSQL, "job-1", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(intruderResultInsertSQL, "job-1", 0); err == nil {
		t.Fatal("duplicate job sequence accepted")
	}
	if _, err := store.db.Exec(`INSERT INTO intruder_jobs (
		id, project_id, attack, state, state_reason, revision, method, url, template_raw,
		request_limit, concurrency, rate_micros, timeout_ms, total_requests, next_sequence,
		completed_count, error_count, scope_version, created_at_unix_nano, updated_at_unix_nano
	) VALUES ('foreign-project', 999, 'sniper', 'draft', '', 1, 'GET', 'https://example.test/', x'00', 1, 1, 100000, 1000, 1, 0, 0, 0, 1, 0, 0)`); err == nil {
		t.Fatal("missing project accepted")
	}
}

func insertIntruderJob(db *sql.DB, id, attack, state string) error {
	_, err := db.Exec(`INSERT INTO intruder_jobs (
		id, project_id, attack, state, state_reason, revision, method, url, template_raw,
		request_limit, concurrency, rate_micros, timeout_ms, total_requests, next_sequence,
		completed_count, error_count, scope_version, created_at_unix_nano, updated_at_unix_nano
	) VALUES (?, 1, ?, ?, '', 1, 'GET', 'https://example.test/', x'00', 1, 1, 100000, 1000, 1, 0, 0, 0, 1, 0, 0)`, id, attack, state)
	return err
}

const intruderResultInsertSQL = `INSERT INTO intruder_results (
	job_id, sequence, selections_json, method, url, status, mime_type, request_size,
	response_size, duration_ms, error_category, response_truncated, request_capture,
	response_capture, body_stored, storage_status, similarity, similarity_partial,
	status_diff, length_delta, duration_delta, mime_diff, capture_bytes, created_at_unix_nano
) VALUES (?, ?, '[]', 'GET', 'https://example.test/', 200, 'text/plain', 0, 0, 1, '', 0, x'', x'', 1, '', 10000, 0, 0, 0, 0, 0, 0, 0)`

func intruderDraft(id string) intruder.Draft {
	cfg := intruder.Config{
		Attack:       intruder.AttackSniper,
		Template:     intruder.Template{Method: "GET", URL: "https://example.test/?q=x", Raw: []byte("GET /?q=x HTTP/1.1\r\nHost: example.test\r\n\r\n")},
		Positions:    []intruder.Position{{ID: "p1", Start: 7, End: 8, PayloadSetID: "s1"}},
		PayloadSets:  []intruder.PayloadSet{{ID: "s1", Payloads: [][]byte{[]byte("a"), []byte("b")}}},
		RequestLimit: 10, Concurrency: 1, RatePerSecond: 1, Timeout: time.Second,
	}
	return intruder.Draft{ID: id, Config: cfg, ScopeVersion: 1}
}

func runningIntruderJob(t *testing.T, s *SQLiteStore, id string) intruder.Job {
	t.Helper()
	ctx := context.Background()
	job, err := s.CreateDraft(ctx, intruderDraft(id))
	if err != nil {
		t.Fatal(err)
	}
	job, err = s.Transition(ctx, job.ID, job.Revision, intruder.StateDraft, intruder.StateRunning, "")
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func TestIntruderStoreDraftRevisionAndIsolation(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	job, err := store.CreateDraft(ctx, intruderDraft("job-store"))
	if err != nil || job.Revision != 1 || job.TotalRequests != 2 || job.State != intruder.StateDraft {
		t.Fatalf("CreateDraft = %+v, %v", job, err)
	}
	loaded, err := store.GetJob(ctx, job.ID)
	if err != nil || !bytes.Equal(loaded.Config.PayloadSets[0].Payloads[1], []byte("b")) {
		t.Fatalf("GetJob = %+v, %v", loaded, err)
	}
	replacement := loaded.Config
	replacement.PayloadSets[0].Payloads = [][]byte{[]byte("changed")}
	if _, err := store.ReplaceDraft(ctx, job.ID, 0, replacement); !errors.Is(err, intruder.ErrRevisionConflict) {
		t.Fatalf("stale replacement error = %v", err)
	}
	unchanged, _ := store.GetJob(ctx, job.ID)
	if string(unchanged.Config.PayloadSets[0].Payloads[0]) != "a" {
		t.Fatalf("stale replacement changed draft: %+v", unchanged.Config.PayloadSets)
	}
	updated, err := store.ReplaceDraft(ctx, job.ID, 1, replacement)
	if err != nil || updated.Revision != 2 || updated.TotalRequests != 1 {
		t.Fatalf("ReplaceDraft = %+v, %v", updated, err)
	}
	jobs, err := store.ListJobs(ctx)
	if err != nil || len(jobs) != 1 || jobs[0].ID != job.ID {
		t.Fatalf("ListJobs = %+v, %v", jobs, err)
	}
}

func TestIntruderStoreTransitionsRecoveryAndDeletion(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	job, err := store.CreateDraft(ctx, intruderDraft("job-state"))
	if err != nil {
		t.Fatal(err)
	}
	running, err := store.Transition(ctx, job.ID, job.Revision, intruder.StateDraft, intruder.StateRunning, "")
	if err != nil || running.State != intruder.StateRunning {
		t.Fatalf("running = %+v, %v", running, err)
	}
	if err := store.DeleteJob(ctx, job.ID); !errors.Is(err, intruder.ErrStateConflict) {
		t.Fatalf("running delete error = %v", err)
	}
	if _, err := store.Transition(ctx, job.ID, running.Revision-1, intruder.StateRunning, intruder.StatePausing, "manual"); !errors.Is(err, intruder.ErrRevisionConflict) {
		t.Fatalf("stale transition error = %v", err)
	}
	if err := store.RecoverRunning(ctx); err != nil {
		t.Fatal(err)
	}
	recovered, _ := store.GetJob(ctx, job.ID)
	if recovered.State != intruder.StatePaused || recovered.StateReason != "application_restarted" {
		t.Fatalf("recovered = %+v", recovered)
	}
	recovered, err = store.Transition(ctx, job.ID, recovered.Revision, intruder.StatePaused, intruder.StateAborted, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteJob(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetJob(ctx, job.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted job error = %v", err)
	}
}

func TestIntruderStoreResultsAreOrderedFilteredAndIdempotent(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	job := runningIntruderJob(t, store, "job-results")
	for _, result := range []intruder.Result{
		{Sequence: 0, Method: "GET", URL: "https://example.test/?q=a", Status: 200, MIMEType: "text/plain", ResponseSize: 5, Duration: 10 * time.Millisecond, ResponseCapture: []byte("alpha"), BodyStored: true, Similarity: 10000},
		{Sequence: 1, Method: "GET", URL: "https://example.test/?q=b", Status: 500, MIMEType: "application/json", ResponseSize: 9, Duration: 20 * time.Millisecond, ErrorCategory: "http", ResponseCapture: []byte(`{"x":true}`), BodyStored: true, Similarity: 5000},
	} {
		if _, err := store.AppendResult(ctx, job.ID, result); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.AppendResult(ctx, job.ID, intruder.Result{Sequence: 1, Method: "GET", URL: "https://example.test/"}); !errors.Is(err, intruder.ErrResultExists) {
		t.Fatalf("duplicate result error = %v", err)
	}
	page, err := store.ListResults(ctx, job.ID, intruder.ResultQuery{Status: 500, Limit: 1})
	if err != nil || len(page.Results) != 1 || page.Results[0].Sequence != 1 {
		t.Fatalf("filtered page = %+v, %v", page, err)
	}
	detail, err := store.GetResult(ctx, job.ID, 0)
	if err != nil || string(detail.ResponseCapture) != "alpha" {
		t.Fatalf("result detail = %+v, %v", detail, err)
	}
}

func TestIntruderResultFiltersAndCursor(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	job := runningIntruderJob(t, s, "filter-job")
	rows := []intruder.Result{
		{Sequence: 0, Method: "GET", URL: "https://example.test/?q=alpha", Status: 200, MIMEType: "text/plain", ResponseSize: 10, Duration: 5 * time.Millisecond, ResponseCapture: []byte("alpha"), BodyStored: true, Selections: []intruder.Selection{{PositionID: "p1", Payload: []byte("alpha")}}},
		{Sequence: 1, Method: "GET", URL: "https://example.test/?q=beta", Status: 500, MIMEType: "application/json", ResponseSize: 50, Duration: 25 * time.Millisecond, ResponseCapture: []byte("beta"), BodyStored: true, ErrorCategory: "network", Selections: []intruder.Selection{{PositionID: "p1", Payload: []byte("beta")}}},
	}
	for _, row := range rows {
		if _, err := s.AppendResult(ctx, job.ID, row); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name  string
		query intruder.ResultQuery
		want  int64
	}{
		{"error", intruder.ResultQuery{ErrorCategory: "network"}, 1},
		{"mime", intruder.ResultQuery{MIMEType: "application/json"}, 1},
		{"size", intruder.ResultQuery{MinSize: 40, MaxSize: 60}, 1},
		{"duration", intruder.ResultQuery{MinDurationMS: 20, MaxDurationMS: 30}, 1},
		{"similarity", intruder.ResultQuery{MaxSimilarity: 1}, 1},
		{"payload", intruder.ResultQuery{PayloadSearch: "beta"}, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page, err := s.ListResults(ctx, job.ID, tc.query)
			if err != nil || len(page.Results) != 1 || page.Results[0].Sequence != tc.want {
				t.Fatalf("page = %+v, %v", page, err)
			}
		})
	}
	page, err := s.ListResults(ctx, job.ID, intruder.ResultQuery{Limit: 1})
	if err != nil || len(page.Results) != 1 || page.Results[0].Sequence != 1 || page.NextBeforeSequence == nil {
		t.Fatalf("first page = %+v, %v", page, err)
	}
	page, err = s.ListResults(ctx, job.ID, intruder.ResultQuery{Limit: 1, BeforeSequence: page.NextBeforeSequence})
	if err != nil || len(page.Results) != 1 || page.Results[0].Sequence != 0 || page.NextBeforeSequence != nil {
		t.Fatalf("second page = %+v, %v", page, err)
	}
}

func TestIntruderBaselineSelectionRecalculatesResults(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	job := runningIntruderJob(t, s, "baseline-job")
	first := intruder.Result{Sequence: 0, Method: "GET", URL: "https://example.test/", Status: 200, MIMEType: "text/plain", ResponseSize: 5, Duration: 10 * time.Millisecond, ResponseCapture: []byte("alpha"), BodyStored: true}
	second := intruder.Result{Sequence: 1, Method: "GET", URL: "https://example.test/", Status: 500, MIMEType: "application/json", ResponseSize: 4, Duration: 20 * time.Millisecond, ResponseCapture: []byte("beta"), BodyStored: true}
	var err error
	job, err = s.AppendResult(ctx, job.ID, first)
	if err != nil {
		t.Fatal(err)
	}
	if job.BaselineSequence == nil || *job.BaselineSequence != 0 {
		t.Fatalf("default baseline = %+v", job.BaselineSequence)
	}
	job, err = s.AppendResult(ctx, job.ID, second)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.GetResult(ctx, job.ID, 1)
	if err != nil || !before.StatusDiff || !before.MIMEDiff || before.LengthDelta != -1 || before.DurationDelta != 10 {
		t.Fatalf("before = %+v, %v", before, err)
	}
	if _, err := s.SetBaseline(ctx, job.ID, job.Revision, 1); !errors.Is(err, intruder.ErrStateConflict) {
		t.Fatalf("running selection = %v", err)
	}
	job, err = s.Transition(ctx, job.ID, job.Revision, intruder.StateRunning, intruder.StateCompleted, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetBaseline(ctx, job.ID, job.Revision-1, 1); !errors.Is(err, intruder.ErrRevisionConflict) {
		t.Fatalf("stale selection = %v", err)
	}
	if _, err := s.SetBaseline(ctx, job.ID, job.Revision, 9); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing result = %v", err)
	}
	job, err = s.SetBaseline(ctx, job.ID, job.Revision, 1)
	if err != nil {
		t.Fatal(err)
	}
	if job.BaselineSequence == nil || *job.BaselineSequence != 1 {
		t.Fatalf("selected baseline = %+v", job.BaselineSequence)
	}
	after, err := s.GetResult(ctx, job.ID, 1)
	if err != nil || after.Similarity != 10000 || after.StatusDiff || after.MIMEDiff {
		t.Fatalf("selected result = %+v, %v", after, err)
	}
	other, err := s.GetResult(ctx, job.ID, 0)
	if err != nil || !other.StatusDiff || other.LengthDelta != 1 || other.DurationDelta != -10 {
		t.Fatalf("recalculated result = %+v, %v", other, err)
	}
}

func TestIntruderQuotaOmitsBodiesButRetainsMetadata(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	job := runningIntruderJob(t, s, "quota-job")
	if _, err := s.SetStorageLimit(ctx, 1); err != nil {
		t.Fatal(err)
	}
	updated, err := s.AppendResult(ctx, job.ID, intruder.Result{Sequence: 0, Method: "GET", URL: "https://example.test/", Status: 200, ResponseSize: 1024, ResponseCapture: bytes.Repeat([]byte("x"), 1024), BodyStored: true})
	if err != nil {
		t.Fatal(err)
	}
	if updated.CompletedCount != 1 || updated.NextSequence != 1 {
		t.Fatalf("job = %+v", updated)
	}
	result, err := s.GetResult(ctx, job.ID, 0)
	if err != nil || result.BodyStored || len(result.ResponseCapture) != 0 || result.StorageStatus != "quota_exceeded" || result.Status != 200 {
		t.Fatalf("result = %+v, %v", result, err)
	}
	status, err := s.StorageStatus(ctx)
	if err != nil || !status.Paused || status.UsedBytes > status.LimitBytes {
		t.Fatalf("quota = %+v, %v", status, err)
	}
}

func TestIntruderAppendResultRequiresNextSequence(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	job := runningIntruderJob(t, s, "sequence-job")
	if _, err := s.AppendResult(ctx, job.ID, intruder.Result{Sequence: 1, Method: "GET", URL: "https://example.test/"}); !errors.Is(err, intruder.ErrSequenceConflict) {
		t.Fatalf("out-of-order error = %v", err)
	}
	unchanged, err := s.GetJob(ctx, job.ID)
	if err != nil || unchanged.CompletedCount != 0 {
		t.Fatalf("job = %+v, %v", unchanged, err)
	}
}

func TestIntruderStoreIsolatesResultsByProject(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.db.Exec(`INSERT INTO projects(id,name,created_at_unix_nano) VALUES(2,'Other',0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO intruder_jobs(id,project_id,attack,state,state_reason,revision,method,url,template_raw,request_limit,concurrency,rate_micros,timeout_ms,total_requests,next_sequence,completed_count,error_count,scope_version,created_at_unix_nano,updated_at_unix_nano) VALUES('foreign-job',2,'sniper','draft','',1,'GET','https://foreign.test/',x'00',1,1,100000,1000,1,1,1,0,1,0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(intruderResultInsertSQL, "foreign-job", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetResult(ctx, "foreign-job", 0); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign detail = %v", err)
	}
	if _, err := s.ListResults(ctx, "foreign-job", intruder.ResultQuery{}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign page = %v", err)
	}
	if _, err := s.AppendResult(ctx, "foreign-job", intruder.Result{Sequence: 1, Method: "GET", URL: "https://foreign.test/"}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign append = %v", err)
	}
}

func TestIntruderStoreRejectsIllegalTransitions(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	job, err := s.CreateDraft(ctx, intruderDraft("transition-job"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, job.ID, job.Revision, intruder.StateDraft, intruder.StateCompleted, ""); !errors.Is(err, intruder.ErrStateConflict) {
		t.Fatalf("draft to complete = %v", err)
	}
	if _, err := s.AppendResult(ctx, job.ID, intruder.Result{Sequence: 0, Method: "GET", URL: "https://example.test/"}); !errors.Is(err, intruder.ErrStateConflict) {
		t.Fatalf("draft append = %v", err)
	}
}
