package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
)

func TestResponseAuditRoundTrip(t *testing.T) {
	sqlite, err := OpenSQLite(filepath.Join(t.TempDir(), "audit.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlite.Close()
	for name, st := range map[string]Store{"sqlite": sqlite, "memory": NewMemoryForTests()} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			ex := &Exchange{AppliedRuleIDs: []string{"first", "second", "first"}, ResponseIntercepted: true}
			if err := st.SaveExchange(ctx, ex); err != nil {
				t.Fatal(err)
			}
			ex.AppliedRuleIDs[0] = "mutated"
			got, err := st.GetExchange(ctx, ex.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"first", "second", "first"}
			if !got.ResponseIntercepted || !reflect.DeepEqual(got.AppliedRuleIDs, want) {
				t.Fatalf("audit = %+v", got)
			}
			got.AppliedRuleIDs[0] = "mutated again"
			history, err := st.ListHistory(ctx, HistoryFilter{})
			if err != nil || len(history) != 1 || !history[0].ResponseIntercepted || !reflect.DeepEqual(history[0].AppliedRuleIDs, want) {
				t.Fatalf("history audit = %+v, %v", history, err)
			}
			history[0].AppliedRuleIDs[0] = "mutated history"
			got, err = st.GetExchange(ctx, ex.ID)
			if err != nil || !reflect.DeepEqual(got.AppliedRuleIDs, want) {
				t.Fatalf("read mutated saved audit: %+v, %v", got, err)
			}
			if pages, ok := st.(RebuildHistoryStore); ok {
				page, err := pages.ListExchangesPage(ctx, 0, ex.ID, 10)
				if err != nil || len(page) != 1 || !page[0].ResponseIntercepted || !reflect.DeepEqual(page[0].AppliedRuleIDs, want) {
					t.Fatalf("page = %+v, %v", page, err)
				}
			}
		})
	}
}

func TestResponseAuditMigratesLegacyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations {
		if migration.version >= 5 {
			break
		}
		if err := migration.apply(tx); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations VALUES (?)`, migration.version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO exchanges (id, method, scheme, host, path, query, status, mime_type, request_size, response_size, duration_ms, started_at_unix_nano, intercepted, error, error_message, request_truncated, response_truncated, tags_json, note) VALUES (1, 'GET', 'http', 'example.test', '/', '', 200, '', 0, 0, 0, 0, 0, 0, '', 0, 0, '[]', ''); INSERT INTO exchange_bodies (exchange_id, request_headers_json, response_headers_json) VALUES (1, '{}', '{}')`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	db.Close()
	st, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ex, err := st.GetExchange(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if ex.ResponseIntercepted || ex.AppliedRuleIDs == nil || len(ex.AppliedRuleIDs) != 0 {
		t.Fatalf("legacy audit = %+v", ex)
	}
}
