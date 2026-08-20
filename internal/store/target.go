package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"
)

const activeProjectID int64 = 1

var targetDiagnosticCodes = map[string]struct{}{
	"body_truncated":                 {},
	"body_compressed":                {},
	"body_unsupported_mime":          {},
	"body_binary":                    {},
	"form_malformed":                 {},
	"json_malformed":                 {},
	"json_depth_exceeded":            {},
	"json_field_limit_exceeded":      {},
	"multipart_malformed":            {},
	"multipart_field_limit_exceeded": {},
}

type TargetGeneration struct {
	ID, ScopeVersion, Processed, Total int64
	Status, Error                      string
	StartedAt                          time.Time
	CompletedAt                        *time.Time
}

type TargetTreeNode struct {
	ID                          int64
	Scheme, Host, Path, Method  string
	Port                        int
	InScope                     bool
	Statuses                    []int
	RequestMIMEs, ResponseMIMEs []string
	Count                       int64
	LastSeen                    time.Time
	Children                    []TargetTreeNode
}

type TargetEndpoint struct {
	ID                                            int64
	Key                                           TargetEndpointKey
	InScope                                       bool
	FirstSeen, LastSeen                           time.Time
	Count                                         int64
	Statuses                                      []int
	RequestMIMEs, ResponseMIMEs, ParseDiagnostics []string
	ErrorSeen                                     bool
	LatestExchangeID                              int64
}

type TargetRequestRef struct {
	ExchangeID int64
	StartedAt  time.Time
	Status     int
	Error      bool
}

type TargetStore interface {
	CreateTargetGeneration(context.Context, int64, int64) (int64, error)
	UpsertTargetObservation(context.Context, int64, TargetObservation) error
	SetTargetGenerationProgress(context.Context, int64, int64) error
	ActivateTargetGeneration(context.Context, int64) error
	FailTargetGeneration(context.Context, int64, string) error
	CancelTargetGeneration(context.Context, int64) error
	ActiveTargetGeneration(context.Context) (TargetGeneration, error)
	LatestTargetGeneration(context.Context) (TargetGeneration, error)
	PruneRetiredTargetGenerations(context.Context) error
	ListTargetTree(context.Context) ([]TargetTreeNode, error)
	GetTargetEndpoint(context.Context, int64) (TargetEndpoint, error)
	ListTargetRequests(context.Context, int64) ([]TargetRequestRef, error)
	ListTargetParameters(context.Context, int64) ([]TargetParameter, error)
}

type targetSQLExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type targetRowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *SQLiteStore) CreateTargetGeneration(ctx context.Context, scopeVersion, total int64) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO target_generations (
			project_id, scope_version, status, processed, total, error, started_at_unix_nano
		) VALUES (?, ?, 'building', 0, ?, '', ?)`,
		activeProjectID, scopeVersion, total, time.Now().UTC().UnixNano(),
	)
	if err != nil {
		return 0, fmt.Errorf("create target generation: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get target generation id: %w", err)
	}
	return id, nil
}

func (s *SQLiteStore) UpsertTargetObservation(ctx context.Context, generationID int64, observation TargetObservation) error {
	conn, err := s.beginImmediateTargetTransaction(ctx)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		}
		_ = conn.Close()
	}()

	if err := targetObservationBoundary(ctx, conn, generationID, observation.ExchangeID); err != nil {
		return err
	}
	seenAt := observation.StartedAt.UTC().UnixNano()
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO target_endpoints (
			generation_id, scheme, host, port, path, method, first_seen_unix_nano,
			last_seen_unix_nano, observation_count, latest_exchange_id, statuses_json,
			request_mimes_json, response_mimes_json, parse_diagnostics_json, error_seen
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?, '[]', '[]', '[]', '[]', 0)
		ON CONFLICT(generation_id, scheme, host, port, path, method) DO NOTHING`,
		generationID, observation.Key.Scheme, observation.Key.Host, observation.Key.Port,
		observation.Key.Path, observation.Key.Method, seenAt, seenAt, observation.ExchangeID,
	); err != nil {
		return fmt.Errorf("upsert target endpoint: %w", err)
	}

	var endpointID, firstSeen, lastSeen int64
	var statusesJSON, requestMIMEsJSON, responseMIMEsJSON, diagnosticsJSON string
	if err := conn.QueryRowContext(ctx, `
		SELECT id, first_seen_unix_nano, last_seen_unix_nano, statuses_json,
			request_mimes_json, response_mimes_json, parse_diagnostics_json
		FROM target_endpoints
		WHERE generation_id = ? AND scheme = ? AND host = ? AND port = ? AND path = ? AND method = ?`,
		generationID, observation.Key.Scheme, observation.Key.Host, observation.Key.Port,
		observation.Key.Path, observation.Key.Method,
	).Scan(&endpointID, &firstSeen, &lastSeen, &statusesJSON, &requestMIMEsJSON, &responseMIMEsJSON, &diagnosticsJSON); err != nil {
		return fmt.Errorf("load target endpoint aggregate: %w", err)
	}

	result, err := conn.ExecContext(ctx, `
		INSERT INTO target_endpoint_exchanges (endpoint_id, exchange_id) VALUES (?, ?)
		ON CONFLICT(endpoint_id, exchange_id) DO NOTHING`, endpointID, observation.ExchangeID)
	if err != nil {
		return fmt.Errorf("insert target endpoint exchange: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count target endpoint exchange insert: %w", err)
	}
	if inserted == 0 {
		if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
			return fmt.Errorf("commit duplicate target observation: %w", err)
		}
		committed = true
		return nil
	}

	statuses, err := mergeIntJSONSet(statusesJSON, observation.Status)
	if err != nil {
		return fmt.Errorf("merge target statuses: %w", err)
	}
	requestMIMEs, err := mergeStringJSONSet(requestMIMEsJSON, observation.RequestMIME)
	if err != nil {
		return fmt.Errorf("merge target request MIME types: %w", err)
	}
	responseMIMEs, err := mergeStringJSONSet(responseMIMEsJSON, observation.ResponseMIME)
	if err != nil {
		return fmt.Errorf("merge target response MIME types: %w", err)
	}
	diagnostic := ""
	if _, allowed := targetDiagnosticCodes[observation.ParseDiagnostic]; allowed {
		diagnostic = observation.ParseDiagnostic
	}
	diagnostics, err := mergeStringJSONSet(diagnosticsJSON, diagnostic)
	if err != nil {
		return fmt.Errorf("merge target parse diagnostics: %w", err)
	}

	latestExchangeID := observation.ExchangeID
	if seenAt < lastSeen {
		if err := conn.QueryRowContext(ctx, `SELECT latest_exchange_id FROM target_endpoints WHERE id = ?`, endpointID).Scan(&latestExchangeID); err != nil {
			return fmt.Errorf("load latest target exchange: %w", err)
		}
	}
	if seenAt < firstSeen {
		firstSeen = seenAt
	}
	if seenAt > lastSeen {
		lastSeen = seenAt
	}
	if _, err := conn.ExecContext(ctx, `
		UPDATE target_endpoints SET
			first_seen_unix_nano = ?, last_seen_unix_nano = ?,
			observation_count = observation_count + 1, latest_exchange_id = ?,
			statuses_json = ?, request_mimes_json = ?, response_mimes_json = ?,
			parse_diagnostics_json = ?, error_seen = error_seen OR ?
		WHERE id = ?`,
		firstSeen, lastSeen, latestExchangeID, statuses, requestMIMEs, responseMIMEs,
		diagnostics, observation.Error, endpointID,
	); err != nil {
		return fmt.Errorf("update target endpoint aggregate: %w", err)
	}

	uniqueParameters := make(map[targetParameterKey]struct{}, len(observation.Parameters))
	for _, parameter := range observation.Parameters {
		key := targetParameterKey{location: parameter.Location, name: parameter.Name, valueType: parameter.ValueType}
		uniqueParameters[key] = struct{}{}
	}
	for parameter := range uniqueParameters {
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO target_parameters (
				endpoint_id, location, name, value_type, first_seen_unix_nano,
				last_seen_unix_nano, observation_count
			) VALUES (?, ?, ?, ?, ?, ?, 1)
			ON CONFLICT(endpoint_id, location, name, value_type) DO UPDATE SET
				first_seen_unix_nano = MIN(first_seen_unix_nano, excluded.first_seen_unix_nano),
				last_seen_unix_nano = MAX(last_seen_unix_nano, excluded.last_seen_unix_nano),
				observation_count = observation_count + 1`,
			endpointID, parameter.location, parameter.name, parameter.valueType, seenAt, seenAt,
		); err != nil {
			return fmt.Errorf("upsert target parameter: %w", err)
		}
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("commit target observation: %w", err)
	}
	committed = true
	return nil
}

func (s *SQLiteStore) beginImmediateTargetTransaction(ctx context.Context) (*sql.Conn, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("open target transaction connection: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA busy_timeout = 5000`); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("set target transaction busy timeout: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("begin immediate target transaction: %w", err)
	}
	return conn, nil
}

