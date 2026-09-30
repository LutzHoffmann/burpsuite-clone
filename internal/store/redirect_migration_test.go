package store

import (
	"database/sql"
	"testing"
)

func TestActiveCheckMigrationsPreserveObservations(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE active_check_runs (id INTEGER PRIMARY KEY);
INSERT INTO active_check_runs(id) VALUES (1);
CREATE TABLE active_check_observations (
 run_id INTEGER NOT NULL REFERENCES active_check_runs(id) ON DELETE CASCADE,
 url TEXT NOT NULL, source TEXT NOT NULL CHECK(source IN ('query','get_form')),
 parameter TEXT NOT NULL, status INTEGER NOT NULL, found INTEGER NOT NULL,
 context TEXT NOT NULL CHECK(context IN ('unknown','plain_text','html_text','html_attribute','raw_text')),
 partial INTEGER NOT NULL, error TEXT NOT NULL,
 PRIMARY KEY(run_id,url,source,parameter)
);
INSERT INTO active_check_observations VALUES (1,'https://example.test/','query','next',200,1,'plain_text',0,'');`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := applyRedirectCheckSchema(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var original string
	if err := db.QueryRow(`SELECT context FROM active_check_observations WHERE source='query'`).Scan(&original); err != nil || original != "plain_text" {
		t.Fatalf("original context=%q, err=%v", original, err)
	}
	if _, err := db.Exec(`INSERT INTO active_check_observations VALUES (1,'https://example.test/','redirect_query','next',302,1,'redirect_location',0,'')`); err != nil {
		t.Fatal(err)
	}
	tx, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := applyCORSCheckSchema(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM active_check_observations`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("preserved observations=%d, err=%v", count, err)
	}
	if _, err := db.Exec(`INSERT INTO active_check_observations VALUES (1,'https://example.test/','cors','Origin',200,1,'cors_credentials',0,'')`); err != nil {
		t.Fatal(err)
	}
}
