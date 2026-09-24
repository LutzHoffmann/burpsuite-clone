package store

import (
	"database/sql"
	"fmt"
)

type migration struct {
	version int
	apply   func(*sql.Tx) error
}

var migrations = []migration{
	{version: 1, apply: applyInitialSchema},
	{version: 2, apply: applyProjectAndRepeaterSchema},
	{version: 3, apply: applyTargetScopeSchema},
	{version: 4, apply: applyTargetProjectionSchema},
	{version: 5, apply: applyResponseInterceptSchema},
	{version: 6, apply: applyCaptureQuotaSchema},
	{version: 7, apply: applyWebSocketSchema},
	{version: 8, apply: applyIntruderSchema},
	{version: 9, apply: applyActiveScanSchema},
}

func applyActiveScanSchema(tx *sql.Tx) error {
	_, err := tx.Exec(`
CREATE TABLE active_scan_runs (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 project_id INTEGER NOT NULL REFERENCES projects(id),
 exchange_id INTEGER NOT NULL REFERENCES exchanges(id) ON DELETE CASCADE,
 state TEXT NOT NULL CHECK(state IN ('running','completed','scope_revoked','cancelled','failed','interrupted')),
 stopped_reason TEXT NOT NULL DEFAULT '' CHECK(length(CAST(stopped_reason AS BLOB)) <= 64),
 probe_count INTEGER NOT NULL DEFAULT 0 CHECK(probe_count BETWEEN 0 AND 5),
 started_at_unix_nano INTEGER NOT NULL,
 finished_at_unix_nano INTEGER
);
CREATE INDEX active_scan_runs_project_id ON active_scan_runs(project_id,id DESC);
CREATE TABLE active_scan_probes (
 run_id INTEGER NOT NULL REFERENCES active_scan_runs(id) ON DELETE CASCADE,
 sequence INTEGER NOT NULL CHECK(sequence BETWEEN 0 AND 4),
 parameter TEXT NOT NULL CHECK(length(CAST(parameter AS BLOB)) BETWEEN 1 AND 256),
 status INTEGER NOT NULL CHECK(status BETWEEN 0 AND 999),
 reflected INTEGER NOT NULL CHECK(reflected IN (0,1)),
 partial INTEGER NOT NULL CHECK(partial IN (0,1)),
 error TEXT NOT NULL CHECK(length(CAST(error AS BLOB)) <= 64),
 PRIMARY KEY(run_id,sequence)
);`)
	return err
}