func targetObservationBoundary(ctx context.Context, tx targetSQLExecutor, generationID, exchangeID int64) error {
	var exists int
	err := tx.QueryRowContext(ctx, `
		SELECT 1
		FROM target_generations g
		JOIN exchanges e ON e.id = ? AND e.project_id = g.project_id
		WHERE g.id = ? AND g.project_id = ? AND g.status IN ('building', 'active')`,
		exchangeID, generationID, activeProjectID,
	).Scan(&exists)
	if err == sql.ErrNoRows {
		return sql.ErrNoRows
	}
	if err != nil {
		return fmt.Errorf("validate target observation boundary: %w", err)
	}
	return nil
}

func (s *SQLiteStore) SetTargetGenerationProgress(ctx context.Context, generationID, processed int64) error {
	return s.updateBuildingGeneration(ctx, `
		UPDATE target_generations SET processed = ?
		WHERE id = ? AND project_id = ? AND status = 'building'`,
		"set target generation progress", processed, generationID, activeProjectID)
}

func (s *SQLiteStore) FailTargetGeneration(ctx context.Context, generationID int64, generationError string) error {
	return s.updateBuildingGeneration(ctx, `
		UPDATE target_generations
		SET status = 'failed', error = ?, completed_at_unix_nano = ?
		WHERE id = ? AND project_id = ? AND status = 'building'`,
		"fail target generation", generationError, time.Now().UTC().UnixNano(), generationID, activeProjectID)
}

func (s *SQLiteStore) CancelTargetGeneration(ctx context.Context, generationID int64) error {
	return s.updateBuildingGeneration(ctx, `
		UPDATE target_generations
		SET status = 'cancelled', completed_at_unix_nano = ?
		WHERE id = ? AND project_id = ? AND status = 'building'`,
		"cancel target generation", time.Now().UTC().UnixNano(), generationID, activeProjectID)
}

