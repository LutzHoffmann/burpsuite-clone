package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/scope"
	modernsqlite "modernc.org/sqlite"
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

func TestSQLiteScopeLoadStateUsesSingleSnapshotDuringReplacement(t *testing.T) {
	registerScopeSnapshotBarrier(t)
	store := openTestStore(t)
	ctx := context.Background()
	if _, err := store.ReplaceScopeRules(ctx, 0, []scope.Rule{{
		Enabled: true, Action: scope.ActionInclude, HostPattern: "scope-1.test",
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`PRAGMA journal_mode = WAL`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
		ALTER TABLE scope_state RENAME TO scope_state_backing;
		CREATE VIEW scope_state AS
			SELECT project_id, version + scope_snapshot_test_barrier() AS version
			FROM scope_state_backing;
		CREATE TRIGGER scope_state_update
		INSTEAD OF UPDATE ON scope_state
		BEGIN
			UPDATE scope_state_backing SET version = NEW.version WHERE project_id = OLD.project_id;
		END;`); err != nil {
		t.Fatal(err)
	}

	entered, release := activateScopeSnapshotBarrier(t)
	loaded := make(chan scope.State, 1)
	loadErr := make(chan error, 1)
	go func() {
		state, err := store.LoadScopeState(ctx)
		if err != nil {
			loadErr <- err
			return
		}
		loaded <- state
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("load did not read scope version")
	}

	replaced := make(chan error, 1)
	go func() {
		_, err := store.ReplaceScopeRules(ctx, 1, []scope.Rule{{
			Enabled: true, Action: scope.ActionInclude, HostPattern: "scope-2.test",
		}})
		replaced <- err
	}()
	select {
	case err := <-replaced:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("replacement did not commit while version read was paused")
	}
	close(release)

	select {
	case err := <-loadErr:
		t.Fatal(err)
	case state := <-loaded:
		if err := validateVersionedScopeState(state); err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("load did not finish")
	}
}

func validateVersionedScopeState(state scope.State) error {
	if len(state.Rules) != 1 {
		return fmt.Errorf("version %d has %d rules", state.Version, len(state.Rules))
	}
	if got, want := state.Rules[0].HostPattern, fmt.Sprintf("scope-%d.test", state.Version); got != want {
		return fmt.Errorf("version %d has host pattern %q, want %q", state.Version, got, want)
	}
	return nil
}

var (
	registerScopeSnapshotBarrierOnce sync.Once
	scopeSnapshotBarrier             struct {
		sync.Mutex
		entered chan<- struct{}
		release <-chan struct{}
		used    bool
	}
)

func registerScopeSnapshotBarrier(t *testing.T) {
	t.Helper()
	registerScopeSnapshotBarrierOnce.Do(func() {
		modernsqlite.MustRegisterScalarFunction("scope_snapshot_test_barrier", 0, func(*modernsqlite.FunctionContext, []driver.Value) (driver.Value, error) {
			scopeSnapshotBarrier.Lock()
			entered, release, used := scopeSnapshotBarrier.entered, scopeSnapshotBarrier.release, scopeSnapshotBarrier.used
			scopeSnapshotBarrier.used = true
			scopeSnapshotBarrier.Unlock()
			if !used && entered != nil {
				entered <- struct{}{}
				<-release
			}
			return int64(0), nil
		})
	})
}

func activateScopeSnapshotBarrier(t *testing.T) (<-chan struct{}, chan<- struct{}) {
	t.Helper()
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	scopeSnapshotBarrier.Lock()
	scopeSnapshotBarrier.entered = entered
	scopeSnapshotBarrier.release = release
	scopeSnapshotBarrier.used = false
	scopeSnapshotBarrier.Unlock()
	t.Cleanup(func() {
		scopeSnapshotBarrier.Lock()
		scopeSnapshotBarrier.entered = nil
		scopeSnapshotBarrier.release = nil
		scopeSnapshotBarrier.used = false
		scopeSnapshotBarrier.Unlock()
	})
	return entered, release
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

func TestSQLiteTargetGenerationActivatesAtomically(t *testing.T) {
	repository := openTestStore(t)
	ctx := context.Background()

	if tree, err := repository.ListTargetTree(ctx); err != nil || tree == nil || len(tree) != 0 {
		t.Fatalf("tree without active generation = %#v, err = %v", tree, err)
	}
	if _, err := repository.ActiveTargetGeneration(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("active generation error = %v, want sql.ErrNoRows", err)
	}

	generationID, err := repository.CreateTargetGeneration(ctx, 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.SetTargetGenerationProgress(ctx, generationID, 1); err != nil {
		t.Fatal(err)
	}
	latest, err := repository.LatestTargetGeneration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if latest.ID != generationID || latest.ScopeVersion != 4 || latest.Status != "building" || latest.Processed != 1 || latest.Total != 2 || latest.CompletedAt != nil {
		t.Fatalf("latest generation = %#v", latest)
	}

	observation := TargetObservation{
		Key:        TargetEndpointKey{Scheme: "https", Host: "example.test", Port: 443, Path: "/api", Method: "GET"},
		ExchangeID: saveTargetExchange(t, repository), StartedAt: time.Unix(1, 0).UTC(), Status: 200,
	}
	if err := repository.UpsertTargetObservation(ctx, generationID, observation); err != nil {
		t.Fatal(err)
	}
	if err := repository.ActivateTargetGeneration(ctx, generationID); err != nil {
		t.Fatal(err)
	}
	active, err := repository.ActiveTargetGeneration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if active.ID != generationID || active.Status != "active" || active.CompletedAt == nil {
		t.Fatalf("active generation = %#v", active)
	}
	tree, err := repository.ListTargetTree(ctx)
	if err != nil || len(tree) != 1 || tree[0].Host != "example.test" {
		t.Fatalf("tree = %#v, err = %v", tree, err)
	}

	replacementID, err := repository.CreateTargetGeneration(ctx, 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	replacement := observation
	replacement.Key.Path = "/replacement"
	replacement.ExchangeID = saveTargetExchange(t, repository)
	if err := repository.UpsertTargetObservation(ctx, replacementID, replacement); err != nil {
		t.Fatal(err)
	}
	tree, err = repository.ListTargetTree(ctx)
	if err != nil || tree[0].Children[0].Path != "api" {
		t.Fatalf("tree while replacement builds = %#v, err = %v", tree, err)
	}
	if err := repository.FailTargetGeneration(ctx, replacementID, "rebuild failed"); err != nil {
		t.Fatal(err)
	}
	if err := repository.ActivateTargetGeneration(ctx, replacementID); err == nil {
		t.Fatal("activated failed generation")
	}
	active, err = repository.ActiveTargetGeneration(ctx)
	if err != nil || active.ID != generationID {
		t.Fatalf("active after failed replacement = %#v, err = %v", active, err)
	}
	latest, err = repository.LatestTargetGeneration(ctx)
	if err != nil || latest.ID != replacementID || latest.Status != "failed" || latest.Error != "rebuild failed" || latest.CompletedAt == nil {
		t.Fatalf("latest failed generation = %#v, err = %v", latest, err)
	}

	cancelledID, err := repository.CreateTargetGeneration(ctx, 6, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CancelTargetGeneration(ctx, cancelledID); err != nil {
		t.Fatal(err)
	}
	latest, err = repository.LatestTargetGeneration(ctx)
	if err != nil || latest.Status != "cancelled" || latest.CompletedAt == nil {
		t.Fatalf("latest cancelled generation = %#v, err = %v", latest, err)
	}
}

func TestSQLiteTargetActivationRollsBackEveryStateChange(t *testing.T) {
	repository := openTestStore(t)
	ctx := context.Background()
	firstID, firstEndpointID := saveAndActivateTarget(t, repository, 1, "/first")

	replacementID, err := repository.CreateTargetGeneration(ctx, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.db.Exec(fmt.Sprintf(`
		CREATE TRIGGER reject_target_activation
		BEFORE UPDATE OF status ON target_generations
		WHEN NEW.id = %d AND NEW.status = 'active'
		BEGIN SELECT RAISE(ABORT, 'activation rejected'); END`, replacementID)); err != nil {
		t.Fatal(err)
	}
	if err := repository.ActivateTargetGeneration(ctx, replacementID); err == nil {
		t.Fatal("activation unexpectedly succeeded")
	}

	active, err := repository.ActiveTargetGeneration(ctx)
	if err != nil || active.ID != firstID || active.Status != "active" {
		t.Fatalf("active generation after rollback = %#v, err = %v", active, err)
	}
	var replacementStatus string
	if err := repository.db.QueryRow(`SELECT status FROM target_generations WHERE id = ?`, replacementID).Scan(&replacementStatus); err != nil {
		t.Fatal(err)
	}
	if replacementStatus != "building" {
		t.Fatalf("replacement status after rollback = %q", replacementStatus)
	}
	if _, err := repository.GetTargetEndpoint(ctx, firstEndpointID); err != nil {
		t.Fatalf("old endpoint unreadable after rollback: %v", err)
	}
}

func TestSQLiteTargetObservationDeduplicatesAndAggregatesValueFreeMetadata(t *testing.T) {
	repository := openTestStore(t)
	ctx := context.Background()
	generationID, err := repository.CreateTargetGeneration(ctx, 7, 2)
	if err != nil {
		t.Fatal(err)
	}
	firstExchangeID := saveTargetExchangeAt(t, repository, time.Unix(10, 0), 500, true)
	secondExchangeID := saveTargetExchangeAt(t, repository, time.Unix(20, 0), 200, false)
	key := TargetEndpointKey{Scheme: "https", Host: "example.test", Port: 443, Path: "/items", Method: "POST"}
	first := TargetObservation{
		Key: key, ExchangeID: firstExchangeID, StartedAt: time.Unix(10, 0).UTC(), Status: 500,
		RequestMIME: "text/plain", ResponseMIME: "application/json", Error: true,
		ParseDiagnostic: "json_malformed",
		Parameters: []TargetParameter{
			{Location: "query", Name: "page", ValueType: "string", Count: 99, FirstSeen: time.Unix(90, 0), LastSeen: time.Unix(99, 0)},
			{Location: "query", Name: "page", ValueType: "string"},
		},
	}
	if err := repository.UpsertTargetObservation(ctx, generationID, first); err != nil {
		t.Fatal(err)
	}
	duplicate := first
	duplicate.Status = 201
	duplicate.RequestMIME = "should/not-be-added"
	duplicate.ParseDiagnostic = "json_malformed: raw-secret-value"
	if err := repository.UpsertTargetObservation(ctx, generationID, duplicate); err != nil {
		t.Fatal(err)
	}
	second := TargetObservation{
		Key: key, ExchangeID: secondExchangeID, StartedAt: time.Unix(20, 0).UTC(), Status: 200,
		RequestMIME: "application/json", ResponseMIME: "text/html",
		Parameters: []TargetParameter{
			{Location: "query", Name: "page", ValueType: "string"},
			{Location: "cookie", Name: "session", ValueType: "string"},
		},
	}
	if err := repository.UpsertTargetObservation(ctx, generationID, second); err != nil {
		t.Fatal(err)
	}
	if err := repository.ActivateTargetGeneration(ctx, generationID); err != nil {
		t.Fatal(err)
	}

	var endpointID, endpointCount int64
	var diagnosticJSON string
	if err := repository.db.QueryRow(`
		SELECT id, observation_count, parse_diagnostics_json
		FROM target_endpoints WHERE generation_id = ?`, generationID).Scan(&endpointID, &endpointCount, &diagnosticJSON); err != nil {
		t.Fatal(err)
	}
	if endpointCount != 2 {
		t.Fatalf("endpoint observation count = %d, want 2", endpointCount)
	}
	if diagnosticJSON != `["json_malformed"]` {
		t.Fatalf("diagnostics JSON = %s", diagnosticJSON)
	}
	var referenceCount int64
	if err := repository.db.QueryRow(`SELECT COUNT(*) FROM target_endpoint_exchanges WHERE endpoint_id = ?`, endpointID).Scan(&referenceCount); err != nil {
		t.Fatal(err)
	}
	if referenceCount != 2 {
		t.Fatalf("endpoint exchange count = %d, want 2", referenceCount)
	}

	endpoint, err := repository.GetTargetEndpoint(ctx, endpointID)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.Count != 2 || !endpoint.ErrorSeen || endpoint.FirstSeen != time.Unix(10, 0).UTC() || endpoint.LastSeen != time.Unix(20, 0).UTC() || endpoint.LatestExchangeID != secondExchangeID {
		t.Fatalf("endpoint aggregate = %#v", endpoint)
	}
	if !slices.Equal(endpoint.Statuses, []int{200, 500}) || !slices.Equal(endpoint.RequestMIMEs, []string{"application/json", "text/plain"}) || !slices.Equal(endpoint.ResponseMIMEs, []string{"application/json", "text/html"}) || !slices.Equal(endpoint.ParseDiagnostics, []string{"json_malformed"}) {
		t.Fatalf("endpoint sets = statuses:%v request:%v response:%v diagnostics:%v", endpoint.Statuses, endpoint.RequestMIMEs, endpoint.ResponseMIMEs, endpoint.ParseDiagnostics)
	}

	parameters, err := repository.ListTargetParameters(ctx, endpointID)
	if err != nil {
		t.Fatal(err)
	}
	wantParameters := []TargetParameter{
		{Location: "cookie", Name: "session", ValueType: "string", FirstSeen: time.Unix(20, 0).UTC(), LastSeen: time.Unix(20, 0).UTC(), Count: 1},
		{Location: "query", Name: "page", ValueType: "string", FirstSeen: time.Unix(10, 0).UTC(), LastSeen: time.Unix(20, 0).UTC(), Count: 2},
	}
	if !slices.Equal(parameters, wantParameters) {
		t.Fatalf("parameters = %#v, want %#v", parameters, wantParameters)
	}
	assertTargetParametersHaveNoValueColumn(t, repository)

	requests, err := repository.ListTargetRequests(ctx, endpointID)
	if err != nil {
		t.Fatal(err)
	}
	wantRequests := []TargetRequestRef{
		{ExchangeID: secondExchangeID, StartedAt: time.Unix(20, 0).UTC(), Status: 200, Error: false},
		{ExchangeID: firstExchangeID, StartedAt: time.Unix(10, 0).UTC(), Status: 500, Error: true},
	}
	if !slices.Equal(requests, wantRequests) {
		t.Fatalf("requests = %#v, want %#v", requests, wantRequests)
	}
}

func TestSQLiteTargetConcurrentDuplicateObservationIsIdempotent(t *testing.T) {
	repository := openTestStore(t)
	ctx := context.Background()
	generationID, err := repository.CreateTargetGeneration(ctx, 9, 1)
	if err != nil {
		t.Fatal(err)
	}
	observation := TargetObservation{
		Key:        TargetEndpointKey{Scheme: "https", Host: "example.test", Port: 443, Path: "/concurrent", Method: "GET"},
		ExchangeID: saveTargetExchange(t, repository), StartedAt: time.Unix(9, 0), Status: 200,
		Parameters: []TargetParameter{{Location: "query", Name: "q", ValueType: "string"}},
	}

	const writers = 8
	start := make(chan struct{})
	errorsByWriter := make(chan error, writers)
	var writersDone sync.WaitGroup
	writersDone.Add(writers)
	for range writers {
		go func() {
			defer writersDone.Done()
			<-start
			errorsByWriter <- repository.UpsertTargetObservation(ctx, generationID, observation)
		}()
	}
	close(start)
	writersDone.Wait()
	close(errorsByWriter)
	for err := range errorsByWriter {
		if err != nil {
			t.Errorf("concurrent upsert: %v", err)
		}
	}

	var endpointCount, parameterCount, referenceCount int64
	if err := repository.db.QueryRow(`SELECT observation_count FROM target_endpoints WHERE generation_id = ?`, generationID).Scan(&endpointCount); err != nil {
		t.Fatal(err)
	}
	if err := repository.db.QueryRow(`SELECT observation_count FROM target_parameters`).Scan(&parameterCount); err != nil {
		t.Fatal(err)
	}
	if err := repository.db.QueryRow(`SELECT COUNT(*) FROM target_endpoint_exchanges`).Scan(&referenceCount); err != nil {
		t.Fatal(err)
	}
	if endpointCount != 1 || parameterCount != 1 || referenceCount != 1 {
		t.Fatalf("concurrent counts = endpoint:%d parameter:%d reference:%d", endpointCount, parameterCount, referenceCount)
	}
}

func TestSQLiteTargetTreeIsDeterministicAndAggregatesEveryLevel(t *testing.T) {
	repository := openTestStore(t)
	ctx := context.Background()
	generationID, err := repository.CreateTargetGeneration(ctx, 8, 4)
	if err != nil {
		t.Fatal(err)
	}
	observations := []TargetObservation{
		{Key: TargetEndpointKey{Scheme: "https", Host: "example.test", Port: 443, Path: "/api/users", Method: "POST"}, StartedAt: time.Unix(20, 0), Status: 201, RequestMIME: "application/json"},
		{Key: TargetEndpointKey{Scheme: "http", Host: "other.test", Port: 80, Path: "/", Method: "GET"}, StartedAt: time.Unix(5, 0), Status: 204},
		{Key: TargetEndpointKey{Scheme: "https", Host: "example.test", Port: 443, Path: "/api/admin", Method: "GET"}, StartedAt: time.Unix(30, 0), Status: 404, ResponseMIME: "text/html"},
		{Key: TargetEndpointKey{Scheme: "https", Host: "example.test", Port: 443, Path: "/api/users", Method: "GET"}, StartedAt: time.Unix(10, 0), Status: 200, RequestMIME: "text/plain"},
	}
	for index := range observations {
		observations[index].ExchangeID = saveTargetExchange(t, repository)
		if err := repository.UpsertTargetObservation(ctx, generationID, observations[index]); err != nil {
			t.Fatal(err)
		}
	}
	if err := repository.ActivateTargetGeneration(ctx, generationID); err != nil {
		t.Fatal(err)
	}

	tree, err := repository.ListTargetTree(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree) != 2 || tree[0].Scheme != "http" || tree[0].Host != "other.test" || tree[1].Scheme != "https" || tree[1].Host != "example.test" {
		t.Fatalf("root ordering = %#v", tree)
	}
	httpsRoot := tree[1]
	if httpsRoot.ID != 0 || httpsRoot.Count != 3 || httpsRoot.LastSeen != time.Unix(30, 0).UTC() || !slices.Equal(httpsRoot.Statuses, []int{200, 201, 404}) || !slices.Equal(httpsRoot.RequestMIMEs, []string{"application/json", "text/plain"}) || !slices.Equal(httpsRoot.ResponseMIMEs, []string{"text/html"}) {
		t.Fatalf("HTTPS aggregate = %#v", httpsRoot)
	}
	if len(httpsRoot.Children) != 1 || httpsRoot.Children[0].Path != "api" || httpsRoot.Children[0].ID != 0 {
		t.Fatalf("api path node = %#v", httpsRoot.Children)
	}
	api := httpsRoot.Children[0]
	if len(api.Children) != 2 || api.Children[0].Path != "admin" || api.Children[1].Path != "users" {
		t.Fatalf("path ordering = %#v", api.Children)
	}
	users := api.Children[1]
	if users.ID != 0 || users.Count != 2 || !slices.Equal(users.Statuses, []int{200, 201}) || len(users.Children) != 2 || users.Children[0].Method != "GET" || users.Children[1].Method != "POST" {
		t.Fatalf("users aggregate = %#v", users)
	}
	for _, method := range users.Children {
		if method.ID == 0 || method.Path != "" || method.Host != "" || len(method.Children) != 0 {
			t.Fatalf("method leaf = %#v", method)
		}
	}
	if tree[0].ID != 0 || len(tree[0].Children) != 1 || tree[0].Children[0].Method != "GET" || tree[0].Children[0].ID == 0 {
		t.Fatalf("root path method = %#v", tree[0])
	}
}

func TestSQLiteTargetQueriesRejectInactiveUnknownAndOtherProjectRows(t *testing.T) {
	repository := openTestStore(t)
	ctx := context.Background()
	firstGenerationID, staleEndpointID := saveAndActivateTarget(t, repository, 1, "/stale")
	secondGenerationID, activeEndpointID := saveAndActivateTarget(t, repository, 2, "/active")
	if firstGenerationID == secondGenerationID {
		t.Fatal("generation IDs were reused")
	}
	var retiredCount int64
	if err := repository.db.QueryRow(`SELECT COUNT(*) FROM target_generations WHERE id = ?`, firstGenerationID).Scan(&retiredCount); err != nil {
		t.Fatal(err)
	}
	if retiredCount != 0 {
		t.Fatalf("retired generation count after activation = %d, want 0", retiredCount)
	}
	assertTargetEndpointQueriesNoRows(t, repository, staleEndpointID)
	assertTargetEndpointQueriesNoRows(t, repository, activeEndpointID+100000)

	if _, err := repository.db.Exec(`INSERT INTO projects (id, name, created_at_unix_nano) VALUES (2, 'Other', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.db.Exec(`INSERT INTO scope_state (project_id, version) VALUES (2, 1)`); err != nil {
		t.Fatal(err)
	}
	result, err := repository.db.Exec(`
		INSERT INTO target_generations (project_id, scope_version, status, processed, total, error, started_at_unix_nano, completed_at_unix_nano)
		VALUES (2, 1, 'active', 1, 1, '', 1, 2)`)
	if err != nil {
		t.Fatal(err)
	}
	otherGenerationID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	otherExchangeID := insertOtherProjectExchange(t, repository)
	result, err = repository.db.Exec(`
		INSERT INTO target_endpoints (
			generation_id, scheme, host, port, path, method, first_seen_unix_nano,
			last_seen_unix_nano, observation_count, latest_exchange_id, statuses_json,
			request_mimes_json, response_mimes_json, parse_diagnostics_json, error_seen
		) VALUES (?, 'https', 'private.test', 443, '/private', 'GET', 1, 1, 1, ?, '[200]', '[]', '[]', '[]', 0)`,
		otherGenerationID, otherExchangeID)
	if err != nil {
		t.Fatal(err)
	}
	otherEndpointID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.db.Exec(`UPDATE scope_state SET active_target_generation_id = ? WHERE project_id = 2`, otherGenerationID); err != nil {
		t.Fatal(err)
	}
	assertTargetEndpointQueriesNoRows(t, repository, otherEndpointID)

	otherObservation := TargetObservation{
		Key:        TargetEndpointKey{Scheme: "https", Host: "private.test", Port: 443, Path: "/private", Method: "GET"},
		ExchangeID: otherExchangeID, StartedAt: time.Unix(1, 0), Status: 200,
	}
	if err := repository.UpsertTargetObservation(ctx, secondGenerationID, otherObservation); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-project exchange upsert error = %v, want sql.ErrNoRows", err)
	}
	otherObservation.ExchangeID = saveTargetExchange(t, repository)
	if err := repository.UpsertTargetObservation(ctx, otherGenerationID, otherObservation); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-project generation upsert error = %v, want sql.ErrNoRows", err)
	}
	if _, err := repository.GetTargetEndpoint(ctx, activeEndpointID); err != nil {
		t.Fatalf("current project endpoint unreadable: %v", err)
	}
}

func TestSQLiteTargetParameterQueryUsesOneActiveGenerationSnapshot(t *testing.T) {
	registerScopeSnapshotBarrier(t)
	repository := openTestStore(t)
	ctx := context.Background()
	generationID, err := repository.CreateTargetGeneration(ctx, 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	observation := TargetObservation{
		Key:        TargetEndpointKey{Scheme: "https", Host: "example.test", Port: 443, Path: "/snapshot", Method: "GET"},
		ExchangeID: saveTargetExchange(t, repository), StartedAt: time.Unix(10, 0), Status: 200,
		Parameters: []TargetParameter{{Location: "query", Name: "page", ValueType: "string"}},
	}
	if err := repository.UpsertTargetObservation(ctx, generationID, observation); err != nil {
		t.Fatal(err)
	}
	if err := repository.ActivateTargetGeneration(ctx, generationID); err != nil {
		t.Fatal(err)
	}
	tree, err := repository.ListTargetTree(ctx)
	if err != nil {
		t.Fatal(err)
	}
	endpointID := firstTargetMethodID(t, tree)
	replacementID, err := repository.CreateTargetGeneration(ctx, 11, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.db.Exec(`PRAGMA journal_mode = WAL`); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.db.Exec(`
		ALTER TABLE target_endpoints RENAME TO target_endpoints_backing;
		CREATE VIEW target_endpoints AS
			SELECT id + scope_snapshot_test_barrier() AS id, generation_id, scheme, host, port,
				path, method, first_seen_unix_nano, last_seen_unix_nano, observation_count,
				latest_exchange_id, statuses_json, request_mimes_json, response_mimes_json,
				parse_diagnostics_json, error_seen
			FROM target_endpoints_backing;`); err != nil {
		t.Fatal(err)
	}

	entered, release := activateScopeSnapshotBarrier(t)
	parametersResult := make(chan []TargetParameter, 1)
	queryErr := make(chan error, 1)
	go func() {
		parameters, err := repository.ListTargetParameters(ctx, endpointID)
		if err != nil {
			queryErr <- err
			return
		}
		parametersResult <- parameters
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("parameter query did not validate its active endpoint")
	}

	activation := make(chan error, 1)
	go func() {
		tx, err := repository.db.BeginTx(ctx, nil)
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE target_generations SET status = 'retired' WHERE id = ?`, generationID)
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE scope_state SET active_target_generation_id = ? WHERE project_id = 1`, replacementID)
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE target_generations SET status = 'active' WHERE id = ?`, replacementID)
		}
		if err == nil {
			err = tx.Commit()
		} else if tx != nil {
			_ = tx.Rollback()
		}
		activation <- err
	}()
	select {
	case err := <-activation:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("activation did not commit while parameter validation was paused")
	}
	close(release)

	select {
	case err := <-queryErr:
		t.Fatal(err)
	case parameters := <-parametersResult:
		if len(parameters) != 1 || parameters[0].Name != "page" {
			t.Fatalf("parameters from mixed generation snapshots = %#v", parameters)
		}
	case <-time.After(time.Second):
		t.Fatal("parameter query did not finish")
	}
}

func TestSQLiteTargetForeignKeysEnabledOnEveryConnection(t *testing.T) {
	repository := openTestStore(t)
	repository.db.SetMaxOpenConns(4)
	ctx := context.Background()

	connections := make([]*sql.Conn, 0, 3)
	for index := 0; index < 3; index++ {
		connection, err := repository.db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, connection)
		var enabled int
		if err := connection.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&enabled); err != nil {
			t.Fatal(err)
		}
		if enabled != 1 {
			t.Errorf("connection %d foreign_keys = %d, want 1", index, enabled)
		}
	}
	for _, connection := range connections {
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
	}
}

func TestSQLiteTargetForeignKeyRejectsOrphanGeneration(t *testing.T) {
	repository := openTestStore(t)
	_, err := repository.db.Exec(`
		INSERT INTO target_generations (
			project_id, scope_version, status, processed, total, error, started_at_unix_nano
		) VALUES (999, 1, 'building', 0, 0, '', 1)`)
	if err == nil {
		t.Fatal("inserted target generation for a missing project")
	}
}

func TestSQLiteTargetForeignKeyGenerationDeleteCascades(t *testing.T) {
	repository := openTestStore(t)
	ctx := context.Background()
	generationID, err := repository.CreateTargetGeneration(ctx, 12, 1)
	if err != nil {
		t.Fatal(err)
	}
	observation := TargetObservation{
		Key:        TargetEndpointKey{Scheme: "https", Host: "example.test", Port: 443, Path: "/cascade", Method: "GET"},
		ExchangeID: saveTargetExchange(t, repository), StartedAt: time.Unix(12, 0), Status: 200,
		Parameters: []TargetParameter{{Location: "query", Name: "page", ValueType: "string"}},
	}
	if err := repository.UpsertTargetObservation(ctx, generationID, observation); err != nil {
		t.Fatal(err)
	}
	var endpointID int64
	if err := repository.db.QueryRow(`SELECT id FROM target_endpoints WHERE generation_id = ?`, generationID).Scan(&endpointID); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.db.Exec(`DELETE FROM target_generations WHERE id = ?`, generationID); err != nil {
		t.Fatal(err)
	}
	for table, query := range map[string]string{
		"endpoints":  `SELECT COUNT(*) FROM target_endpoints WHERE id = ?`,
		"references": `SELECT COUNT(*) FROM target_endpoint_exchanges WHERE endpoint_id = ?`,
		"parameters": `SELECT COUNT(*) FROM target_parameters WHERE endpoint_id = ?`,
	} {
		var count int64
		if err := repository.db.QueryRow(query, endpointID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("cascade left %d %s rows", count, table)
		}
	}
}

func saveTargetExchange(t *testing.T, repository *SQLiteStore) int64 {
	t.Helper()
	return saveTargetExchangeAt(t, repository, time.Unix(1, 0), 200, false)
}

func saveTargetExchangeAt(t *testing.T, repository *SQLiteStore, startedAt time.Time, status int, failed bool) int64 {
	t.Helper()
	exchange := &Exchange{
		Method: "GET", Scheme: "https", Host: "example.test", Path: "/", Status: status,
		StartedAt: startedAt.UTC(), Error: failed,
	}
	if err := repository.SaveExchange(context.Background(), exchange); err != nil {
		t.Fatal(err)
	}
	return exchange.ID
}

func saveAndActivateTarget(t *testing.T, repository *SQLiteStore, scopeVersion int64, path string) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	generationID, err := repository.CreateTargetGeneration(ctx, scopeVersion, 1)
	if err != nil {
		t.Fatal(err)
	}
	observation := TargetObservation{
		Key:        TargetEndpointKey{Scheme: "https", Host: "example.test", Port: 443, Path: path, Method: "GET"},
		ExchangeID: saveTargetExchange(t, repository), StartedAt: time.Unix(scopeVersion, 0).UTC(), Status: 200,
	}
	if err := repository.UpsertTargetObservation(ctx, generationID, observation); err != nil {
		t.Fatal(err)
	}
	if err := repository.ActivateTargetGeneration(ctx, generationID); err != nil {
		t.Fatal(err)
	}
	tree, err := repository.ListTargetTree(ctx)
	if err != nil {
		t.Fatal(err)
	}
	endpointID := firstTargetMethodID(t, tree)
	return generationID, endpointID
}

func firstTargetMethodID(t *testing.T, nodes []TargetTreeNode) int64 {
	t.Helper()
	for _, node := range nodes {
		if node.Method != "" {
			return node.ID
		}
		if id := firstTargetMethodID(t, node.Children); id != 0 {
			return id
		}
	}
	return 0
}

func assertTargetParametersHaveNoValueColumn(t *testing.T, repository *SQLiteStore) {
	t.Helper()
	rows, err := repository.db.Query(`PRAGMA table_info(target_parameters)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var columnID, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&columnID, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		if name == "value" {
			t.Fatal("target_parameters stores parameter values")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func assertTargetEndpointQueriesNoRows(t *testing.T, repository *SQLiteStore, endpointID int64) {
	t.Helper()
	ctx := context.Background()
	if _, err := repository.GetTargetEndpoint(ctx, endpointID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("endpoint %d detail error = %v, want sql.ErrNoRows", endpointID, err)
	}
	if _, err := repository.ListTargetRequests(ctx, endpointID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("endpoint %d requests error = %v, want sql.ErrNoRows", endpointID, err)
	}
	if _, err := repository.ListTargetParameters(ctx, endpointID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("endpoint %d parameters error = %v, want sql.ErrNoRows", endpointID, err)
	}
}

func insertOtherProjectExchange(t *testing.T, repository *SQLiteStore) int64 {
	t.Helper()
	result, err := repository.db.Exec(`
		INSERT INTO exchanges (
			method, scheme, host, path, query, status, mime_type, request_size, response_size,
			duration_ms, started_at_unix_nano, intercepted, error, error_message,
			request_truncated, response_truncated, in_scope, scope_version, scope_rule_id,
			tags_json, note, project_id
		) VALUES ('GET', 'https', 'private.test', '/private', '', 200, '', 0, 0, 0, 1, 0, 0, '', 0, 0, 1, 1, NULL, '[]', '', 2)`)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
