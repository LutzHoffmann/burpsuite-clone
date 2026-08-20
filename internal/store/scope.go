package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/lutzifer/burpsuite-clone/internal/scope"
)

var ErrScopeVersionConflict = errors.New("scope version conflict")

func (s *SQLiteStore) LoadScopeState(ctx context.Context) (scope.State, error) {
	var state scope.State
	if err := s.db.QueryRowContext(ctx, `SELECT version FROM scope_state WHERE project_id = 1`).Scan(&state.Version); err != nil {
		return scope.State{}, fmt.Errorf("load scope version: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, enabled, action, scheme, host_pattern, port, path_prefix
		FROM scope_rules
		WHERE project_id = 1
		ORDER BY position ASC`)
	if err != nil {
		return scope.State{}, fmt.Errorf("list scope rules: %w", err)
	}
	defer rows.Close()

	state.Rules = make([]scope.Rule, 0)
	for rows.Next() {
		var rule scope.Rule
		if err := rows.Scan(
			&rule.ID, &rule.Enabled, &rule.Action, &rule.Scheme, &rule.HostPattern, &rule.Port, &rule.PathPrefix,
		); err != nil {
			return scope.State{}, fmt.Errorf("scan scope rule: %w", err)
		}
		state.Rules = append(state.Rules, rule)
	}
	if err := rows.Err(); err != nil {
		return scope.State{}, fmt.Errorf("iterate scope rules: %w", err)
	}
	return state, nil
}

func (s *SQLiteStore) ReplaceScopeRules(ctx context.Context, expectedVersion int64, rules []scope.Rule) (scope.State, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return scope.State{}, fmt.Errorf("open scope connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return scope.State{}, fmt.Errorf("begin scope transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	var version int64
	if err := conn.QueryRowContext(ctx, `SELECT version FROM scope_state WHERE project_id = 1`).Scan(&version); err != nil {
		return scope.State{}, fmt.Errorf("load scope version: %w", err)
	}
	if version != expectedVersion {
		return scope.State{}, ErrScopeVersionConflict
	}
	if _, err := conn.ExecContext(ctx, `DELETE FROM scope_rules WHERE project_id = 1`); err != nil {
		return scope.State{}, fmt.Errorf("delete scope rules: %w", err)
	}

	persisted := make([]scope.Rule, len(rules))
	copy(persisted, rules)
	for position := range persisted {
		rule := &persisted[position]
		result, err := conn.ExecContext(ctx, `
			INSERT INTO scope_rules (
				project_id, enabled, action, scheme, host_pattern, port, path_prefix, position
			) VALUES (1, ?, ?, ?, ?, ?, ?, ?)`,
			rule.Enabled, rule.Action, rule.Scheme, rule.HostPattern, rule.Port, rule.PathPrefix, position,
		)
		if err != nil {
			return scope.State{}, fmt.Errorf("insert scope rule: %w", err)
		}
		rule.ID, err = result.LastInsertId()
		if err != nil {
			return scope.State{}, fmt.Errorf("get scope rule id: %w", err)
		}
	}
	if _, err := conn.ExecContext(ctx, `UPDATE scope_state SET version = version + 1 WHERE project_id = 1`); err != nil {
		return scope.State{}, fmt.Errorf("increment scope version: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return scope.State{}, fmt.Errorf("commit scope transaction: %w", err)
	}
	committed = true
	return scope.State{Version: version + 1, Rules: persisted}, nil
}

func (s *SQLiteStore) LatestExchangeID(ctx context.Context) (int64, error) {
	var id int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM exchanges WHERE project_id = 1`).Scan(&id); err != nil {
		return 0, fmt.Errorf("latest exchange id: %w", err)
	}
	return id, nil
}

func (s *SQLiteStore) CountExchangesThrough(ctx context.Context, throughID int64) (int64, error) {
	var count int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM exchanges WHERE project_id = 1 AND id <= ?`, throughID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count exchanges through id: %w", err)
	}
	return count, nil
}

func (s *SQLiteStore) ListExchangesPage(ctx context.Context, afterID, throughID int64, limit int) ([]Exchange, error) {
	if limit <= 0 {
		return []Exchange{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.id
		FROM exchanges e
		WHERE e.project_id = 1 AND e.id > ? AND e.id <= ?
		ORDER BY e.id ASC
		LIMIT ?`, afterID, throughID, limit)
	if err != nil {
		return nil, fmt.Errorf("list exchange page: %w", err)
	}

	ids := make([]int64, 0)
	if err := func() error {
		defer rows.Close()
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return fmt.Errorf("scan exchange page id: %w", err)
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate exchange page: %w", err)
		}
		return nil
	}(); err != nil {
		return nil, err
	}

	exchanges := make([]Exchange, 0, len(ids))
	for _, id := range ids {
		exchange, err := s.GetExchange(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("get exchange page item: %w", err)
		}
		exchanges = append(exchanges, *exchange)
	}
	return exchanges, nil
}

var _ ScopeStore = (*SQLiteStore)(nil)
var _ RebuildHistoryStore = (*SQLiteStore)(nil)