func (s *SQLiteStore) updateBuildingGeneration(ctx context.Context, query, operation string, args ...any) error {
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count %s: %w", operation, err)
	}
	if updated == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SQLiteStore) ActivateTargetGeneration(ctx context.Context, generationID int64) error {
	conn, err := s.beginImmediateTargetTransaction(ctx)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		}
		_ = conn.Close()
	}()

	var status string
	if err := conn.QueryRowContext(ctx, `
		SELECT status FROM target_generations WHERE id = ? AND project_id = ?`,
		generationID, activeProjectID,
	).Scan(&status); err != nil {
		if err == sql.ErrNoRows {
			return sql.ErrNoRows
		}
		return fmt.Errorf("load target generation for activation: %w", err)
	}
	if status != "building" {
		return fmt.Errorf("activate target generation %d: status is %s", generationID, status)
	}
	if _, err := conn.ExecContext(ctx, `
		UPDATE target_generations SET status = 'retired'
		WHERE id = (
			SELECT active_target_generation_id FROM scope_state WHERE project_id = ?
		) AND project_id = ? AND status = 'active'`, activeProjectID, activeProjectID); err != nil {
		return fmt.Errorf("retire active target generation: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `
		UPDATE scope_state SET active_target_generation_id = ? WHERE project_id = ?`,
		generationID, activeProjectID,
	); err != nil {
		return fmt.Errorf("point to active target generation: %w", err)
	}
	result, err := conn.ExecContext(ctx, `
		UPDATE target_generations
		SET status = 'active', completed_at_unix_nano = ?
		WHERE id = ? AND project_id = ? AND status = 'building'`,
		time.Now().UTC().UnixNano(), generationID, activeProjectID)
	if err != nil {
		return fmt.Errorf("activate target generation: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count activated target generation: %w", err)
	}
	if updated != 1 {
		return fmt.Errorf("activate target generation %d: concurrent status change", generationID)
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("commit target activation: %w", err)
	}
	committed = true

	if err := s.PruneRetiredTargetGenerations(ctx); err != nil {
		log.Printf("prune retired target generations after activating %d: %v", generationID, err)
	}
	return nil
}

func (s *SQLiteStore) ActiveTargetGeneration(ctx context.Context) (TargetGeneration, error) {
	return scanTargetGeneration(s.db.QueryRowContext(ctx, `
		SELECT g.id, g.scope_version, g.processed, g.total, g.status, g.error,
			g.started_at_unix_nano, g.completed_at_unix_nano
		FROM scope_state ss
		JOIN target_generations g ON g.id = ss.active_target_generation_id
		WHERE ss.project_id = ? AND g.project_id = ? AND g.status = 'active'`,
		activeProjectID, activeProjectID))
}

func (s *SQLiteStore) LatestTargetGeneration(ctx context.Context) (TargetGeneration, error) {
	return scanTargetGeneration(s.db.QueryRowContext(ctx, `
		SELECT id, scope_version, processed, total, status, error,
			started_at_unix_nano, completed_at_unix_nano
		FROM target_generations
		WHERE project_id = ?
		ORDER BY id DESC
		LIMIT 1`, activeProjectID))
}

type rowScanner interface {
	Scan(...any) error
}

func scanTargetGeneration(row rowScanner) (TargetGeneration, error) {
	var generation TargetGeneration
	var startedAt int64
	var completedAt sql.NullInt64
	if err := row.Scan(
		&generation.ID, &generation.ScopeVersion, &generation.Processed, &generation.Total,
		&generation.Status, &generation.Error, &startedAt, &completedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return TargetGeneration{}, sql.ErrNoRows
		}
		return TargetGeneration{}, fmt.Errorf("scan target generation: %w", err)
	}
	generation.StartedAt = time.Unix(0, startedAt).UTC()
	if completedAt.Valid {
		completed := time.Unix(0, completedAt.Int64).UTC()
		generation.CompletedAt = &completed
	}
	return generation, nil
}

func (s *SQLiteStore) PruneRetiredTargetGenerations(ctx context.Context) error {
	conn, err := s.beginImmediateTargetTransaction(ctx)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		}
		_ = conn.Close()
	}()

	statements := []string{
		`DELETE FROM target_parameters WHERE endpoint_id IN (
			SELECT e.id FROM target_endpoints e JOIN target_generations g ON g.id = e.generation_id
			WHERE g.project_id = ? AND g.status = 'retired')`,
		`DELETE FROM target_endpoint_exchanges WHERE endpoint_id IN (
			SELECT e.id FROM target_endpoints e JOIN target_generations g ON g.id = e.generation_id
			WHERE g.project_id = ? AND g.status = 'retired')`,
		`DELETE FROM target_endpoints WHERE generation_id IN (
			SELECT id FROM target_generations WHERE project_id = ? AND status = 'retired')`,
		`DELETE FROM target_generations WHERE project_id = ? AND status = 'retired'`,
	}
	for _, statement := range statements {
		if _, err := conn.ExecContext(ctx, statement, activeProjectID); err != nil {
			return fmt.Errorf("prune retired target generation: %w", err)
		}
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("commit retired target generation pruning: %w", err)
	}
	committed = true
	return nil
}

func (s *SQLiteStore) GetTargetEndpoint(ctx context.Context, endpointID int64) (TargetEndpoint, error) {
	var endpoint TargetEndpoint
	var firstSeen, lastSeen int64
	var statusesJSON, requestMIMEsJSON, responseMIMEsJSON, diagnosticsJSON string
	err := s.db.QueryRowContext(ctx, `
		SELECT e.id, e.scheme, e.host, e.port, e.path, e.method,
			e.first_seen_unix_nano, e.last_seen_unix_nano, e.observation_count,
			e.statuses_json, e.request_mimes_json, e.response_mimes_json,
			e.parse_diagnostics_json, e.error_seen, e.latest_exchange_id
		FROM target_endpoints e
		JOIN target_generations g ON g.id = e.generation_id AND g.project_id = ? AND g.status = 'active'
		JOIN scope_state ss ON ss.project_id = ? AND ss.active_target_generation_id = g.id
		WHERE e.id = ?`, activeProjectID, activeProjectID, endpointID).Scan(
		&endpoint.ID, &endpoint.Key.Scheme, &endpoint.Key.Host, &endpoint.Key.Port,
		&endpoint.Key.Path, &endpoint.Key.Method, &firstSeen, &lastSeen, &endpoint.Count,
		&statusesJSON, &requestMIMEsJSON, &responseMIMEsJSON, &diagnosticsJSON,
		&endpoint.ErrorSeen, &endpoint.LatestExchangeID,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return TargetEndpoint{}, sql.ErrNoRows
		}
		return TargetEndpoint{}, fmt.Errorf("get target endpoint: %w", err)
	}
	if err := unmarshalTargetEndpointSets(&endpoint, statusesJSON, requestMIMEsJSON, responseMIMEsJSON, diagnosticsJSON); err != nil {
		return TargetEndpoint{}, err
	}
	endpoint.FirstSeen = time.Unix(0, firstSeen).UTC()
	endpoint.LastSeen = time.Unix(0, lastSeen).UTC()
	return endpoint, nil
}

func (s *SQLiteStore) ListTargetRequests(ctx context.Context, endpointID int64) ([]TargetRequestRef, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin target requests read transaction: %w", err)
	}
	defer tx.Rollback()
	if err := requireActiveTargetEndpoint(ctx, tx, endpointID); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT x.id, x.started_at_unix_nano, x.status, x.error
		FROM target_endpoint_exchanges r
		JOIN target_endpoints e ON e.id = r.endpoint_id
		JOIN target_generations g ON g.id = e.generation_id AND g.project_id = ? AND g.status = 'active'
		JOIN scope_state ss ON ss.project_id = ? AND ss.active_target_generation_id = g.id
		JOIN exchanges x ON x.id = r.exchange_id AND x.project_id = g.project_id
		WHERE e.id = ?
		ORDER BY x.started_at_unix_nano DESC, x.id DESC`, activeProjectID, activeProjectID, endpointID)
	if err != nil {
		return nil, fmt.Errorf("list target requests: %w", err)
	}
	defer rows.Close()

	requests := make([]TargetRequestRef, 0)
	for rows.Next() {
		var request TargetRequestRef
		var startedAt int64
		if err := rows.Scan(&request.ExchangeID, &startedAt, &request.Status, &request.Error); err != nil {
			return nil, fmt.Errorf("scan target request: %w", err)
		}
		request.StartedAt = time.Unix(0, startedAt).UTC()
		requests = append(requests, request)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate target requests: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close target requests: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit target requests read transaction: %w", err)
	}
	return requests, nil
}