func applyIntruderSchema(tx *sql.Tx) error {
	_, err := tx.Exec(`
CREATE TABLE intruder_jobs (
 id TEXT PRIMARY KEY CHECK(length(CAST(id AS BLOB)) BETWEEN 1 AND 128),
 project_id INTEGER NOT NULL REFERENCES projects(id),
 attack TEXT NOT NULL CHECK(attack IN ('sniper','battering_ram','pitchfork','cluster_bomb')),
 state TEXT NOT NULL CHECK(state IN ('draft','running','pausing','paused','aborting','aborted','completed','failed')),
 state_reason TEXT NOT NULL CHECK(length(CAST(state_reason AS BLOB)) <= 1024),
 revision INTEGER NOT NULL CHECK(revision >= 1),
 method TEXT NOT NULL CHECK(length(CAST(method AS BLOB)) BETWEEN 1 AND 32),
 url TEXT NOT NULL CHECK(length(CAST(url AS BLOB)) BETWEEN 1 AND 65536),
 template_raw BLOB NOT NULL CHECK(length(template_raw) <= 2097152),
 request_limit INTEGER NOT NULL CHECK(request_limit BETWEEN 1 AND 100000),
 concurrency INTEGER NOT NULL CHECK(concurrency BETWEEN 1 AND 20),
 rate_micros INTEGER NOT NULL CHECK(rate_micros BETWEEN 100000 AND 100000000),
 timeout_ms INTEGER NOT NULL CHECK(timeout_ms BETWEEN 1000 AND 120000),
 total_requests INTEGER NOT NULL CHECK(total_requests BETWEEN 1 AND 100000),
 next_sequence INTEGER NOT NULL CHECK(next_sequence BETWEEN 0 AND total_requests),
 completed_count INTEGER NOT NULL CHECK(completed_count BETWEEN 0 AND total_requests),
 error_count INTEGER NOT NULL CHECK(error_count BETWEEN 0 AND completed_count),
 scope_version INTEGER NOT NULL CHECK(scope_version >= 0),
 baseline_sequence INTEGER,
 created_at_unix_nano INTEGER NOT NULL,
 updated_at_unix_nano INTEGER NOT NULL
);
CREATE INDEX intruder_jobs_project_updated ON intruder_jobs(project_id, updated_at_unix_nano DESC);
CREATE INDEX intruder_jobs_project_state ON intruder_jobs(project_id, state);

CREATE TABLE intruder_payload_sets (
 job_id TEXT NOT NULL REFERENCES intruder_jobs(id) ON DELETE CASCADE,
 id TEXT NOT NULL CHECK(length(CAST(id AS BLOB)) BETWEEN 1 AND 128),
 set_order INTEGER NOT NULL CHECK(set_order >= 0),
 PRIMARY KEY(job_id, id),
 UNIQUE(job_id, set_order)
);
CREATE TABLE intruder_payloads (
 job_id TEXT NOT NULL,
 set_id TEXT NOT NULL,
 payload_index INTEGER NOT NULL CHECK(payload_index >= 0),
 payload BLOB NOT NULL CHECK(length(payload) <= 1048576),
 PRIMARY KEY(job_id, set_id, payload_index),
 FOREIGN KEY(job_id, set_id) REFERENCES intruder_payload_sets(job_id, id) ON DELETE CASCADE
);
CREATE TABLE intruder_positions (
 job_id TEXT NOT NULL REFERENCES intruder_jobs(id) ON DELETE CASCADE,
 id TEXT NOT NULL CHECK(length(CAST(id AS BLOB)) BETWEEN 1 AND 128),
 position_order INTEGER NOT NULL CHECK(position_order >= 0),
 start_offset INTEGER NOT NULL CHECK(start_offset >= 0),
 end_offset INTEGER NOT NULL CHECK(end_offset > start_offset),
 payload_set_id TEXT NOT NULL,
 PRIMARY KEY(job_id, id),
 UNIQUE(job_id, position_order),
 FOREIGN KEY(job_id, payload_set_id) REFERENCES intruder_payload_sets(job_id, id)
);

CREATE TABLE intruder_results (
 job_id TEXT NOT NULL REFERENCES intruder_jobs(id) ON DELETE CASCADE,
 sequence INTEGER NOT NULL CHECK(sequence >= 0),
 selections_json TEXT NOT NULL CHECK(length(CAST(selections_json AS BLOB)) <= 2097152),
 method TEXT NOT NULL CHECK(length(CAST(method AS BLOB)) BETWEEN 1 AND 32),
 url TEXT NOT NULL CHECK(length(CAST(url AS BLOB)) BETWEEN 1 AND 65536),
 status INTEGER NOT NULL CHECK(status BETWEEN 0 AND 999),
 mime_type TEXT NOT NULL CHECK(length(CAST(mime_type AS BLOB)) <= 1024),
 request_size INTEGER NOT NULL CHECK(request_size >= 0),
 response_size INTEGER NOT NULL CHECK(response_size >= 0),
 duration_ms INTEGER NOT NULL CHECK(duration_ms >= 0),
 error_category TEXT NOT NULL CHECK(length(CAST(error_category AS BLOB)) <= 64),
 response_truncated INTEGER NOT NULL CHECK(response_truncated IN (0,1)),
 request_capture BLOB,
 response_capture BLOB,
 body_stored INTEGER NOT NULL CHECK(body_stored IN (0,1)),
 storage_status TEXT NOT NULL CHECK(length(CAST(storage_status AS BLOB)) <= 64),
 similarity INTEGER NOT NULL CHECK(similarity BETWEEN 0 AND 10000),
 similarity_partial INTEGER NOT NULL CHECK(similarity_partial IN (0,1)),
 status_diff INTEGER NOT NULL CHECK(status_diff IN (0,1)),
 length_delta INTEGER NOT NULL,
 duration_delta INTEGER NOT NULL,
 mime_diff INTEGER NOT NULL CHECK(mime_diff IN (0,1)),
 capture_bytes INTEGER NOT NULL CHECK(capture_bytes >= 0),
 created_at_unix_nano INTEGER NOT NULL,
 PRIMARY KEY(job_id, sequence)
);
CREATE INDEX intruder_results_status ON intruder_results(job_id, status, sequence);
CREATE INDEX intruder_results_size ON intruder_results(job_id, response_size, sequence);
CREATE INDEX intruder_results_duration ON intruder_results(job_id, duration_ms, sequence);
CREATE INDEX intruder_results_similarity ON intruder_results(job_id, similarity, sequence);
`)
	return err
}

