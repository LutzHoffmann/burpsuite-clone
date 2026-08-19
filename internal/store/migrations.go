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
