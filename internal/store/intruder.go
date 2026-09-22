package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/intruder"
)

var _ intruder.Store = (*SQLiteStore)(nil)

func (s *SQLiteStore) CreateDraft(ctx context.Context, draft intruder.Draft) (intruder.Job, error) {
	total, err := intruder.ValidateConfig(draft.Config)
	if err != nil {
		return intruder.Job{}, err
	}
	if draft.ID == "" {
		return intruder.Job{}, errors.New("intruder job id is required")
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return intruder.Job{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO intruder_jobs (id,project_id,attack,state,state_reason,revision,method,url,template_raw,request_limit,concurrency,rate_micros,timeout_ms,total_requests,next_sequence,completed_count,error_count,scope_version,created_at_unix_nano,updated_at_unix_nano) VALUES (?,1,?,'draft','',1,?,?,?,?,?,?,?,?,0,0,0,?,?,?)`,
		draft.ID, draft.Config.Attack, draft.Config.Template.Method, draft.Config.Template.URL, draft.Config.Template.Raw, draft.Config.RequestLimit, draft.Config.Concurrency, rateMicros(draft.Config.RatePerSecond), draft.Config.Timeout.Milliseconds(), total, draft.ScopeVersion, now.UnixNano(), now.UnixNano())
	if err != nil {
		return intruder.Job{}, fmt.Errorf("insert intruder job: %w", err)
	}
	if err := writeIntruderConfig(ctx, tx, draft.ID, draft.Config); err != nil {
		return intruder.Job{}, err
	}
	if err := tx.Commit(); err != nil {
		return intruder.Job{}, err
	}
	return s.GetJob(ctx, draft.ID)
}

func writeIntruderConfig(ctx context.Context, tx *sql.Tx, jobID string, cfg intruder.Config) error {
	for i, set := range cfg.PayloadSets {
		if _, err := tx.ExecContext(ctx, `INSERT INTO intruder_payload_sets(job_id,id,set_order) VALUES (?,?,?)`, jobID, set.ID, i); err != nil {
			return err
		}
		for j, payload := range set.Payloads {
			if _, err := tx.ExecContext(ctx, `INSERT INTO intruder_payloads(job_id,set_id,payload_index,payload) VALUES (?,?,?,?)`, jobID, set.ID, j, payload); err != nil {
				return err
			}
		}
	}
	for i, position := range cfg.Positions {
		if _, err := tx.ExecContext(ctx, `INSERT INTO intruder_positions(job_id,id,position_order,start_offset,end_offset,payload_set_id) VALUES (?,?,?,?,?,?)`, jobID, position.ID, i, position.Start, position.End, position.PayloadSetID); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) ReplaceDraft(ctx context.Context, id string, revision int64, cfg intruder.Config) (intruder.Job, error) {
	total, err := intruder.ValidateConfig(cfg)
	if err != nil {
		return intruder.Job{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return intruder.Job{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE intruder_jobs SET attack=?,method=?,url=?,template_raw=?,request_limit=?,concurrency=?,rate_micros=?,timeout_ms=?,total_requests=?,revision=revision+1,updated_at_unix_nano=? WHERE id=? AND project_id=1 AND state='draft' AND revision=?`, cfg.Attack, cfg.Template.Method, cfg.Template.URL, cfg.Template.Raw, cfg.RequestLimit, cfg.Concurrency, rateMicros(cfg.RatePerSecond), cfg.Timeout.Milliseconds(), total, time.Now().UnixNano(), id, revision)
	if err != nil {
		return intruder.Job{}, err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return intruder.Job{}, s.intruderConflict(ctx, tx, id, revision, intruder.StateDraft)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM intruder_positions WHERE job_id=?`, id); err != nil {
		return intruder.Job{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM intruder_payload_sets WHERE job_id=?`, id); err != nil {
		return intruder.Job{}, err
	}
	if err := writeIntruderConfig(ctx, tx, id, cfg); err != nil {
		return intruder.Job{}, err
	}
	if err := tx.Commit(); err != nil {
		return intruder.Job{}, err
	}
	return s.GetJob(ctx, id)
}

func (s *SQLiteStore) intruderConflict(ctx context.Context, tx *sql.Tx, id string, revision int64, state intruder.State) error {
	var gotRevision int64
	var gotState string
	err := tx.QueryRowContext(ctx, `SELECT revision,state FROM intruder_jobs WHERE id=? AND project_id=1`, id).Scan(&gotRevision, &gotState)
	if err != nil {
		return err
	}
	if gotRevision != revision {
		return intruder.ErrRevisionConflict
	}
	if gotState != string(state) {
		return intruder.ErrStateConflict
	}
	return intruder.ErrStateConflict
}

func (s *SQLiteStore) ListJobs(ctx context.Context) ([]intruder.JobSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,attack,state,state_reason,revision,total_requests,completed_count,error_count,updated_at_unix_nano FROM intruder_jobs WHERE project_id=1 ORDER BY updated_at_unix_nano DESC,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []intruder.JobSummary{}
	for rows.Next() {
		var j intruder.JobSummary
		var updated int64
		if err := rows.Scan(&j.ID, &j.Attack, &j.State, &j.StateReason, &j.Revision, &j.TotalRequests, &j.CompletedCount, &j.ErrorCount, &updated); err != nil {
			return nil, err
		}
		j.UpdatedAt = time.Unix(0, updated).UTC()
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) GetJob(ctx context.Context, id string) (intruder.Job, error) {
	var j intruder.Job
	var rate int64
	var timeoutMS, created, updated int64
	err := s.db.QueryRowContext(ctx, `SELECT id,attack,state,state_reason,revision,method,url,template_raw,request_limit,concurrency,rate_micros,timeout_ms,total_requests,next_sequence,completed_count,error_count,scope_version,baseline_sequence,created_at_unix_nano,updated_at_unix_nano FROM intruder_jobs WHERE id=? AND project_id=1`, id).Scan(&j.ID, &j.Config.Attack, &j.State, &j.StateReason, &j.Revision, &j.Config.Template.Method, &j.Config.Template.URL, &j.Config.Template.Raw, &j.Config.RequestLimit, &j.Config.Concurrency, &rate, &timeoutMS, &j.TotalRequests, &j.NextSequence, &j.CompletedCount, &j.ErrorCount, &j.ScopeVersion, &j.BaselineSequence, &created, &updated)
	if err != nil {
		return intruder.Job{}, err
	}
	j.Config.RatePerSecond = float64(rate) / 1_000_000
	j.Config.Timeout = time.Duration(timeoutMS) * time.Millisecond
	j.CreatedAt = time.Unix(0, created).UTC()
	j.UpdatedAt = time.Unix(0, updated).UTC()
	setRows, err := s.db.QueryContext(ctx, `SELECT id FROM intruder_payload_sets WHERE job_id=? ORDER BY set_order`, id)
	if err != nil {
		return intruder.Job{}, err
	}
	for setRows.Next() {
		var set intruder.PayloadSet
		if err := setRows.Scan(&set.ID); err != nil {
			setRows.Close()
			return intruder.Job{}, err
		}
		payloadRows, e := s.db.QueryContext(ctx, `SELECT payload FROM intruder_payloads WHERE job_id=? AND set_id=? ORDER BY payload_index`, id, set.ID)
		if e != nil {
			setRows.Close()
			return intruder.Job{}, e
		}
		for payloadRows.Next() {
			var p []byte
			if e = payloadRows.Scan(&p); e != nil {
				payloadRows.Close()
				setRows.Close()
				return intruder.Job{}, e
			}
			set.Payloads = append(set.Payloads, p)
		}
		payloadRows.Close()
		j.Config.PayloadSets = append(j.Config.PayloadSets, set)
	}
	setRows.Close()
	rows, err := s.db.QueryContext(ctx, `SELECT id,start_offset,end_offset,payload_set_id FROM intruder_positions WHERE job_id=? ORDER BY position_order`, id)
	if err != nil {
		return intruder.Job{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var p intruder.Position
		if err := rows.Scan(&p.ID, &p.Start, &p.End, &p.PayloadSetID); err != nil {
			return intruder.Job{}, err
		}
		j.Config.Positions = append(j.Config.Positions, p)
	}
	return j, rows.Err()
}

func (s *SQLiteStore) Transition(ctx context.Context, id string, revision int64, from, to intruder.State, reason string) (intruder.Job, error) {
	if !validIntruderTransition(from, to) {
		return intruder.Job{}, intruder.ErrStateConflict
	}
	result, err := s.db.ExecContext(ctx, `UPDATE intruder_jobs SET state=?,state_reason=?,revision=revision+1,updated_at_unix_nano=? WHERE id=? AND project_id=1 AND revision=? AND state=?`, to, reason, time.Now().UnixNano(), id, revision, from)
	if err != nil {
		return intruder.Job{}, err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		tx, e := s.db.BeginTx(ctx, nil)
		if e != nil {
			return intruder.Job{}, e
		}
		defer tx.Rollback()
		return intruder.Job{}, s.intruderConflict(ctx, tx, id, revision, from)
	}
	return s.GetJob(ctx, id)
}

func validIntruderTransition(from, to intruder.State) bool {
	switch from {
	case intruder.StateDraft:
		return to == intruder.StateRunning
	case intruder.StateRunning:
		return to == intruder.StatePausing || to == intruder.StateAborting || to == intruder.StateCompleted || to == intruder.StateFailed
	case intruder.StatePausing:
		return to == intruder.StatePaused || to == intruder.StateAborting || to == intruder.StateFailed
	case intruder.StatePaused:
		return to == intruder.StateRunning || to == intruder.StateAborted
	case intruder.StateAborting:
		return to == intruder.StateAborted || to == intruder.StateFailed
	}
	return false
}

func (s *SQLiteStore) AppendResult(ctx context.Context, id string, r intruder.Result) (intruder.Job, error) {
	var state intruder.State
	if err := s.db.QueryRowContext(ctx, `SELECT state FROM intruder_jobs WHERE id=? AND project_id=1`, id).Scan(&state); err != nil {
		return intruder.Job{}, err
	}
	if state != intruder.StateRunning && state != intruder.StatePausing && state != intruder.StateAborting {
		return intruder.Job{}, intruder.ErrStateConflict
	}
	selections, _ := json.Marshal(r.Selections)
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	charge := CaptureRecordAllowance + int64(len(selections)+len(r.Method)+len(r.URL)+len(r.MIMEType)+len(r.ErrorCategory)+len(r.RequestCapture)+len(r.ResponseCapture))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return intruder.Job{}, err
	}
	defer tx.Rollback()
	stored := r.BodyStored
	if err = reserveCapture(ctx, tx, charge); err != nil {
		if !errors.Is(err, ErrCaptureQuotaExceeded) {
			return intruder.Job{}, err
		}
		tx, err = s.db.BeginTx(ctx, nil)
		if err != nil {
			return intruder.Job{}, err
		}
		defer tx.Rollback()
		r.RequestCapture = nil
		r.ResponseCapture = nil
		r.StorageStatus = "quota_exceeded"
		stored = false
		charge = 0
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO intruder_results(job_id,sequence,selections_json,method,url,status,mime_type,request_size,response_size,duration_ms,error_category,response_truncated,request_capture,response_capture,body_stored,storage_status,similarity,similarity_partial,status_diff,length_delta,duration_delta,mime_diff,capture_bytes,created_at_unix_nano) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, r.Sequence, string(selections), r.Method, r.URL, r.Status, r.MIMEType, r.RequestSize, r.ResponseSize, r.Duration.Milliseconds(), r.ErrorCategory, r.ResponseTruncated, r.RequestCapture, r.ResponseCapture, stored, r.StorageStatus, r.Similarity, r.SimilarityPartial, r.StatusDiff, r.LengthDelta, r.DurationDelta, r.MIMEDiff, charge, r.CreatedAt.UnixNano())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return intruder.Job{}, intruder.ErrResultExists
		}
		return intruder.Job{}, err
	}
	errFlag := 0
	if r.ErrorCategory != "" {
		errFlag = 1
	}
	progress, err := tx.ExecContext(ctx, `UPDATE intruder_jobs SET next_sequence=next_sequence+1,completed_count=completed_count+1,error_count=error_count+?,revision=revision+1,updated_at_unix_nano=? WHERE id=? AND project_id=1 AND state IN ('running','pausing','aborting') AND next_sequence=? AND next_sequence<total_requests`, errFlag, time.Now().UnixNano(), id, r.Sequence)
	if err != nil {
		return intruder.Job{}, err
	}
	changed, err := progress.RowsAffected()
	if err != nil {
		return intruder.Job{}, err
	}
	if changed != 1 {
		return intruder.Job{}, intruder.ErrSequenceConflict
	}
	if err = tx.Commit(); err != nil {
		return intruder.Job{}, err
	}
	return s.GetJob(ctx, id)
}