func applyWebSocketSchema(tx *sql.Tx) error {
	_, err := tx.Exec(`
 CREATE TABLE ws_connections (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  url TEXT NOT NULL CHECK(length(CAST(url AS BLOB)) <= 65536),
  in_scope INTEGER NOT NULL CHECK(in_scope IN (0,1)),
  opened_at_unix_nano INTEGER NOT NULL,
  closed_at_unix_nano INTEGER,
  state TEXT NOT NULL CHECK(state IN ('open','closed','interrupted')),
  gaps INTEGER NOT NULL DEFAULT 0 CHECK(gaps >= 0),
  capture_incomplete INTEGER NOT NULL DEFAULT 0 CHECK(capture_incomplete IN (0,1)),
  capture_bytes INTEGER NOT NULL CHECK(capture_bytes >= 0)
 );
 CREATE TABLE ws_messages (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  connection_id INTEGER NOT NULL REFERENCES ws_connections(id),
  sequence INTEGER NOT NULL CHECK(sequence >= 0),
  direction TEXT NOT NULL CHECK(length(CAST(direction AS BLOB)) BETWEEN 1 AND 64),
  observed_at_unix_nano INTEGER NOT NULL,
  type TEXT NOT NULL CHECK(length(CAST(type AS BLOB)) BETWEEN 1 AND 32),
  size INTEGER NOT NULL CHECK(size >= 0),
  payload BLOB,
  truncated INTEGER NOT NULL CHECK(truncated IN (0,1)),
  complete INTEGER NOT NULL CHECK(complete IN (0,1)),
  encoding TEXT NOT NULL CHECK(length(CAST(encoding AS BLOB)) <= 64),
  capture_bytes INTEGER NOT NULL CHECK(capture_bytes >= 0),
  UNIQUE(connection_id, sequence)
 );
 CREATE INDEX ws_messages_connection_id ON ws_messages(connection_id, id DESC);
 CREATE INDEX ws_connections_open ON ws_connections(id) WHERE state = 'open';`)
	return err
}

func applyCaptureQuotaSchema(tx *sql.Tx) error {
	// BLOB casts measure UTF-8 bytes (including embedded NULs), not characters.
	// Aggregate in SQLite so migrating retained bodies does not load them into Go.
	_, err := tx.Exec(`
		ALTER TABLE exchanges ADD COLUMN capture_bytes INTEGER NOT NULL DEFAULT 0 CHECK(capture_bytes >= 0);
		ALTER TABLE repeater_sends ADD COLUMN capture_bytes INTEGER NOT NULL DEFAULT 0 CHECK(capture_bytes >= 0);
		UPDATE exchanges SET capture_bytes = 256
			+ length(CAST(method AS BLOB)) + length(CAST(scheme AS BLOB))
			+ length(CAST(host AS BLOB)) + length(CAST(path AS BLOB))
			+ length(CAST(query AS BLOB)) + length(CAST(mime_type AS BLOB))
			+ length(CAST(error_message AS BLOB)) + length(CAST(tags_json AS BLOB))
			+ length(CAST(note AS BLOB)) + length(CAST(applied_rule_ids_json AS BLOB))
			+ COALESCE((SELECT length(CAST(request_headers_json AS BLOB))
				+ length(CAST(response_headers_json AS BLOB))
				+ COALESCE(length(request_body), 0) + COALESCE(length(request_raw), 0)
				+ COALESCE(length(response_body), 0) + COALESCE(length(response_raw), 0)
				FROM exchange_bodies WHERE exchange_id = exchanges.id), 0);
		UPDATE repeater_sends SET capture_bytes = 256
			+ length(CAST(session_id AS BLOB)) + length(CAST(method AS BLOB))
			+ length(CAST(url AS BLOB)) + length(CAST(request_headers_json AS BLOB))
			+ length(CAST(request_body AS BLOB)) + length(CAST(response_headers_json AS BLOB))
			+ length(CAST(response_body AS BLOB)) + length(CAST(content_type AS BLOB));
		CREATE TABLE capture_quota (
			revision INTEGER NOT NULL DEFAULT 0 CHECK(revision >= 0),
			project_id INTEGER PRIMARY KEY REFERENCES projects(id) CHECK(project_id = 1),
			limit_bytes INTEGER NOT NULL CHECK(limit_bytes > 0 AND limit_bytes <= 1099511627776),
			used_bytes INTEGER NOT NULL CHECK(used_bytes >= 0),
			paused INTEGER NOT NULL CHECK(paused IN (0, 1)),
			skipped_records INTEGER NOT NULL CHECK(skipped_records >= 0)
		);
		INSERT INTO capture_quota(project_id, limit_bytes, used_bytes, paused, skipped_records)
		SELECT 1, 1073741824, used, used >= 1073741824, 0 FROM (
			SELECT COALESCE((SELECT SUM(capture_bytes) FROM exchanges WHERE project_id = 1), 0)
			+ COALESCE((SELECT SUM(h.capture_bytes) FROM repeater_sends h JOIN repeater_sessions s
				ON s.id = h.session_id WHERE s.project_id = 1), 0) AS used
		);`)
	return err
}