func (s *SQLiteStore) ListTargetParameters(ctx context.Context, endpointID int64) ([]TargetParameter, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin target parameters read transaction: %w", err)
	}
	defer tx.Rollback()
	if err := requireActiveTargetEndpoint(ctx, tx, endpointID); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT p.location, p.name, p.value_type, p.first_seen_unix_nano,
			p.last_seen_unix_nano, p.observation_count
		FROM target_parameters p
		JOIN target_endpoints e ON e.id = p.endpoint_id
		JOIN target_generations g ON g.id = e.generation_id AND g.project_id = ? AND g.status = 'active'
		JOIN scope_state ss ON ss.project_id = ? AND ss.active_target_generation_id = g.id
		WHERE e.id = ?
		ORDER BY p.location, p.name, p.value_type`, activeProjectID, activeProjectID, endpointID)
	if err != nil {
		return nil, fmt.Errorf("list target parameters: %w", err)
	}
	defer rows.Close()

	parameters := make([]TargetParameter, 0)
	for rows.Next() {
		var parameter TargetParameter
		var firstSeen, lastSeen int64
		if err := rows.Scan(
			&parameter.Location, &parameter.Name, &parameter.ValueType,
			&firstSeen, &lastSeen, &parameter.Count,
		); err != nil {
			return nil, fmt.Errorf("scan target parameter: %w", err)
		}
		parameter.FirstSeen = time.Unix(0, firstSeen).UTC()
		parameter.LastSeen = time.Unix(0, lastSeen).UTC()
		parameters = append(parameters, parameter)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate target parameters: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close target parameters: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit target parameters read transaction: %w", err)
	}
	return parameters, nil
}

func requireActiveTargetEndpoint(ctx context.Context, queryer targetRowQueryer, endpointID int64) error {
	var exists int
	err := queryer.QueryRowContext(ctx, `
		SELECT 1
		FROM target_endpoints e
		JOIN target_generations g ON g.id = e.generation_id AND g.project_id = ? AND g.status = 'active'
		JOIN scope_state ss ON ss.project_id = ? AND ss.active_target_generation_id = g.id
		WHERE e.id = ?`, activeProjectID, activeProjectID, endpointID).Scan(&exists)
	if err == sql.ErrNoRows {
		return sql.ErrNoRows
	}
	if err != nil {
		return fmt.Errorf("validate active target endpoint: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ListTargetTree(ctx context.Context) ([]TargetTreeNode, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.id, e.scheme, e.host, e.port, e.path, e.method,
			e.last_seen_unix_nano, e.observation_count, e.statuses_json,
			e.request_mimes_json, e.response_mimes_json
		FROM target_endpoints e
		JOIN target_generations g ON g.id = e.generation_id AND g.project_id = ? AND g.status = 'active'
		JOIN scope_state ss ON ss.project_id = ? AND ss.active_target_generation_id = g.id
		ORDER BY e.scheme, e.host, e.port, e.path, e.method, e.id`, activeProjectID, activeProjectID)
	if err != nil {
		return nil, fmt.Errorf("list target tree endpoints: %w", err)
	}
	defer rows.Close()

	roots := make(map[targetAuthorityKey]*targetTreeBuilder)
	for rows.Next() {
		var endpoint TargetTreeNode
		var lastSeen int64
		var statusesJSON, requestMIMEsJSON, responseMIMEsJSON string
		if err := rows.Scan(
			&endpoint.ID, &endpoint.Scheme, &endpoint.Host, &endpoint.Port,
			&endpoint.Path, &endpoint.Method, &lastSeen, &endpoint.Count,
			&statusesJSON, &requestMIMEsJSON, &responseMIMEsJSON,
		); err != nil {
			return nil, fmt.Errorf("scan target tree endpoint: %w", err)
		}
		endpoint.LastSeen = time.Unix(0, lastSeen).UTC()
		if err := json.Unmarshal([]byte(statusesJSON), &endpoint.Statuses); err != nil {
			return nil, fmt.Errorf("unmarshal target tree statuses: %w", err)
		}
		if err := json.Unmarshal([]byte(requestMIMEsJSON), &endpoint.RequestMIMEs); err != nil {
			return nil, fmt.Errorf("unmarshal target tree request MIME types: %w", err)
		}
		if err := json.Unmarshal([]byte(responseMIMEsJSON), &endpoint.ResponseMIMEs); err != nil {
			return nil, fmt.Errorf("unmarshal target tree response MIME types: %w", err)
		}
		addTargetTreeEndpoint(roots, endpoint)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate target tree endpoints: %w", err)
	}

	tree := make([]TargetTreeNode, 0, len(roots))
	for _, root := range roots {
		tree = append(tree, root.freeze())
	}
	sort.Slice(tree, func(i, j int) bool {
		return compareTargetTreeNodes(tree[i], tree[j]) < 0
	})
	return tree, nil
}

