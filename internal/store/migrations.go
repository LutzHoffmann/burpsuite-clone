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
