package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type Project struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}

type RepeaterSession struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	SendCount int64     `json:"sendCount"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type RepeaterSend struct {
	ID              int64               `json:"id"`
	SessionID       string              `json:"sessionId"`
	Method          string              `json:"method"`
	URL             string              `json:"url"`
	RequestHeaders  map[string][]string `json:"requestHeaders"`
	RequestBody     string              `json:"requestBody"`
	Status          int                 `json:"status"`
	ResponseHeaders map[string][]string `json:"responseHeaders"`
	ResponseBody    string              `json:"responseBody"`
	DurationMS      int64               `json:"durationMs"`
	Size            int64               `json:"size"`
	Truncated       bool                `json:"truncated"`
	ContentType     string              `json:"contentType"`
	SentAt          time.Time           `json:"sentAt"`
}

type ProjectStore interface {
	ActiveProject(context.Context) (Project, error)
	GetSetting(context.Context, string) (string, error)
	SetSetting(context.Context, string, string) error
	SaveRepeaterSend(context.Context, *RepeaterSend) error
	ListRepeaterSessions(context.Context) ([]RepeaterSession, error)
	ListRepeaterSends(context.Context, string) ([]RepeaterSend, error)
}

type MetadataStore interface {
	UpdateExchangeMetadata(context.Context, int64, []string, string) error
}

func (s *SQLiteStore) UpdateExchangeMetadata(ctx context.Context, id int64, tags []string, note string) error {
	tagsJSON, err := json.Marshal(tags)
	if err != nil {
		return fmt.Errorf("marshal exchange tags: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin metadata transaction: %w", err)
	}
	defer tx.Rollback()
	// Acquire the database write lock before reading the old metadata charge.
	if _, err := tx.ExecContext(ctx, `UPDATE capture_quota SET used_bytes = used_bytes WHERE project_id = 1`); err != nil {
		return fmt.Errorf("lock metadata quota: %w", err)
	}
	var oldBytes int64
	if err := tx.QueryRowContext(ctx, `SELECT length(CAST(tags_json AS BLOB)) + length(CAST(note AS BLOB)) FROM exchanges WHERE id = ? AND project_id = 1`, id).Scan(&oldBytes); err != nil {
		return fmt.Errorf("read metadata charge: %w", err)
	}
	delta := int64(len(tagsJSON)) + int64(len(note)) - oldBytes
	result, err := tx.ExecContext(ctx, `UPDATE capture_quota SET used_bytes = used_bytes + ?
		WHERE project_id = 1 AND (? <= 0 OR (paused = 0 AND ? <= limit_bytes - used_bytes))`, delta, delta, delta)
	if err != nil {
		return fmt.Errorf("reserve metadata quota: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count metadata reservation: %w", err)
	}
	if n == 0 {
		return ErrCaptureQuotaExceeded
	}
	result, err = tx.ExecContext(ctx, `UPDATE exchanges SET tags_json = ?, note = ?, capture_bytes = capture_bytes + ? WHERE id = ? AND project_id = 1`, string(tagsJSON), note, delta, id)
	if err != nil {
		return fmt.Errorf("update exchange metadata: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count updated exchange metadata: %w", err)
	}
	if updated == 0 {
		return sql.ErrNoRows
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit metadata transaction: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ActiveProject(ctx context.Context) (Project, error) {
	var project Project
	var createdAt int64
	err := s.db.QueryRowContext(ctx, `SELECT id, name, created_at_unix_nano FROM projects WHERE id = 1`).Scan(
		&project.ID, &project.Name, &createdAt,
	)
	if err != nil {
		return Project{}, fmt.Errorf("get active project: %w", err)
	}
	project.CreatedAt = time.Unix(0, createdAt).UTC()
	return project, nil
}

func (s *SQLiteStore) GetSetting(ctx context.Context, key string) (string, error) {
	var value string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value); err != nil {
		if err == sql.ErrNoRows {
			return "", err
		}
		return "", fmt.Errorf("get setting: %w", err)
	}
	return value, nil
}

func (s *SQLiteStore) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("set setting: %w", err)
	}
	return nil
}

func (s *SQLiteStore) SaveRepeaterSend(ctx context.Context, send *RepeaterSend) error {
	requestHeaders, err := json.Marshal(send.RequestHeaders)
	if err != nil {
		return fmt.Errorf("marshal repeater request headers: %w", err)
	}
	responseHeaders, err := json.Marshal(send.ResponseHeaders)
	if err != nil {
		return fmt.Errorf("marshal repeater response headers: %w", err)
	}
	if send.SentAt.IsZero() {
		send.SentAt = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin repeater transaction: %w", err)
	}
	defer tx.Rollback()
	charge := CaptureRecordAllowance + captureTextBytes(send.SessionID, send.Method, send.URL,
		string(requestHeaders), send.RequestBody, string(responseHeaders), send.ResponseBody, send.ContentType)
	if err := reserveCapture(ctx, tx, charge); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO repeater_sessions (id, project_id, name, created_at_unix_nano, updated_at_unix_nano)
		VALUES (?, 1, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET updated_at_unix_nano = excluded.updated_at_unix_nano`,
		send.SessionID, send.SessionID, send.SentAt.UnixNano(), send.SentAt.UnixNano(),
	); err != nil {
		return fmt.Errorf("upsert repeater session: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO repeater_sends (
			session_id, method, url, request_headers_json, request_body, status,
			response_headers_json, response_body, duration_ms, size, truncated,
			content_type, sent_at_unix_nano, capture_bytes
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		send.SessionID, send.Method, send.URL, string(requestHeaders), send.RequestBody,
		send.Status, string(responseHeaders), send.ResponseBody, send.DurationMS, send.Size,
		send.Truncated, send.ContentType, send.SentAt.UnixNano(), charge,
	)
	if err != nil {
		return fmt.Errorf("insert repeater send: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("get repeater send id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit repeater transaction: %w", err)
	}
	send.ID = id
	return nil
}

func (s *SQLiteStore) ListRepeaterSessions(ctx context.Context) ([]RepeaterSession, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.id, s.name, COUNT(h.id), s.updated_at_unix_nano
		FROM repeater_sessions s
		LEFT JOIN repeater_sends h ON h.session_id = s.id
		GROUP BY s.id, s.name, s.updated_at_unix_nano
		ORDER BY s.updated_at_unix_nano DESC`)
	if err != nil {
		return nil, fmt.Errorf("list repeater sessions: %w", err)
	}
	defer rows.Close()
	sessions := make([]RepeaterSession, 0)
	for rows.Next() {
		var session RepeaterSession
		var updatedAt int64
		if err := rows.Scan(&session.ID, &session.Name, &session.SendCount, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan repeater session: %w", err)
		}
		session.UpdatedAt = time.Unix(0, updatedAt).UTC()
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

func (s *SQLiteStore) ListRepeaterSends(ctx context.Context, sessionID string) ([]RepeaterSend, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, method, url, request_headers_json, request_body, status,
			response_headers_json, response_body, duration_ms, size, truncated,
			content_type, sent_at_unix_nano
		FROM repeater_sends WHERE session_id = ? ORDER BY id DESC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list repeater sends: %w", err)
	}
	defer rows.Close()
	sends := make([]RepeaterSend, 0)
	for rows.Next() {
		var send RepeaterSend
		var requestHeaders, responseHeaders string
		var sentAt int64
		if err := rows.Scan(
			&send.ID, &send.SessionID, &send.Method, &send.URL, &requestHeaders,
			&send.RequestBody, &send.Status, &responseHeaders, &send.ResponseBody,
			&send.DurationMS, &send.Size, &send.Truncated, &send.ContentType, &sentAt,
		); err != nil {
			return nil, fmt.Errorf("scan repeater send: %w", err)
		}
		if err := json.Unmarshal([]byte(requestHeaders), &send.RequestHeaders); err != nil {
			return nil, fmt.Errorf("unmarshal repeater request headers: %w", err)
		}
		if err := json.Unmarshal([]byte(responseHeaders), &send.ResponseHeaders); err != nil {
			return nil, fmt.Errorf("unmarshal repeater response headers: %w", err)
		}
		send.SentAt = time.Unix(0, sentAt).UTC()
		sends = append(sends, send)
	}
	return sends, rows.Err()
}