func applyResponseInterceptSchema(tx *sql.Tx) error {
	_, err := tx.Exec(`
		ALTER TABLE exchanges ADD COLUMN applied_rule_ids_json TEXT NOT NULL DEFAULT '[]';
		ALTER TABLE exchanges ADD COLUMN response_intercepted INTEGER NOT NULL DEFAULT 0;`)
	return err
}

func applyTargetProjectionSchema(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE target_generations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			scope_version INTEGER NOT NULL,
			status TEXT NOT NULL CHECK(status IN ('building', 'active', 'retired', 'failed', 'cancelled')),
			processed INTEGER NOT NULL,
			total INTEGER NOT NULL,
			error TEXT NOT NULL,
			started_at_unix_nano INTEGER NOT NULL,
			completed_at_unix_nano INTEGER
		);
		ALTER TABLE scope_state ADD COLUMN active_target_generation_id INTEGER REFERENCES target_generations(id);
		CREATE TABLE target_endpoints (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			generation_id INTEGER NOT NULL REFERENCES target_generations(id) ON DELETE CASCADE,
			scheme TEXT NOT NULL,
			host TEXT NOT NULL,
			port INTEGER NOT NULL,
			path TEXT NOT NULL,
			method TEXT NOT NULL,
			first_seen_unix_nano INTEGER NOT NULL,
			last_seen_unix_nano INTEGER NOT NULL,
			observation_count INTEGER NOT NULL,
			latest_exchange_id INTEGER NOT NULL REFERENCES exchanges(id),
			statuses_json TEXT NOT NULL,
			request_mimes_json TEXT NOT NULL,
			response_mimes_json TEXT NOT NULL,
			parse_diagnostics_json TEXT NOT NULL,
			error_seen INTEGER NOT NULL,
			UNIQUE(generation_id, scheme, host, port, path, method)
		);
		CREATE TABLE target_endpoint_exchanges (
			endpoint_id INTEGER NOT NULL REFERENCES target_endpoints(id) ON DELETE CASCADE,
			exchange_id INTEGER NOT NULL REFERENCES exchanges(id) ON DELETE CASCADE,
			PRIMARY KEY(endpoint_id, exchange_id)
		);
		CREATE TABLE target_parameters (
			endpoint_id INTEGER NOT NULL REFERENCES target_endpoints(id) ON DELETE CASCADE,
			location TEXT NOT NULL,
			name TEXT NOT NULL,
			value_type TEXT NOT NULL,
			first_seen_unix_nano INTEGER NOT NULL,
			last_seen_unix_nano INTEGER NOT NULL,
			observation_count INTEGER NOT NULL,
			PRIMARY KEY(endpoint_id, location, name, value_type)
		);
		CREATE INDEX target_generations_project_id ON target_generations(project_id, id DESC);
		CREATE INDEX target_endpoints_generation_id ON target_endpoints(generation_id, id);
		CREATE INDEX target_endpoint_exchanges_exchange_id ON target_endpoint_exchanges(exchange_id);`)
	return err
}

func applyTargetScopeSchema(tx *sql.Tx) error {
	_, err := tx.Exec(`
		ALTER TABLE exchanges ADD COLUMN in_scope INTEGER NOT NULL DEFAULT 0;
		ALTER TABLE exchanges ADD COLUMN scope_version INTEGER NOT NULL DEFAULT 0;
		ALTER TABLE exchanges ADD COLUMN scope_rule_id INTEGER;

		CREATE TABLE scope_state (
			project_id INTEGER PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
			version INTEGER NOT NULL
		);
		INSERT INTO scope_state(project_id, version) SELECT id, 0 FROM projects;

		CREATE TABLE scope_rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			enabled INTEGER NOT NULL,
			action TEXT NOT NULL CHECK(action IN ('include', 'exclude')),
			scheme TEXT NOT NULL,
			host_pattern TEXT NOT NULL,
			port INTEGER NOT NULL,
			path_prefix TEXT NOT NULL,
			position INTEGER NOT NULL
		);
		CREATE INDEX scope_rules_project_position ON scope_rules(project_id, position);`)
	return err
}

func applyProjectAndRepeaterSchema(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS projects (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			created_at_unix_nano INTEGER NOT NULL
		);
		INSERT OR IGNORE INTO projects (id, name, created_at_unix_nano)
			VALUES (1, 'Default Project', 0);
		ALTER TABLE exchanges ADD COLUMN project_id INTEGER NOT NULL DEFAULT 1;

		CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS repeater_sessions (
			id TEXT PRIMARY KEY,
			project_id INTEGER NOT NULL REFERENCES projects(id),
			name TEXT NOT NULL,
			created_at_unix_nano INTEGER NOT NULL,
			updated_at_unix_nano INTEGER NOT NULL
		);

		CREATE TABLE IF NOT EXISTS repeater_sends (
			id INTEGER PRIMARY KEY,
			session_id TEXT NOT NULL REFERENCES repeater_sessions(id) ON DELETE CASCADE,
			method TEXT NOT NULL,
			url TEXT NOT NULL,
			request_headers_json TEXT NOT NULL,
			request_body TEXT NOT NULL,
			status INTEGER NOT NULL,
			response_headers_json TEXT NOT NULL,
			response_body TEXT NOT NULL,
			duration_ms INTEGER NOT NULL,
			size INTEGER NOT NULL,
			truncated INTEGER NOT NULL,
			content_type TEXT NOT NULL,
			sent_at_unix_nano INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS repeater_sends_session_id
			ON repeater_sends(session_id, id DESC);`)
	return err
}

