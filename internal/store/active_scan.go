package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrActiveScanLimit = errors.New("active scan history limit reached")

type ActiveScanProbe struct {
	Parameter string `json:"parameter"`
	Status    int    `json:"status"`
	Reflected bool   `json:"reflected"`
	Partial   bool   `json:"partial"`
	Error     string `json:"error,omitempty"`
}

type ActiveScanRun struct {
	ID             int64             `json:"id"`
	HistoryID      int64             `json:"historyId"`
	Host           string            `json:"host"`
	Path           string            `json:"path"`
	State          string            `json:"state"`
	StoppedReason  string            `json:"stoppedReason,omitempty"`
	ProbeCount     int               `json:"probeCount"`
	ReflectedCount int               `json:"reflectedCount"`
	StartedAt      time.Time         `json:"startedAt"`
	FinishedAt     *time.Time        `json:"finishedAt"`
	Probes         []ActiveScanProbe `json:"probes,omitempty"`
}

type ActiveScanRunStore interface {
	CreateActiveScanRun(context.Context, int64) (int64, error)
	AppendActiveScanProbe(context.Context, int64, int, ActiveScanProbe) error
	FinishActiveScanRun(context.Context, int64, string, string) error
	ListActiveScanRuns(context.Context) ([]ActiveScanRun, error)
	GetActiveScanRun(context.Context, int64) (ActiveScanRun, error)
	DeleteActiveScanRun(context.Context, int64) error
	RecoverActiveScanRuns(context.Context) error
}

func (s *SQLiteStore) CreateActiveScanRun(ctx context.Context, historyID int64) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM active_scan_runs WHERE project_id=1`).Scan(&count); err != nil {
		return 0, err
	}
	if count >= 10000 {
		return 0, ErrActiveScanLimit
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO active_scan_runs(project_id,exchange_id,state,started_at_unix_nano) SELECT 1,id,'running',? FROM exchanges WHERE id=? AND project_id=1`, time.Now().UTC().UnixNano(), historyID)
	if err != nil {
		return 0, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if changed != 1 {
		return 0, sql.ErrNoRows
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *SQLiteStore) AppendActiveScanProbe(ctx context.Context, id int64, sequence int, probe ActiveScanProbe) error {
	if sequence < 0 || sequence >= 5 || len(probe.Parameter) == 0 || len(probe.Parameter) > 256 || len(probe.Error) > 64 || probe.Status < 0 || probe.Status > 999 {
		return fmt.Errorf("invalid scan probe")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO active_scan_probes(run_id,sequence,parameter,status,reflected,partial,error) SELECT id,?,?,?,?,?,? FROM active_scan_runs WHERE id=? AND project_id=1 AND state='running' AND probe_count=?`, sequence, probe.Parameter, probe.Status, probe.Reflected, probe.Partial, probe.Error, id, sequence)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("active scan sequence conflict")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE active_scan_runs SET probe_count=probe_count+1 WHERE id=? AND project_id=1 AND state='running'`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) FinishActiveScanRun(ctx context.Context, id int64, state, reason string) error {
	if state != "completed" && state != "scope_revoked" && state != "cancelled" && state != "failed" {
		return fmt.Errorf("invalid scan state")
	}
	if len(reason) > 64 {
		return fmt.Errorf("invalid scan reason")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE active_scan_runs SET state=?,stopped_reason=?,finished_at_unix_nano=? WHERE id=? AND project_id=1 AND state='running'`, state, reason, time.Now().UTC().UnixNano(), id)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SQLiteStore) RecoverActiveScanRuns(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE active_scan_runs SET state='interrupted',stopped_reason='application_restarted',finished_at_unix_nano=? WHERE project_id=1 AND state='running'`, time.Now().UTC().UnixNano())
	return err
}

const activeScanRunSelect = `SELECT r.id,r.exchange_id,e.host,e.path,r.state,r.stopped_reason,r.probe_count,COALESCE((SELECT SUM(p.reflected) FROM active_scan_probes p WHERE p.run_id=r.id),0),r.started_at_unix_nano,r.finished_at_unix_nano FROM active_scan_runs r JOIN exchanges e ON e.id=r.exchange_id WHERE r.project_id=1`

func scanActiveRun(row interface{ Scan(...any) error }) (ActiveScanRun, error) {
	var run ActiveScanRun
	var started int64
	var finished sql.NullInt64
	err := row.Scan(&run.ID, &run.HistoryID, &run.Host, &run.Path, &run.State, &run.StoppedReason, &run.ProbeCount, &run.ReflectedCount, &started, &finished)
	if err != nil {
		return run, err
	}
	run.StartedAt = time.Unix(0, started).UTC()
	if finished.Valid {
		value := time.Unix(0, finished.Int64).UTC()
		run.FinishedAt = &value
	}
	return run, nil
}

func (s *SQLiteStore) ListActiveScanRuns(ctx context.Context) ([]ActiveScanRun, error) {
	rows, err := s.db.QueryContext(ctx, activeScanRunSelect+` ORDER BY r.id DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []ActiveScanRun{}
	for rows.Next() {
		run, err := scanActiveRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *SQLiteStore) GetActiveScanRun(ctx context.Context, id int64) (ActiveScanRun, error) {
	run, err := scanActiveRun(s.db.QueryRowContext(ctx, activeScanRunSelect+` AND r.id=?`, id))
	if err != nil {
		return run, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT parameter,status,reflected,partial,error FROM active_scan_probes WHERE run_id=? ORDER BY sequence`, id)
	if err != nil {
		return run, err
	}
	defer rows.Close()
	run.Probes = []ActiveScanProbe{}
	for rows.Next() {
		var probe ActiveScanProbe
		if err := rows.Scan(&probe.Parameter, &probe.Status, &probe.Reflected, &probe.Partial, &probe.Error); err != nil {
			return run, err
		}
		run.Probes = append(run.Probes, probe)
	}
	return run, rows.Err()
}

func (s *SQLiteStore) DeleteActiveScanRun(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM active_scan_runs WHERE id=? AND project_id=1 AND state!='running'`, id)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return sql.ErrNoRows
	}
	return nil
}