func (s *SQLiteStore) ListResults(ctx context.Context, id string, q intruder.ResultQuery) (intruder.ResultPage, error) {
	var visibleID string
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM intruder_jobs WHERE id=? AND project_id=1`, id).Scan(&visibleID); err != nil {
		return intruder.ResultPage{}, err
	}
	limit := q.Limit
	if limit < 1 || limit > 100 {
		limit = 100
	}
	query := `SELECT sequence,selections_json,method,url,status,mime_type,request_size,response_size,duration_ms,error_category,response_truncated,body_stored,storage_status,similarity,similarity_partial,status_diff,length_delta,duration_delta,mime_diff,created_at_unix_nano FROM intruder_results WHERE job_id=?`
	args := []any{id}
	if q.Status != 0 {
		query += ` AND status=?`
		args = append(args, q.Status)
	}
	if q.BeforeSequence != nil {
		query += ` AND sequence<?`
		args = append(args, *q.BeforeSequence)
	}
	if q.ErrorCategory != "" {
		query += ` AND error_category=?`
		args = append(args, q.ErrorCategory)
	}
	if q.MIMEType != "" {
		query += ` AND mime_type=?`
		args = append(args, q.MIMEType)
	}
	if q.MinSize > 0 {
		query += ` AND response_size>=?`
		args = append(args, q.MinSize)
	}
	if q.MaxSize > 0 {
		query += ` AND response_size<=?`
		args = append(args, q.MaxSize)
	}
	if q.MinDurationMS > 0 {
		query += ` AND duration_ms>=?`
		args = append(args, q.MinDurationMS)
	}
	if q.MaxDurationMS > 0 {
		query += ` AND duration_ms<=?`
		args = append(args, q.MaxDurationMS)
	}
	if q.MinSimilarity > 0 {
		query += ` AND similarity>=?`
		args = append(args, q.MinSimilarity)
	}
	if q.MaxSimilarity > 0 {
		query += ` AND similarity<=?`
		args = append(args, q.MaxSimilarity)
	}
	query += ` ORDER BY sequence DESC`
	if q.PayloadSearch == "" {
		query += ` LIMIT ?`
		args = append(args, limit+1)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return intruder.ResultPage{}, err
	}
	defer rows.Close()
	page := intruder.ResultPage{}
	for rows.Next() {
		r, e := scanIntruderResult(rows, false)
		if e != nil {
			return page, e
		}
		if q.PayloadSearch != "" && !resultContainsPayload(r, q.PayloadSearch) {
			continue
		}
		page.Results = append(page.Results, r)
		if len(page.Results) > limit {
			break
		}
	}
	if len(page.Results) > limit {
		next := page.Results[limit-1].Sequence
		page.NextBeforeSequence = &next
		page.Results = page.Results[:limit]
	}
	return page, rows.Err()
}

func resultContainsPayload(result intruder.Result, search string) bool {
	for _, selection := range result.Selections {
		if strings.Contains(string(selection.Payload), search) {
			return true
		}
	}
	return false
}

func (s *SQLiteStore) GetResult(ctx context.Context, id string, sequence int64) (intruder.Result, error) {
	row := s.db.QueryRowContext(ctx, `SELECT r.sequence,r.selections_json,r.method,r.url,r.status,r.mime_type,r.request_size,r.response_size,r.duration_ms,r.error_category,r.response_truncated,r.body_stored,r.storage_status,r.similarity,r.similarity_partial,r.status_diff,r.length_delta,r.duration_delta,r.mime_diff,r.created_at_unix_nano,r.request_capture,r.response_capture FROM intruder_results r JOIN intruder_jobs j ON j.id=r.job_id WHERE r.job_id=? AND r.sequence=? AND j.project_id=1`, id, sequence)
	return scanIntruderResult(row, true)
}

type intruderRowScanner interface{ Scan(...any) error }

func scanIntruderResult(row intruderRowScanner, detail bool) (intruder.Result, error) {
	var r intruder.Result
	var selections string
	var duration, created int64
	args := []any{&r.Sequence, &selections, &r.Method, &r.URL, &r.Status, &r.MIMEType, &r.RequestSize, &r.ResponseSize, &duration, &r.ErrorCategory, &r.ResponseTruncated, &r.BodyStored, &r.StorageStatus, &r.Similarity, &r.SimilarityPartial, &r.StatusDiff, &r.LengthDelta, &r.DurationDelta, &r.MIMEDiff, &created}
	if detail {
		args = append(args, &r.RequestCapture, &r.ResponseCapture)
	}
	if err := row.Scan(args...); err != nil {
		return r, err
	}
	if err := json.Unmarshal([]byte(selections), &r.Selections); err != nil {
		return r, err
	}
	r.Duration = time.Duration(duration) * time.Millisecond
	r.CreatedAt = time.Unix(0, created).UTC()
	return r, nil
}

func (s *SQLiteStore) RecoverRunning(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE intruder_jobs SET state='paused',state_reason='application_restarted',revision=revision+1,updated_at_unix_nano=? WHERE project_id=1 AND state IN ('running','pausing','aborting')`, time.Now().UnixNano())
	return err
}
func (s *SQLiteStore) DeleteJob(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM intruder_jobs WHERE id=? AND project_id=1 AND state IN ('draft','aborted','completed','failed')`, id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 1 {
		return nil
	}
	var state string
	if err := s.db.QueryRowContext(ctx, `SELECT state FROM intruder_jobs WHERE id=? AND project_id=1`, id).Scan(&state); err != nil {
		return err
	}
	return intruder.ErrStateConflict
}
func rateMicros(rate float64) int64 { return int64(rate*1_000_000 + 0.5) }
