package store

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/scope"
)

func openTestStore(t *testing.T) *SQLiteStore {
	t.Helper()

	store, err := OpenSQLite(filepath.Join(t.TempDir(), "project.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return store
}

func TestSQLiteMigratesVersionFixturesToScopeSchema(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run("version-"+string(rune('0'+version)), func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "project.sqlite")
			createSchemaFixture(t, dbPath, version)

			store, err := OpenSQLite(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })

			var inScope, scopeVersion int64
			var scopeRuleID *int64
			if err := store.db.QueryRow(`SELECT in_scope, scope_version, scope_rule_id FROM exchanges WHERE id = 1`).Scan(&inScope, &scopeVersion, &scopeRuleID); err != nil {
				t.Fatal(err)
			}
			if inScope != 0 || scopeVersion != 0 || scopeRuleID != nil {
				t.Fatalf("legacy exchange scope = in_scope:%d scope_version:%d scope_rule_id:%v", inScope, scopeVersion, scopeRuleID)
			}

			state, err := store.LoadScopeState(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if state.Version != 0 || len(state.Rules) != 0 {
				t.Fatalf("scope state = %#v", state)
			}
		})
	}
}

func createSchemaFixture(t *testing.T, path string, version int) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err := applyInitialSchema(tx); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations (version) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	if version == 2 {
		if err := applyProjectAndRepeaterSchema(tx); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version) VALUES (2)`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO exchanges (id, method, scheme, host, path, query, status, mime_type, request_size, response_size, duration_ms, started_at_unix_nano, intercepted, error, error_message, request_truncated, response_truncated, tags_json, note) VALUES (1, '', '', '', '', '', 0, '', 0, 0, 0, 0, 0, 0, '', 0, 0, '[]', '')`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteScopeRulesReplaceAtomically(t *testing.T) {
	store := openTestStore(t)
	state, err := store.ReplaceScopeRules(context.Background(), 0, []scope.Rule{{
		Enabled: true, Action: scope.ActionInclude, Scheme: "https", HostPattern: "example.test", PathPrefix: "/api",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if state.Version != 1 || len(state.Rules) != 1 || state.Rules[0].ID == 0 {
		t.Fatalf("state = %#v", state)
	}
	if _, err := store.ReplaceScopeRules(context.Background(), 0, nil); !errors.Is(err, ErrScopeVersionConflict) {
		t.Fatalf("conflict error = %v", err)
	}
	loaded, err := store.LoadScopeState(context.Background())
	if err != nil || loaded.Version != 1 || len(loaded.Rules) != 1 {
		t.Fatalf("loaded = %#v, err = %v", loaded, err)
	}
}

func TestSQLiteHistoryScopeRoundTrip(t *testing.T) {
	store := openTestStore(t)
	ruleID := int64(41)
	exchange := &Exchange{InScope: true, ScopeVersion: 7, ScopeRuleID: &ruleID}
	if err := store.SaveExchange(context.Background(), exchange); err != nil {
		t.Fatal(err)
	}
	withoutRule := &Exchange{InScope: false, ScopeVersion: 7}
	if err := store.SaveExchange(context.Background(), withoutRule); err != nil {
		t.Fatal(err)
	}

	history, err := store.ListHistory(context.Background(), HistoryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].ScopeRuleID != nil || !history[1].InScope || history[1].ScopeVersion != 7 || history[1].ScopeRuleID == nil || *history[1].ScopeRuleID != ruleID {
		t.Fatalf("history = %#v", history)
	}

	loaded, err := store.GetExchange(context.Background(), exchange.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.InScope || loaded.ScopeVersion != 7 || loaded.ScopeRuleID == nil || *loaded.ScopeRuleID != ruleID {
		t.Fatalf("loaded = %#v", loaded)
	}
}

func TestSQLiteListExchangesPageReturnsAscendingIDsThroughBoundary(t *testing.T) {
	store := openTestStore(t)
	for i := 0; i < 4; i++ {
		exchange := &Exchange{}
		if i == 0 {
			exchange.Request.Body = []byte("request-body")
			exchange.Response.Body = []byte("response-body")
		}
		if err := store.SaveExchange(context.Background(), exchange); err != nil {
			t.Fatal(err)
		}
	}
	latestID, err := store.LatestExchangeID(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if latestID != 4 {
		t.Fatalf("latest ID = %d", latestID)
	}
	count, err := store.CountExchangesThrough(context.Background(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("count through ID = %d", count)
	}

	page, err := store.ListExchangesPage(context.Background(), 0, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := exchangeIDs(page), []int64{1, 2}; !slices.Equal(got, want) {
		t.Fatalf("first page = %v, want %v", got, want)
	}
	if string(page[0].Request.Body) != "request-body" || string(page[0].Response.Body) != "response-body" {
		t.Fatalf("page exchange is incomplete: %#v", page[0])
	}

	page, err = store.ListExchangesPage(context.Background(), 2, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := exchangeIDs(page), []int64{3}; !slices.Equal(got, want) {
		t.Fatalf("second page = %v, want %v", got, want)
	}
}

func exchangeIDs(exchanges []Exchange) []int64 {
	ids := make([]int64, 0, len(exchanges))
	for _, exchange := range exchanges {
		ids = append(ids, exchange.ID)
	}
	return ids
}

func TestSQLiteStoreSavesAndListsExchange(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "project.sqlite")
	st, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ex := &Exchange{
		Method:    "GET",
		Scheme:    "https",
		Host:      "example.test",
		Path:      "/login",
		Query:     "next=/app",
		Status:    200,
		MIMEType:  "text/html",
		StartedAt: time.Unix(1700000000, 0).UTC(),
		Duration:  25 * time.Millisecond,
		Request: RequestData{
			Headers: map[string][]string{"User-Agent": {"test"}},
			Body:    []byte("request-body"),
		},
		Response: ResponseData{
			Headers: map[string][]string{"Content-Type": {"text/html"}},
			Body:    []byte("response-body"),
		},
	}

	if err := st.SaveExchange(context.Background(), ex); err != nil {
		t.Fatal(err)
	}
	if ex.ID == 0 {
		t.Fatal("expected generated id")
	}

	items, err := st.ListHistory(context.Background(), HistoryFilter{Search: "example"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d", len(items))
	}
	if items[0].Host != "example.test" || items[0].Status != 200 {
		t.Fatalf("unexpected item: %+v", items[0])
	}

	loaded, err := st.GetExchange(context.Background(), ex.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(loaded.Response.Body) != "response-body" {
		t.Fatalf("response body = %q", loaded.Response.Body)
	}
}

func TestSQLiteStoreCapsBodiesAndRawData(t *testing.T) {
	t.Setenv("BC_BODY_LIMIT_BYTES", "4")

	st, err := OpenSQLite(filepath.Join(t.TempDir(), "project.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ex := &Exchange{
		Method: "POST",
		Request: RequestData{
			Body: []byte("request-body"),
			Raw:  []byte("request-raw"),
		},
		Response: ResponseData{
			Body: []byte("response-body"),
			Raw:  []byte("response-raw"),
		},
	}
	if err := st.SaveExchange(context.Background(), ex); err != nil {
		t.Fatal(err)
	}

	loaded, err := st.GetExchange(context.Background(), ex.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.RequestTruncated || !loaded.ResponseTruncated {
		t.Fatalf("truncation flags = request:%t response:%t", loaded.RequestTruncated, loaded.ResponseTruncated)
	}
	if len(loaded.Request.Body) > 4 || len(loaded.Request.Raw) > 4 {
		t.Fatalf("request capture exceeds limit: body=%d raw=%d", len(loaded.Request.Body), len(loaded.Request.Raw))
	}
	if len(loaded.Response.Body) > 4 || len(loaded.Response.Raw) > 4 {
		t.Fatalf("response capture exceeds limit: body=%d raw=%d", len(loaded.Response.Body), len(loaded.Response.Raw))
	}
}

func TestOpenSQLiteRecordsAppliedMigrations(t *testing.T) {
	st, err := OpenSQLite(filepath.Join(t.TempDir(), "project.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	var count int
	if err := st.db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatal("expected at least one applied migration")
	}
}

func TestSQLitePersistsRepeaterSessionAndSendHistory(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "project.sqlite")
	st, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	record := &RepeaterSend{
		SessionID: "session-1", Method: "POST", URL: "https://example.test/send",
		RequestHeaders: http.Header{"Content-Type": {"text/plain"}}, RequestBody: "request",
		Status: 202, ResponseHeaders: http.Header{"Content-Type": {"text/plain"}}, ResponseBody: "accepted",
		DurationMS: 15, Size: 8, ContentType: "text/plain", SentAt: time.Unix(1700000000, 0).UTC(),
	}
	if err := st.SaveRepeaterSend(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if record.ID == 0 {
		t.Fatal("expected generated send id")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	sessions, err := reopened.ListRepeaterSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != "session-1" || sessions[0].SendCount != 1 {
		t.Fatalf("sessions = %+v", sessions)
	}
	sends, err := reopened.ListRepeaterSends(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sends) != 1 || sends[0].RequestBody != "request" || sends[0].ResponseBody != "accepted" {
		t.Fatalf("sends = %+v", sends)
	}
}

func TestSQLiteProvidesDefaultProjectAndPersistentSettings(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "project.sqlite")
	st, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	project, err := st.ActiveProject(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if project.ID == 0 || project.Name != "Default Project" {
		t.Fatalf("project = %+v", project)
	}
	if err := st.SetSetting(context.Background(), "intercept.enabled", "true"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	value, err := reopened.GetSetting(context.Background(), "intercept.enabled")
	if err != nil {
		t.Fatal(err)
	}
	if value != "true" {
		t.Fatalf("setting = %q", value)
	}
}
