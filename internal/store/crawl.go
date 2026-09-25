package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"time"
)

var ErrCrawlLimit = errors.New("crawl history limit reached")

type CrawlField struct {
	Name string `json:"name"`
	Type string `json:"type"`
}
type CrawlForm struct {
	PageURL   string       `json:"pageUrl"`
	ActionURL string       `json:"actionUrl"`
	Method    string       `json:"method"`
	Fields    []CrawlField `json:"fields"`
}
type CrawlPage struct {
	URL         string `json:"url"`
	Depth       int    `json:"depth"`
	Status      int    `json:"status"`
	ContentType string `json:"contentType"`
	Truncated   bool   `json:"truncated"`
	Error       string `json:"error,omitempty"`
}
type CrawlRun struct {
	ID         int64       `json:"id"`
	HistoryID  int64       `json:"historyId"`
	MaxPages   int         `json:"maxPages"`
	MaxDepth   int         `json:"maxDepth"`
	State      string      `json:"state"`
	Reason     string      `json:"reason,omitempty"`
	PageCount  int         `json:"pageCount"`
	FieldCount int         `json:"fieldCount"`
	StartedAt  time.Time   `json:"startedAt"`
	FinishedAt *time.Time  `json:"finishedAt"`
	Pages      []CrawlPage `json:"pages,omitempty"`
	Forms      []CrawlForm `json:"forms,omitempty"`
}

type CrawlStore interface {
	CreateCrawlRun(context.Context, int64, int, int) (int64, error)
	AppendCrawlPage(context.Context, int64, CrawlPage, []CrawlForm) error
	FinishCrawlRun(context.Context, int64, string, string) error
	RecoverCrawlRuns(context.Context) error
	ListCrawlRuns(context.Context) ([]CrawlRun, error)
	GetCrawlRun(context.Context, int64) (CrawlRun, error)
	DeleteCrawlRun(context.Context, int64) error
}

func withoutQuery(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	return u.String()
}

