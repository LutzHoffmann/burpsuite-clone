package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const historyPageSize = 100

type HistoryPageRequest struct {
	Filter     HistoryFilter
	BeforeID   int64
	SnapshotID int64
}

type HistoryPage struct {
	Items        []HistoryItem `json:"items"`
	NextBeforeID int64         `json:"nextBeforeId"`
	SnapshotID   int64         `json:"snapshotId"`
}

type HistoryPageStore interface {
	ListHistoryPage(context.Context, HistoryPageRequest) (HistoryPage, error)
}

func validateHistoryPageRequest(request HistoryPageRequest) error {
	if request.BeforeID < 0 || request.SnapshotID < 0 || (request.BeforeID > 0 && (request.SnapshotID == 0 || request.BeforeID > request.SnapshotID)) {
		return fmt.Errorf("invalid history page cursors")
	}
	return nil
}

func finishHistoryPage(items []HistoryItem, snapshot int64) HistoryPage {
	page := HistoryPage{Items: items, SnapshotID: snapshot}
	if len(items) > historyPageSize {
		page.Items = items[:historyPageSize]
		page.NextBeforeID = page.Items[historyPageSize-1].ID
	}
	return page
}

// SQLite LIKE folds ASCII only; Unicode lowercasing would change memory semantics.
func historyASCIILower(value string) string {
	bytes := []byte(value)
	for i, b := range bytes {
		if b >= 'A' && b <= 'Z' {
			bytes[i] = b + ('a' - 'A')
		}
	}
	return string(bytes)
}

func (s *SQLiteStore) ListHistoryPage(ctx context.Context, request HistoryPageRequest) (HistoryPage, error) {
	if err := validateHistoryPageRequest(request); err != nil {
		return HistoryPage{}, err
	}
	snapshot := request.SnapshotID
	if snapshot == 0 {
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM exchanges`).Scan(&snapshot); err != nil {
			return HistoryPage{}, fmt.Errorf("read history snapshot: %w", err)
		}
	}
	conditions := []string{"id <= ?"}
	args := []any{snapshot}
	if request.BeforeID > 0 {
		conditions = append(conditions, "id < ?")
		args = append(args, request.BeforeID)
	}
	filter := request.Filter
	if filter.Search != "" {
		conditions = append(conditions, `(host LIKE ? ESCAPE '\' OR path LIKE ? ESCAPE '\' OR query LIKE ? ESCAPE '\')`)
		search := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(filter.Search) + "%"
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
	if filter.InScope != nil {
		conditions = append(conditions, "in_scope = ?")
		args = append(args, *filter.InScope)
	}
	query := `SELECT id, method, scheme, host, path, query, status, mime_type, request_size,
 response_size, duration_ms, started_at_unix_nano, intercepted, error,
 in_scope, scope_version, scope_rule_id, applied_rule_ids_json, response_intercepted
 FROM exchanges WHERE ` + strings.Join(conditions, " AND ") + ` ORDER BY id DESC LIMIT 101`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return HistoryPage{}, fmt.Errorf("list history page: %w", err)
	}
	defer rows.Close()
	items := make([]HistoryItem, 0, historyPageSize+1)
	for rows.Next() {
		var item HistoryItem
		var startedAt int64
		var rules string
		if err := rows.Scan(&item.ID, &item.Method, &item.Scheme, &item.Host, &item.Path, &item.Query,
			&item.Status, &item.MIMEType, &item.RequestSize, &item.ResponseSize, &item.DurationMS, &startedAt,
			&item.Intercepted, &item.Error, &item.InScope, &item.ScopeVersion, &item.ScopeRuleID, &rules, &item.ResponseIntercepted); err != nil {
			return HistoryPage{}, fmt.Errorf("scan history page: %w", err)
		}
		if err := json.Unmarshal([]byte(rules), &item.AppliedRuleIDs); err != nil {
			return HistoryPage{}, fmt.Errorf("decode history page rules: %w", err)
		}
		item.StartedAt = time.Unix(0, startedAt).UTC()
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return HistoryPage{}, fmt.Errorf("iterate history page: %w", err)
	}
	return finishHistoryPage(items, snapshot), nil
}
