package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
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
	if err := restrictSQLiteFiles(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	if err := applyMigrations(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply sqlite migrations: %w", err)
	}
	if err := restrictSQLiteFiles(path); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &SQLiteStore{db: db, bodyLimitBytes: config.Load().BodyLimitBytes}, nil
}

func restrictSQLiteFiles(path string) error {
	for _, candidate := range []string{path, path + "-journal", path + "-shm", path + "-wal"} {
		if err := os.Chmod(candidate, 0o600); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("restrict sqlite file permissions: %w", err)
		}
	}
	return nil
}

func sqliteDSN(path string) string {
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	return path + separator + "_pragma=foreign_keys%281%29&_pragma=busy_timeout%285000%29"
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
	appliedRuleIDs, err := json.Marshal(append([]string{}, exchange.AppliedRuleIDs...))
	if err != nil {
		return fmt.Errorf("marshal applied rule ids: %w", err)
	}

	requestBody, requestBodyTruncated := s.capBody(exchange.Request.Body)
	requestRaw, requestRawTruncated := s.capBody(exchange.Request.Raw)
	responseBody, responseBodyTruncated := s.capBody(exchange.Response.Body)
	responseRaw, responseRawTruncated := s.capBody(exchange.Response.Raw)
	exchange.RequestTruncated = exchange.RequestTruncated || requestBodyTruncated || requestRawTruncated
	exchange.ResponseTruncated = exchange.ResponseTruncated || responseBodyTruncated || responseRawTruncated

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin exchange transaction: %w", err)
	}
	defer tx.Rollback()

	charge := CaptureRecordAllowance + captureTextBytes(exchange.Method, exchange.Scheme,
		exchange.Host, exchange.Path, exchange.Query, exchange.MIMEType, exchange.ErrorMessage,
		string(tags), exchange.Note, string(appliedRuleIDs)) + int64(len(requestHeaders)) +
		int64(len(responseHeaders)) + int64(len(requestBody)) + int64(len(requestRaw)) +
		int64(len(responseBody)) + int64(len(responseRaw))
	if err := reserveCapture(ctx, tx, charge); err != nil {
		return err
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO exchanges (
			method, scheme, host, path, query, status, mime_type, request_size, response_size,
			duration_ms, started_at_unix_nano, intercepted, error, error_message,
			request_truncated, response_truncated, in_scope, scope_version, scope_rule_id,
			tags_json, note, applied_rule_ids_json, response_intercepted, capture_bytes
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		exchange.Method, exchange.Scheme, exchange.Host, exchange.Path, exchange.Query,
		exchange.Status, exchange.MIMEType, exchange.RequestSize, exchange.ResponseSize,
		exchange.Duration.Milliseconds(), exchange.StartedAt.UnixNano(), exchange.Intercepted,
		exchange.Error, exchange.ErrorMessage, exchange.RequestTruncated,
		exchange.ResponseTruncated, exchange.InScope, exchange.ScopeVersion, exchange.ScopeRuleID,
		string(tags), exchange.Note, string(appliedRuleIDs), exchange.ResponseIntercepted, charge,
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
		id, requestHeaders, requestBody, requestRaw,
		responseHeaders, responseBody, responseRaw,
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
	page, err := s.ListHistoryPage(ctx, HistoryPageRequest{Filter: filter})
	return page.Items, err
}

func (s *SQLiteStore) GetExchange(ctx context.Context, id int64) (*Exchange, error) {
	exchange := &Exchange{ID: id}
	var startedAt int64
	var tagsJSON, requestHeadersJSON, responseHeadersJSON, appliedRuleIDsJSON string
	err := s.db.QueryRowContext(ctx, `
		SELECT e.method, e.scheme, e.host, e.path, e.query, e.status, e.mime_type,
			e.request_size, e.response_size, e.duration_ms, e.started_at_unix_nano,
			e.intercepted, e.error, e.error_message, e.request_truncated,
			e.response_truncated, e.in_scope, e.scope_version, e.scope_rule_id, e.tags_json, e.note, b.request_headers_json,
			b.request_body, b.request_raw, b.response_headers_json, b.response_body, b.response_raw,
			e.applied_rule_ids_json, e.response_intercepted
		FROM exchanges e
		JOIN exchange_bodies b ON b.exchange_id = e.id
		WHERE e.id = ?`, id).Scan(
		&exchange.Method, &exchange.Scheme, &exchange.Host, &exchange.Path, &exchange.Query,
		&exchange.Status, &exchange.MIMEType, &exchange.RequestSize, &exchange.ResponseSize,
		&exchange.Duration, &startedAt, &exchange.Intercepted, &exchange.Error,
		&exchange.ErrorMessage, &exchange.RequestTruncated, &exchange.ResponseTruncated,
		&exchange.InScope, &exchange.ScopeVersion, &exchange.ScopeRuleID,
		&tagsJSON, &exchange.Note, &requestHeadersJSON, &exchange.Request.Body,
		&exchange.Request.Raw, &responseHeadersJSON, &exchange.Response.Body, &exchange.Response.Raw,
		&appliedRuleIDsJSON, &exchange.ResponseIntercepted,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, err
		}
		return nil, fmt.Errorf("get exchange: %w", err)
	}

	if err := json.Unmarshal([]byte(appliedRuleIDsJSON), &exchange.AppliedRuleIDs); err != nil {
		return nil, fmt.Errorf("unmarshal applied rule ids: %w", err)
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