type targetParameterKey struct {
	location, name, valueType string
}

type targetAuthorityKey struct {
	scheme, host string
	port         int
}

type targetTreeBuilder struct {
	node     TargetTreeNode
	children map[string]*targetTreeBuilder
}

func addTargetTreeEndpoint(roots map[targetAuthorityKey]*targetTreeBuilder, endpoint TargetTreeNode) {
	authority := targetAuthorityKey{scheme: endpoint.Scheme, host: endpoint.Host, port: endpoint.Port}
	root := roots[authority]
	if root == nil {
		root = &targetTreeBuilder{
			node:     TargetTreeNode{Scheme: endpoint.Scheme, Host: endpoint.Host, Port: endpoint.Port},
			children: make(map[string]*targetTreeBuilder),
		}
		roots[authority] = root
	}
	root.merge(endpoint)

	parent := root
	for _, segment := range targetPathSegments(endpoint.Path) {
		key := "path\x00" + segment
		child := parent.children[key]
		if child == nil {
			child = &targetTreeBuilder{node: TargetTreeNode{Path: segment}, children: make(map[string]*targetTreeBuilder)}
			parent.children[key] = child
		}
		child.merge(endpoint)
		parent = child
	}
	method := endpoint
	method.Scheme = ""
	method.Host = ""
	method.Port = 0
	method.Path = ""
	method.Children = nil
	parent.children["method\x00"+endpoint.Method] = &targetTreeBuilder{node: method, children: make(map[string]*targetTreeBuilder)}
}

