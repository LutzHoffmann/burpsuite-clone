package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrActiveCheckLimit = errors.New("active check history limit reached")

type ActiveCheckObservation struct {
	URL       string `json:"url"`
	Source    string `json:"source"`
	Parameter string `json:"parameter"`
	Status    int    `json:"status"`
	Found     bool   `json:"found"`
	Context   string `json:"context"`
	Partial   bool   `json:"partial"`
	Error     string `json:"error,omitempty"`
}
type ActiveCheckRun struct {
	ID               int64                    `json:"id"`
	CrawlID          int64                    `json:"crawlId"`
	State            string                   `json:"state"`
	Reason           string                   `json:"reason,omitempty"`
	ObservationCount int                      `json:"observationCount"`
	StartedAt        time.Time                `json:"startedAt"`
	FinishedAt       *time.Time               `json:"finishedAt"`
	Observations     []ActiveCheckObservation `json:"observations,omitempty"`
}
type ActiveCheckStore interface {
	CreateActiveCheckRun(context.Context, int64) (int64, error)
	AppendActiveCheck(context.Context, int64, ActiveCheckObservation) error
	FinishActiveCheckRun(context.Context, int64, string, string) error
	RecoverActiveCheckRuns(context.Context) error
	ListActiveCheckRuns(context.Context) ([]ActiveCheckRun, error)
	GetActiveCheckRun(context.Context, int64) (ActiveCheckRun, error)
	DeleteActiveCheckRun(context.Context, int64) error
}

func (s *SQLiteStore) CreateActiveCheckRun(ctx context.Context, crawlID int64) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM active_check_runs WHERE project_id=1`).Scan(&count); err != nil {
		return 0, err
	}
	if count >= 10000 {
		return 0, ErrActiveCheckLimit
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO active_check_runs(project_id,crawl_id,state,started_at_unix_nano) SELECT 1,id,'running',? FROM crawl_runs WHERE id=? AND project_id=1 AND state='completed'`, time.Now().UTC().UnixNano(), crawlID)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n != 1 {
		return 0, sql.ErrNoRows
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}
func (s *SQLiteStore) AppendActiveCheck(ctx context.Context, id int64, item ActiveCheckObservation) error {
	if len(item.URL) < 1 || len(item.URL) > 4096 || (item.Source != "query" && item.Source != "get_form" && item.Source != "redirect_query" && item.Source != "redirect_get_form" && item.Source != "cors") || len(item.Parameter) < 1 || len(item.Parameter) > 256 || item.Status < 0 || item.Status > 999 || len(item.Error) > 64 {
		return fmt.Errorf("invalid active check observation")
	}
	switch item.Context {
	case "unknown", "plain_text", "html_text", "html_attribute", "raw_text", "redirect_location", "cors_credentials":
	default:
		return fmt.Errorf("invalid active check context")
	}
	redacted := withoutQuery(item.URL)
	if redacted == "" {
		return fmt.Errorf("invalid observation URL")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE active_check_runs SET observation_count=observation_count+1 WHERE id=? AND project_id=1 AND state='running' AND observation_count<100`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("active check limit or state conflict")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO active_check_observations(run_id,url,source,parameter,status,found,context,partial,error) VALUES(?,?,?,?,?,?,?,?,?)`, id, redacted, item.Source, item.Parameter, item.Status, item.Found, item.Context, item.Partial, item.Error)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *SQLiteStore) FinishActiveCheckRun(ctx context.Context, id int64, state, reason string) error {
	if state != "completed" && state != "cancelled" && state != "scope_revoked" && state != "failed" {
		return fmt.Errorf("invalid active check state")
	}
	if len(reason) > 64 {
		return fmt.Errorf("invalid active check reason")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE active_check_runs SET state=?,reason=?,finished_at_unix_nano=? WHERE id=? AND project_id=1 AND state='running'`, state, reason, time.Now().UTC().UnixNano(), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return sql.ErrNoRows
	}
	return nil
}
func (s *SQLiteStore) RecoverActiveCheckRuns(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE active_check_runs SET state='interrupted',reason='application_restarted',finished_at_unix_nano=? WHERE project_id=1 AND state='running'`, time.Now().UTC().UnixNano())
	return err
}

const activeCheckSelect = `SELECT id,crawl_id,state,reason,observation_count,started_at_unix_nano,finished_at_unix_nano FROM active_check_runs WHERE project_id=1`

func scanActiveCheckRun(row interface{ Scan(...any) error }) (ActiveCheckRun, error) {
	var run ActiveCheckRun
	var started int64
	var finished sql.NullInt64
	err := row.Scan(&run.ID, &run.CrawlID, &run.State, &run.Reason, &run.ObservationCount, &started, &finished)
	if err != nil {
		return run, err
	}
	run.StartedAt = time.Unix(0, started).UTC()
	if finished.Valid {
		v := time.Unix(0, finished.Int64).UTC()
		run.FinishedAt = &v
	}
	return run, nil
}
func (s *SQLiteStore) ListActiveCheckRuns(ctx context.Context) ([]ActiveCheckRun, error) {
	rows, err := s.db.QueryContext(ctx, activeCheckSelect+` ORDER BY id DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []ActiveCheckRun{}
	for rows.Next() {
		run, err := scanActiveCheckRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}
func (s *SQLiteStore) GetActiveCheckRun(ctx context.Context, id int64) (ActiveCheckRun, error) {
	run, err := scanActiveCheckRun(s.db.QueryRowContext(ctx, activeCheckSelect+` AND id=?`, id))
	if err != nil {
		return run, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT url,source,parameter,status,found,context,partial,error FROM active_check_observations WHERE run_id=? ORDER BY rowid`, id)
	if err != nil {
		return run, err
	}
	defer rows.Close()
	run.Observations = []ActiveCheckObservation{}
	for rows.Next() {
		var item ActiveCheckObservation
		if err := rows.Scan(&item.URL, &item.Source, &item.Parameter, &item.Status, &item.Found, &item.Context, &item.Partial, &item.Error); err != nil {
			return run, err
		}
		run.Observations = append(run.Observations, item)
	}
	return run, rows.Err()
}
func (s *SQLiteStore) DeleteActiveCheckRun(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM active_check_runs WHERE id=? AND project_id=1 AND state!='running'`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return sql.ErrNoRows
	}
	return nil
}