func applyMigrations(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin migrations: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY
		)`); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}

	for _, migration := range migrations {
		var version int
		err := tx.QueryRow("SELECT version FROM schema_migrations WHERE version = ?", migration.version).Scan(&version)
		if err == nil {
			continue
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("check migration %d: %w", migration.version, err)
		}
		if err := migration.apply(tx); err != nil {
			return fmt.Errorf("apply migration %d: %w", migration.version, err)
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations (version) VALUES (?)", migration.version); err != nil {
			return fmt.Errorf("record migration %d: %w", migration.version, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	return nil
}

func applyInitialSchema(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS exchanges (
			id INTEGER PRIMARY KEY,
			method TEXT NOT NULL,
			scheme TEXT NOT NULL,
			host TEXT NOT NULL,
			path TEXT NOT NULL,
			query TEXT NOT NULL,
			status INTEGER NOT NULL,
			mime_type TEXT NOT NULL,
			request_size INTEGER NOT NULL,
			response_size INTEGER NOT NULL,
			duration_ms INTEGER NOT NULL,
			started_at_unix_nano INTEGER NOT NULL,
			intercepted INTEGER NOT NULL,
			error INTEGER NOT NULL,
			error_message TEXT NOT NULL,
			request_truncated INTEGER NOT NULL,
			response_truncated INTEGER NOT NULL,
			tags_json TEXT NOT NULL,
			note TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS exchange_bodies (
			exchange_id INTEGER PRIMARY KEY REFERENCES exchanges(id) ON DELETE CASCADE,
			request_headers_json TEXT NOT NULL,
			request_body BLOB,
			request_raw BLOB,
			response_headers_json TEXT NOT NULL,
			response_body BLOB,
			response_raw BLOB
		);`)
	return err
}