func targetPathSegments(path string) []string {
	withoutLeadingSlash := strings.TrimPrefix(path, "/")
	if withoutLeadingSlash == "" {
		return nil
	}
	return strings.Split(withoutLeadingSlash, "/")
}

func (builder *targetTreeBuilder) merge(endpoint TargetTreeNode) {
	builder.node.Count += endpoint.Count
	if endpoint.LastSeen.After(builder.node.LastSeen) {
		builder.node.LastSeen = endpoint.LastSeen
	}
	builder.node.Statuses = mergeInts(builder.node.Statuses, endpoint.Statuses...)
	builder.node.RequestMIMEs = mergeStrings(builder.node.RequestMIMEs, endpoint.RequestMIMEs...)
	builder.node.ResponseMIMEs = mergeStrings(builder.node.ResponseMIMEs, endpoint.ResponseMIMEs...)
}

func (builder *targetTreeBuilder) freeze() TargetTreeNode {
	node := builder.node
	node.Children = make([]TargetTreeNode, 0, len(builder.children))
	for _, child := range builder.children {
		node.Children = append(node.Children, child.freeze())
	}
	sort.Slice(node.Children, func(i, j int) bool {
		return compareTargetTreeNodes(node.Children[i], node.Children[j]) < 0
	})
	return node
}

func compareTargetTreeNodes(left, right TargetTreeNode) int {
	leftMethod := left.Method != ""
	rightMethod := right.Method != ""
	if leftMethod != rightMethod {
		if leftMethod {
			return 1
		}
		return -1
	}
	leftValues := []string{left.Scheme, left.Host, left.Path, left.Method}
	rightValues := []string{right.Scheme, right.Host, right.Path, right.Method}
	for index := range leftValues {
		if leftValues[index] < rightValues[index] {
			return -1
		}
		if leftValues[index] > rightValues[index] {
			return 1
		}
	}
	if left.Port < right.Port {
		return -1
	}
	if left.Port > right.Port {
		return 1
	}
	return 0
}

func unmarshalTargetEndpointSets(endpoint *TargetEndpoint, statusesJSON, requestMIMEsJSON, responseMIMEsJSON, diagnosticsJSON string) error {
	if err := json.Unmarshal([]byte(statusesJSON), &endpoint.Statuses); err != nil {
		return fmt.Errorf("unmarshal target statuses: %w", err)
	}
	if err := json.Unmarshal([]byte(requestMIMEsJSON), &endpoint.RequestMIMEs); err != nil {
		return fmt.Errorf("unmarshal target request MIME types: %w", err)
	}
	if err := json.Unmarshal([]byte(responseMIMEsJSON), &endpoint.ResponseMIMEs); err != nil {
		return fmt.Errorf("unmarshal target response MIME types: %w", err)
	}
	if err := json.Unmarshal([]byte(diagnosticsJSON), &endpoint.ParseDiagnostics); err != nil {
		return fmt.Errorf("unmarshal target parse diagnostics: %w", err)
	}
	return nil
}

func mergeIntJSONSet(encoded string, value int) (string, error) {
	var values []int
	if err := json.Unmarshal([]byte(encoded), &values); err != nil {
		return "", err
	}
	values = mergeInts(values, value)
	merged, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	return string(merged), nil
}

func mergeStringJSONSet(encoded string, value string) (string, error) {
	var values []string
	if err := json.Unmarshal([]byte(encoded), &values); err != nil {
		return "", err
	}
	if value != "" {
		values = append(values, value)
	}
	values = mergeStrings(nil, values...)
	merged, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	return string(merged), nil
}

func mergeInts(existing []int, additions ...int) []int {
	set := make(map[int]struct{}, len(existing)+len(additions))
	for _, value := range existing {
		set[value] = struct{}{}
	}
	for _, value := range additions {
		set[value] = struct{}{}
	}
	merged := make([]int, 0, len(set))
	for value := range set {
		merged = append(merged, value)
	}
	sort.Ints(merged)
	return merged
}

func mergeStrings(existing []string, additions ...string) []string {
	set := make(map[string]struct{}, len(existing)+len(additions))
	for _, value := range existing {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	for _, value := range additions {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	merged := make([]string, 0, len(set))
	for value := range set {
		merged = append(merged, value)
	}
	sort.Strings(merged)
	return merged
}

var _ TargetStore = (*SQLiteStore)(nil)