func (s *SQLiteStore) CreateCrawlRun(ctx context.Context, historyID int64, maxPages, maxDepth int) (int64, error) {
	if maxPages < 1 || maxPages > 25 || maxDepth < 0 || maxDepth > 3 {
		return 0, fmt.Errorf("invalid crawl limits")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM crawl_runs WHERE project_id=1`).Scan(&count); err != nil {
		return 0, err
	}
	if count >= 10000 {
		return 0, ErrCrawlLimit
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO crawl_runs(project_id,exchange_id,max_pages,max_depth,state,started_at_unix_nano) SELECT 1,id,?,?,'running',? FROM exchanges WHERE id=? AND project_id=1`, maxPages, maxDepth, time.Now().UTC().UnixNano(), historyID)
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

func (s *SQLiteStore) AppendCrawlPage(ctx context.Context, runID int64, page CrawlPage, forms []CrawlForm) error {
	if len(page.URL) < 1 || len(page.URL) > 4096 || page.Depth < 0 || page.Depth > 3 || page.Status < 0 || page.Status > 999 || len(page.ContentType) > 128 || len(page.Error) > 64 {
		return fmt.Errorf("invalid crawl page")
	}
	fieldCount := 0
	for _, form := range forms {
		if len(form.PageURL) < 1 || len(form.PageURL) > 4096 || len(form.ActionURL) < 1 || len(form.ActionURL) > 4096 || len(form.Method) < 1 || len(form.Method) > 16 {
			return fmt.Errorf("invalid crawl form")
		}
		for _, field := range form.Fields {
			if len(field.Name) < 1 || len(field.Name) > 256 || len(field.Type) < 1 || len(field.Type) > 64 {
				return fmt.Errorf("invalid crawl field")
			}
		}
		fieldCount += len(form.Fields)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE crawl_runs SET page_count=page_count+1,field_count=field_count+? WHERE id=? AND project_id=1 AND state='running' AND page_count<max_pages AND field_count+?<=100`, fieldCount, runID, fieldCount)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("crawl page limit or state conflict")
	}
	urlHash := fmt.Sprintf("%x", sha256.Sum256([]byte(page.URL)))
	_, err = tx.ExecContext(ctx, `INSERT INTO crawl_pages(run_id,url_hash,url,depth,status,content_type,truncated,error) VALUES(?,?,?,?,?,?,?,?)`, runID, urlHash, withoutQuery(page.URL), page.Depth, page.Status, page.ContentType, page.Truncated, page.Error)
	if err != nil {
		return err
	}
	for _, form := range forms {
		res, err := tx.ExecContext(ctx, `INSERT INTO crawl_forms(run_id,page_url,action_url,method) VALUES(?,?,?,?)`, runID, withoutQuery(form.PageURL), withoutQuery(form.ActionURL), form.Method)
		if err != nil {
			return err
		}
		formID, err := res.LastInsertId()
		if err != nil {
			return err
		}
		for i, field := range form.Fields {
			if _, err := tx.ExecContext(ctx, `INSERT INTO crawl_fields(form_id,sequence,name,type) VALUES(?,?,?,?)`, formID, i, field.Name, field.Type); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *SQLiteStore) FinishCrawlRun(ctx context.Context, id int64, state, reason string) error {
	if state != "completed" && state != "cancelled" && state != "scope_revoked" && state != "failed" {
		return fmt.Errorf("invalid crawl state")
	}
	if len(reason) > 64 {
		return fmt.Errorf("invalid crawl reason")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE crawl_runs SET state=?,reason=?,finished_at_unix_nano=? WHERE id=? AND project_id=1 AND state='running'`, state, reason, time.Now().UTC().UnixNano(), id)
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

func (s *SQLiteStore) RecoverCrawlRuns(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE crawl_runs SET state='interrupted',reason='application_restarted',finished_at_unix_nano=? WHERE project_id=1 AND state='running'`, time.Now().UTC().UnixNano())
	return err
}

const crawlSelect = `SELECT id,exchange_id,max_pages,max_depth,state,reason,page_count,field_count,started_at_unix_nano,finished_at_unix_nano FROM crawl_runs WHERE project_id=1`

func scanCrawlRun(row interface{ Scan(...any) error }) (CrawlRun, error) {
	var run CrawlRun
	var started int64
	var finished sql.NullInt64
	err := row.Scan(&run.ID, &run.HistoryID, &run.MaxPages, &run.MaxDepth, &run.State, &run.Reason, &run.PageCount, &run.FieldCount, &started, &finished)
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
func (s *SQLiteStore) ListCrawlRuns(ctx context.Context) ([]CrawlRun, error) {
	rows, err := s.db.QueryContext(ctx, crawlSelect+` ORDER BY id DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []CrawlRun{}
	for rows.Next() {
		run, err := scanCrawlRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}
func (s *SQLiteStore) GetCrawlRun(ctx context.Context, id int64) (CrawlRun, error) {
	run, err := scanCrawlRun(s.db.QueryRowContext(ctx, crawlSelect+` AND id=?`, id))
	if err != nil {
		return run, err
	}
	pages, err := s.db.QueryContext(ctx, `SELECT url,depth,status,content_type,truncated,error FROM crawl_pages WHERE run_id=? ORDER BY rowid`, id)
	if err != nil {
		return run, err
	}
	run.Pages = []CrawlPage{}
	for pages.Next() {
		var p CrawlPage
		if err := pages.Scan(&p.URL, &p.Depth, &p.Status, &p.ContentType, &p.Truncated, &p.Error); err != nil {
			pages.Close()
			return run, err
		}
		run.Pages = append(run.Pages, p)
	}
	err = pages.Err()
	pages.Close()
	if err != nil {
		return run, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT f.id,f.page_url,f.action_url,f.method,d.name,d.type FROM crawl_forms f LEFT JOIN crawl_fields d ON d.form_id=f.id WHERE f.run_id=? ORDER BY f.id,d.sequence`, id)
	if err != nil {
		return run, err
	}
	defer rows.Close()
	run.Forms = []CrawlForm{}
	var lastID int64
	for rows.Next() {
		var formID int64
		var pageURL, action, method string
		var name, typ sql.NullString
		if err := rows.Scan(&formID, &pageURL, &action, &method, &name, &typ); err != nil {
			return run, err
		}
		if formID != lastID {
			run.Forms = append(run.Forms, CrawlForm{PageURL: pageURL, ActionURL: action, Method: method, Fields: []CrawlField{}})
			lastID = formID
		}
		if name.Valid {
			last := len(run.Forms) - 1
			run.Forms[last].Fields = append(run.Forms[last].Fields, CrawlField{Name: name.String, Type: typ.String})
		}
	}
	return run, rows.Err()
}
func (s *SQLiteStore) DeleteCrawlRun(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM crawl_runs WHERE id=? AND project_id=1 AND state!='running'`, id)
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
