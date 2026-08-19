package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/config"
	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db             *sql.DB
	bodyLimitBytes int64
}

func OpenSQLite(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	if _, err := db.Exec(schemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply sqlite schema: %w", err)
	}

	return &SQLiteStore{db: db, bodyLimitBytes: config.Load().BodyLimitBytes}, nil
}

func (s *SQLiteStore) SaveExchange(ctx context.Context, exchange *Exchange) error {
	requestHeaders, err := marshalHeaders(exchange.Request.Headers)
	if err != nil {
		return fmt.Errorf("marshal request headers: %w", err)
	}
	responseHeaders, err := marshalHeaders(exchange.Response.Headers)
	if err != nil {
		return fmt.Errorf("marshal response headers: %w", err)
	}
	tags, err := json.Marshal(exchange.Tags)
	if err != nil {
		return fmt.Errorf("marshal tags: %w", err)
	}

	requestBody, requestTruncated := s.capBody(exchange.Request.Body)
	responseBody, responseTruncated := s.capBody(exchange.Response.Body)
	exchange.RequestTruncated = exchange.RequestTruncated || requestTruncated
	exchange.ResponseTruncated = exchange.ResponseTruncated || responseTruncated

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin exchange transaction: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		INSERT INTO exchanges (
			method, scheme, host, path, query, status, mime_type, request_size, response_size,
			duration_ms, started_at_unix_nano, intercepted, error, error_message,
			request_truncated, response_truncated, tags_json, note
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		exchange.Method, exchange.Scheme, exchange.Host, exchange.Path, exchange.Query,
		exchange.Status, exchange.MIMEType, exchange.RequestSize, exchange.ResponseSize,
		exchange.Duration.Milliseconds(), exchange.StartedAt.UnixNano(), exchange.Intercepted,
		exchange.Error, exchange.ErrorMessage, exchange.RequestTruncated,
		exchange.ResponseTruncated, string(tags), exchange.Note,
	)
	if err != nil {
		return fmt.Errorf("insert exchange: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("get exchange id: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO exchange_bodies (
			exchange_id, request_headers_json, request_body, request_raw,
			response_headers_json, response_body, response_raw
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, requestHeaders, requestBody, exchange.Request.Raw,
		responseHeaders, responseBody, exchange.Response.Raw,
	); err != nil {
		return fmt.Errorf("insert exchange bodies: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit exchange transaction: %w", err)
	}

	exchange.ID = id
	return nil
}

func (s *SQLiteStore) ListHistory(ctx context.Context, filter HistoryFilter) ([]HistoryItem, error) {
	query := `
		SELECT id, method, scheme, host, path, query, status, mime_type, request_size,
			response_size, duration_ms, started_at_unix_nano, intercepted, error
		FROM exchanges`
	var conditions []string
	var args []any
	if filter.Search != "" {
		conditions = append(conditions, "(host LIKE ? OR path LIKE ? OR query LIKE ?)")
		search := "%" + filter.Search + "%"
		args = append(args, search, search, search)
	}
	if filter.Method != "" {
		conditions = append(conditions, "method = ?")
		args = append(args, filter.Method)
	}
	if filter.Host != "" {
		conditions = append(conditions, "host = ?")
		args = append(args, filter.Host)
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY id DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list history: %w", err)
	}
	defer rows.Close()

	var items []HistoryItem
	for rows.Next() {
		var item HistoryItem
		var startedAt int64
		if err := rows.Scan(
			&item.ID, &item.Method, &item.Scheme, &item.Host, &item.Path, &item.Query,
			&item.Status, &item.MIMEType, &item.RequestSize, &item.ResponseSize,
			&item.DurationMS, &startedAt, &item.Intercepted, &item.Error,
		); err != nil {
			return nil, fmt.Errorf("scan history item: %w", err)
		}
		item.StartedAt = time.Unix(0, startedAt).UTC()
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate history items: %w", err)
	}
	return items, nil
}

func (s *SQLiteStore) GetExchange(ctx context.Context, id int64) (*Exchange, error) {
	exchange := &Exchange{ID: id}
	var startedAt int64
	var tagsJSON, requestHeadersJSON, responseHeadersJSON string
	err := s.db.QueryRowContext(ctx, `
		SELECT e.method, e.scheme, e.host, e.path, e.query, e.status, e.mime_type,
			e.request_size, e.response_size, e.duration_ms, e.started_at_unix_nano,
			e.intercepted, e.error, e.error_message, e.request_truncated,
			e.response_truncated, e.tags_json, e.note, b.request_headers_json,
			b.request_body, b.request_raw, b.response_headers_json, b.response_body, b.response_raw
		FROM exchanges e
		JOIN exchange_bodies b ON b.exchange_id = e.id
		WHERE e.id = ?`, id).Scan(
		&exchange.Method, &exchange.Scheme, &exchange.Host, &exchange.Path, &exchange.Query,
		&exchange.Status, &exchange.MIMEType, &exchange.RequestSize, &exchange.ResponseSize,
		&exchange.Duration, &startedAt, &exchange.Intercepted, &exchange.Error,
		&exchange.ErrorMessage, &exchange.RequestTruncated, &exchange.ResponseTruncated,
		&tagsJSON, &exchange.Note, &requestHeadersJSON, &exchange.Request.Body,
		&exchange.Request.Raw, &responseHeadersJSON, &exchange.Response.Body, &exchange.Response.Raw,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, err
		}
		return nil, fmt.Errorf("get exchange: %w", err)
	}

	if err := json.Unmarshal([]byte(tagsJSON), &exchange.Tags); err != nil {
		return nil, fmt.Errorf("unmarshal tags: %w", err)
	}
	if err := json.Unmarshal([]byte(requestHeadersJSON), &exchange.Request.Headers); err != nil {
		return nil, fmt.Errorf("unmarshal request headers: %w", err)
	}
	if err := json.Unmarshal([]byte(responseHeadersJSON), &exchange.Response.Headers); err != nil {
		return nil, fmt.Errorf("unmarshal response headers: %w", err)
	}
	exchange.Duration *= time.Millisecond
	exchange.StartedAt = time.Unix(0, startedAt).UTC()
	return exchange, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) capBody(body []byte) ([]byte, bool) {
	if int64(len(body)) <= s.bodyLimitBytes {
		return body, false
	}
	return body[:s.bodyLimitBytes], true
}

func marshalHeaders(headers map[string][]string) ([]byte, error) {
	if headers == nil {
		headers = map[string][]string{}
	}
	return json.Marshal(headers)
}
