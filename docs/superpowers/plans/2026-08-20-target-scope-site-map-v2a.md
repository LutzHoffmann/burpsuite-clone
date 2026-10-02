# Target, Scope, and Site Map v2a Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add project scope rules, a live Site Map, and a value-free parameter inventory derived from captured HTTP and HTTPS history.

**Architecture:** A pure `internal/scope` package compiles immutable rules and classifies requests through one shared manager. A generation-based `internal/target` service projects in-scope exchanges into disposable SQLite target tables, coordinates background rebuilds, and exposes query methods to the API. The existing proxy remains the source of History and submits completed exchanges to the projector only after durable storage succeeds.

**Tech Stack:** Go 1.23, `net/http`, `database/sql`, modernc SQLite, `golang.org/x/net/idna`, React 19, TypeScript 5.8, Vite 7, Vitest 3, Testing Library.

**Spec:** `docs/superpowers/specs/2026-08-20-target-scope-site-map-v2a-design.md`

## Global Constraints

- Local services continue binding to `127.0.0.1` by default.
- Empty scope means no request is in scope; enabled exclusion rules override enabled inclusion rules.
- Out-of-scope traffic is forwarded and retained in History but bypasses interception and is never persisted in a target generation unless a newer pending scope classifies it in scope.
- Scope matching normalizes scheme, IDNA host, effective port, and path-segment boundaries.
- Target tables contain parameter names and metadata only; parameter values remain exclusively in History.
- Target projection failure never changes the proxied client response or discards a History exchange.
- Scope updates are atomic, use optimistic version checks, and never activate a partially valid rule set.
- Rebuilds preserve the last complete generation until a new generation is fully caught up and activated.
- Parameter parsing is bounded by captured-body size, JSON depth, field count, and multipart field count.
- New API writes retain Host, Origin, JSON content type, body-size, and unknown-field validation.
- Every behavior change follows test-first red-green-refactor and every task ends with a focused commit.

---

## File Map

### New backend files

- `internal/scope/rules.go`: scope domain types, validation, normalization, and classification.
- `internal/scope/rules_test.go`: matcher, precedence, normalization, and invalid-input tests.
- `internal/scope/manager.go`: lock-free reads of the current immutable rule set.
- `internal/scope/manager_test.go`: replacement and concurrent-read tests.
- `internal/store/scope.go`: SQLite scope state loading and atomic rule replacement.
- `internal/store/target.go`: target persistence DTOs and generation/query operations.
- `internal/target/analyzer.go`: endpoint normalization and bounded parameter extraction.
- `internal/target/analyzer_test.go`: query, form, JSON, cookie, multipart, and malformed-body tests.
- `internal/target/service.go`: incremental projection, serialized rebuild lifecycle, and target queries.
- `internal/target/service_test.go`: rebuild activation, rollback, cancellation, catch-up, and event tests.
- `internal/api/target_handlers.go`: scope and target REST handlers plus explicit DTO conversion.

### New frontend files

- `web/src/components/TargetWorkspace.tsx`: target data loading, selection, rebuild state, and coordination.
- `web/src/components/SiteMapTree.tsx`: accessible scheme/host/path/method tree.
- `web/src/components/ScopeEditor.tsx`: versioned include/exclude rule editing.
- `web/src/components/TargetWorkspace.test.tsx`: target workspace behavior tests.

### Modified files

- `go.mod`, `go.sum`: add IDNA normalization dependency.
- `internal/store/store.go`: History scope metadata and shared target DTOs.
- `internal/store/migrations.go`: schema versions 3 (scope/History) and 4 (target projection).
- `internal/store/sqlite.go`: History scope columns and rebuild History paging.
- `internal/store/sqlite_test.go`: migration, scope, generation, and project-isolation coverage.
- `internal/store/memory.go`: preserve scope fields in API and proxy tests.
- `internal/proxy/proxy.go`: classify before interception and project after History persistence.
- `internal/proxy/proxy_test.go`: in-scope and out-of-scope forwarding/interception tests.
- `internal/api/server.go`: target service configuration and routes.
- `internal/api/handlers.go`: History and exchange scope fields.
- `internal/api/server_test.go`: scope/target API validation and DTO tests.
- `cmd/proxy/main.go`: load rules and wire scope manager plus target service.
- `web/src/types.ts`: scope, target, rebuild, and History types.
- `web/src/api/client.ts`: scope and target API calls.
- `web/src/App.tsx`: Target navigation, event refresh, and History add-to-scope action.
- `web/src/components/HistoryTable.tsx`: scope indicator and add-to-scope control.
- `web/src/App.test.tsx`: navigation, live refresh, and History integration tests.
- `web/src/styles.css`: responsive Target workspace styling.
- `README.md`: Target/Scope usage and v2a test commands.

---

### Task 1: Immutable Scope Matcher and Manager

**Files:**
- Create: `internal/scope/rules.go`
- Create: `internal/scope/rules_test.go`
- Create: `internal/scope/manager.go`
- Create: `internal/scope/manager_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Produces: `scope.Rule`, `scope.State`, `scope.Target`, `scope.Decision`, `scope.RuleSet`, `scope.Compile`, `scope.Manager`.
- Consumes: standard URL/host parsing and `golang.org/x/net/idna` normalization.

- [ ] **Step 1: Add failing matcher tests**

Create table-driven tests with literal outcomes:

```go
func TestRuleSetClassifyHonorsExcludePrecedence(t *testing.T) {
	rules, err := Compile(7, []Rule{
		{ID: 1, Enabled: true, Action: ActionInclude, Scheme: "https", HostPattern: "*.example.test", PathPrefix: "/api"},
		{ID: 2, Enabled: true, Action: ActionExclude, HostPattern: "admin.example.test", PathPrefix: "/api/private"},
	})
	if err != nil { t.Fatal(err) }

	allowed := rules.Classify(Target{Scheme: "HTTPS", Host: "shop.example.test:443", Path: "/api/orders"})
	if !allowed.InScope || allowed.RuleID == nil || *allowed.RuleID != 1 || allowed.Version != 7 {
		t.Fatalf("allowed decision = %#v", allowed)
	}
	blocked := rules.Classify(Target{Scheme: "https", Host: "admin.example.test", Path: "/api/private/keys"})
	if blocked.InScope || blocked.RuleID == nil || *blocked.RuleID != 2 {
		t.Fatalf("blocked decision = %#v", blocked)
	}
}

func TestRuleSetClassifyRequiresIncludeAndPathBoundary(t *testing.T) {
	rules, err := Compile(3, []Rule{{ID: 4, Enabled: true, Action: ActionInclude, HostPattern: "example.test", PathPrefix: "/api"}})
	if err != nil { t.Fatal(err) }
	if rules.Classify(Target{Scheme: "http", Host: "example.test", Path: "/apiv2"}).InScope {
		t.Fatal("/apiv2 matched /api prefix")
	}
	if !rules.Classify(Target{Scheme: "http", Host: "example.test:80", Path: "/api/v1"}).InScope {
		t.Fatal("default HTTP port or path boundary did not normalize")
	}
}
```

Add cases for empty rules, disabled rules, exact hosts, a leading wildcard, Unicode/ASCII IDNA equivalence, HTTP/HTTPS default ports, explicit non-default ports, IPv4, bracketed IPv6, and root path matching. Add invalid cases for non-empty unsupported schemes, empty host patterns, wildcard placement other than `*.`, non-zero ports outside `1..65535`, and non-empty path prefixes not starting with `/`. Assert the three matching reasons and that `*.example.test` matches subdomains but not the apex host.

- [ ] **Step 2: Run the matcher test and verify RED**

Run: `go test ./internal/scope -run 'TestRuleSet' -count=1`

Expected: FAIL because the package and matcher types do not exist.

- [ ] **Step 3: Add the IDNA dependency**

Run: `go get golang.org/x/net/idna`

Expected: `go.mod` and `go.sum` record `golang.org/x/net` and its required transitive modules.

- [ ] **Step 4: Implement the domain types and compiler**

Use these exact public types:

```go
type Action string

const (
	ActionInclude Action = "include"
	ActionExclude Action = "exclude"
)

type Rule struct {
	ID          int64  `json:"id"`
	Enabled     bool   `json:"enabled"`
	Action      Action `json:"action"`
	Scheme      string `json:"scheme"`
	HostPattern string `json:"hostPattern"`
	Port        int    `json:"port"`
	PathPrefix  string `json:"pathPrefix"`
}

type State struct {
	Version int64  `json:"version"`
	Rules   []Rule `json:"rules"`
}

type Target struct { Scheme, Host, Path string }
type Decision struct { InScope bool; RuleID *int64; Version int64; Reason string }

func Compile(version int64, rules []Rule) (*RuleSet, error)
func (s *RuleSet) Classify(target Target) Decision
```

`Compile` must normalize copies of rules and keep `RuleSet` immutable. Empty scheme, port `0`, and empty path prefix mean unrestricted; normalize an empty path prefix to `/`. `Classify` first finds matching excludes, then matching includes, and otherwise returns `InScope: false`. Set `Reason` to `excluded`, `included`, or `no_include`, and return a copied rule ID pointer so callers cannot mutate compiled state. Normalize DNS names with `idna.Lookup.ToASCII`, lower-case scheme and host, split host/port with IPv6 support, infer ports 80/443, and compare path prefixes only when the next character is `/` or the candidate exactly equals the prefix.

- [ ] **Step 5: Run matcher tests and verify GREEN**

Run: `go test ./internal/scope -run 'TestRuleSet' -count=1`

Expected: PASS.

- [ ] **Step 6: Add failing manager replacement and concurrency tests**

```go
func TestManagerReplacePublishesWholeRuleSet(t *testing.T) {
	initial, _ := Compile(1, nil)
	manager := NewManager(initial)
	next, _ := Compile(2, []Rule{{ID: 9, Enabled: true, Action: ActionInclude, HostPattern: "example.test"}})
	manager.Replace(next)
	decision := manager.Current().Classify(Target{Scheme: "https", Host: "example.test", Path: "/"})
	if !decision.InScope || decision.Version != 2 { t.Fatalf("decision = %#v", decision) }
}
```

Add a test with 100 concurrent readers while one goroutine replaces versions 2 through 20; every observed version must be a complete compiled set and the race detector must remain clean.

- [ ] **Step 7: Run manager tests and verify RED**

Run: `go test ./internal/scope -run 'TestManager' -race -count=1`

Expected: FAIL because `Manager` does not exist.

- [ ] **Step 8: Implement the atomic manager**

```go
type Manager struct { current atomic.Pointer[RuleSet] }

func NewManager(initial *RuleSet) *Manager {
	manager := &Manager{}
	manager.current.Store(initial)
	return manager
}

func (m *Manager) Current() *RuleSet { return m.current.Load() }
func (m *Manager) Replace(next *RuleSet) { m.current.Store(next) }
```

Reject `nil` at construction with a package-private empty compiled set; `Replace(nil)` must panic because publishing an undefined scope is a programming error.

- [ ] **Step 9: Run all scope tests**

Run: `go test -race ./internal/scope -count=1`

Expected: PASS with no race reports.

- [ ] **Step 10: Commit Task 1**

```bash
git add -- go.mod go.sum internal/scope/rules.go internal/scope/rules_test.go internal/scope/manager.go internal/scope/manager_test.go
git commit -m "feat: add immutable project scope matcher"
```

---

### Task 2: Scope Schema, History Metadata, and Atomic Rule Storage

**Files:**
- Modify: `internal/store/store.go`
- Create: `internal/store/scope.go`
- Modify: `internal/store/migrations.go`
- Modify: `internal/store/sqlite.go`
- Modify: `internal/store/memory.go`
- Modify: `internal/store/sqlite_test.go`

**Interfaces:**
- Consumes: `scope.State` and `scope.Rule` from Task 1.
- Produces: `store.ScopeStore`, scope-aware `store.Exchange` and `store.HistoryItem`, History paging for rebuilds.

- [ ] **Step 1: Add failing migration and scope-store tests**

Add tests that open both version-1 and version-2 fixture databases, apply all migrations, and assert the following behavior:

```go
func TestSQLiteScopeRulesReplaceAtomically(t *testing.T) {
	store := openTestStore(t)
	state, err := store.ReplaceScopeRules(context.Background(), 0, []scope.Rule{{
		Enabled: true, Action: scope.ActionInclude, Scheme: "https", HostPattern: "example.test", PathPrefix: "/api",
	}})
	if err != nil { t.Fatal(err) }
	if state.Version != 1 || len(state.Rules) != 1 || state.Rules[0].ID == 0 { t.Fatalf("state = %#v", state) }
	if _, err := store.ReplaceScopeRules(context.Background(), 0, nil); !errors.Is(err, store.ErrScopeVersionConflict) {
		t.Fatalf("conflict error = %v", err)
	}
	loaded, err := store.LoadScopeState(context.Background())
	if err != nil || loaded.Version != 1 || len(loaded.Rules) != 1 { t.Fatalf("loaded = %#v, err = %v", loaded, err) }
}
```

Define `openTestStore(t *testing.T) *SQLiteStore` in `sqlite_test.go` if it is not already present. It must open a temporary project database, register `t.Cleanup(store.Close)`, and fail the test on setup errors.

Add a History round-trip assertion for `InScope`, `ScopeVersion`, and nullable `ScopeRuleID`, plus a paging test proving `ListExchangesPage(ctx, afterID, throughID, 2)` returns ascending IDs and never returns an ID above `throughID`.

- [ ] **Step 2: Run store tests and verify RED**

Run: `go test ./internal/store -run 'TestSQLite(Scope|HistoryScope|ListExchangesPage)' -count=1`

Expected: FAIL because schema version 3 and scope methods do not exist.

- [ ] **Step 3: Add schema migration 3**

Append `{version: 3, apply: applyTargetScopeSchema}`. The migration must add these History columns and tables:

```sql
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
CREATE INDEX scope_rules_project_position ON scope_rules(project_id, position);
```

Migration 3 is complete in this task and must never be edited after it can be applied. Task 4 adds target projection tables in migration 4.

- [ ] **Step 4: Extend History domain and persistence**

Add untagged fields to `Exchange`:

```go
InScope      bool
ScopeVersion int64
ScopeRuleID  *int64
```

Add the same fields with `json:"inScope"`, `json:"scopeVersion"`, and `json:"scopeRuleId"` tags to `HistoryItem`. Update SQLite INSERT/SELECT/Scan and memory-store projection so list and detail results agree.

- [ ] **Step 5: Implement atomic scope storage and rebuild paging**

Create:

```go
var ErrScopeVersionConflict = errors.New("scope version conflict")

type ScopeStore interface {
	LoadScopeState(context.Context) (scope.State, error)
	ReplaceScopeRules(context.Context, int64, []scope.Rule) (scope.State, error)
}

type RebuildHistoryStore interface {
	LatestExchangeID(context.Context) (int64, error)
	CountExchangesThrough(context.Context, int64) (int64, error)
	ListExchangesPage(context.Context, int64, int64, int) ([]Exchange, error)
}
```

`ReplaceScopeRules` starts an immediate transaction, checks `scope_state.version`, deletes current rules, inserts all new rules in supplied order, increments the version once, and returns assigned IDs. A conflict rolls back without modifying rules or version.

- [ ] **Step 6: Run focused store tests**

Run: `go test ./internal/store -run 'TestSQLite(Scope|HistoryScope|ListExchangesPage)' -count=1`

Expected: PASS.

- [ ] **Step 7: Run all store tests**

Run: `go test -race ./internal/store -count=1`

Expected: PASS.

- [ ] **Step 8: Commit Task 2**

```bash
git add -- internal/store/store.go internal/store/scope.go internal/store/migrations.go internal/store/sqlite.go internal/store/memory.go internal/store/sqlite_test.go
git commit -m "feat: persist project scope and history classification"
```

---

### Task 3: Endpoint Analyzer and Bounded Parameter Extraction

**Files:**
- Create: `internal/target/analyzer.go`
- Create: `internal/target/analyzer_test.go`
- Modify: `internal/store/store.go`

**Interfaces:**
- Consumes: complete `store.Exchange` records.
- Produces: `store.TargetObservation`, `store.TargetEndpointKey`, and `store.TargetParameter` without parameter values.

- [ ] **Step 1: Define failing analyzer behavior tests**

Add literal tests for endpoint identity and all supported parameter locations:

```go
func TestAnalyzeExtractsValueFreeParameters(t *testing.T) {
	exchange := &store.Exchange{
		ID: 41, Method: "post", Scheme: "HTTPS", Host: "EXAMPLE.TEST:443", Path: "/users", Query: "page=2&page=3",
		StartedAt: time.Unix(10, 0).UTC(), Status: 201, MIMEType: "application/json",
		Request: store.RequestData{
			Headers: http.Header{"Content-Type": {"application/json"}, "Cookie": {"session=secret"}},
			Body: []byte(`{"user":{"email":"a@example.test","admin":false},"items":[{"id":7}]}`),
		},
	}
	observation, err := Analyze(exchange, Limits{MaxJSONDepth: 16, MaxFields: 1000, MaxMultipartFields: 100})
	if err != nil { t.Fatal(err) }
	if observation.Key != (store.TargetEndpointKey{Scheme: "https", Host: "example.test", Port: 443, Path: "/users", Method: "POST"}) {
		t.Fatalf("key = %#v", observation.Key)
	}
	want := []store.TargetParameter{
		{Location: "query", Name: "page", ValueType: "string"},
		{Location: "json", Name: "items[].id", ValueType: "number"},
		{Location: "json", Name: "user.admin", ValueType: "boolean"},
		{Location: "json", Name: "user.email", ValueType: "string"},
		{Location: "cookie", Name: "session", ValueType: "string"},
	}
	slices.SortFunc(observation.Parameters, compareTargetParameter)
	slices.SortFunc(want, compareTargetParameter)
	if !slices.Equal(observation.Parameters, want) { t.Fatalf("parameters = %#v, want %#v", observation.Parameters, want) }
}
```

Define `compareTargetParameter(a, b store.TargetParameter) int` in `analyzer_test.go`; compare location, then name, then value type with `strings.Compare`. This uses only the standard library and makes the test ordering explicit.

Add tests for URL-encoded forms, non-file multipart fields, repeated names, JSON arrays, null/object/array types, malformed JSON diagnostics, unsupported MIME, binary data, truncated requests, depth overflow, field-count overflow, and a guarantee that the literal secret values never occur in serialized `TargetObservation`.

- [ ] **Step 2: Run analyzer tests and verify RED**

Run: `go test ./internal/target -run 'TestAnalyze' -count=1`

Expected: FAIL because analyzer and target DTOs do not exist.

- [ ] **Step 3: Add store-level target DTOs**

Add exact shared persistence types:

```go
type TargetEndpointKey struct { Scheme, Host string; Port int; Path, Method string }
type TargetParameter struct {
	Location, Name, ValueType string
	FirstSeen, LastSeen time.Time
	Count int64
}
type TargetObservation struct {
	Key TargetEndpointKey
	ExchangeID int64
	StartedAt time.Time
	Status int
	RequestMIME string
	ResponseMIME string
	Error bool
	Parameters []TargetParameter
	ParseDiagnostic string
}
```

- [ ] **Step 4: Implement bounded extraction**

Expose:

```go
type Limits struct { MaxJSONDepth, MaxFields, MaxMultipartFields int }
func Analyze(exchange *store.Exchange, limits Limits) (store.TargetObservation, error)
```

Use `url.ParseQuery` for query and URL-encoded forms, `http.Request.Cookies` semantics for cookie names, `json.Decoder.UseNumber` plus iterative stack traversal for JSON, and `mime/multipart.Reader` for non-file multipart fields. Only parse body parameters when the request is complete, uncompressed, and text-safe; truncated, compressed, unsupported, and binary bodies produce a fixed diagnostic code and no body parameters. Sort and deduplicate parameters by `(location, name, valueType)`. Analyzer-produced parameters leave timestamps/count at zero because persistence derives them from `TargetObservation.StartedAt`. Return an error only when endpoint identity cannot be normalized; malformed bodies populate a bounded, value-free `ParseDiagnostic` code and return a valid observation.

- [ ] **Step 5: Run analyzer tests and verify GREEN**

Run: `go test ./internal/target -run 'TestAnalyze' -count=1`

Expected: PASS.

- [ ] **Step 6: Run analyzer fuzz seed tests**

Add `FuzzAnalyzeNeverPanics` seeds for malformed JSON, multipart boundaries, invalid UTF-8, and deeply nested arrays. Run: `go test ./internal/target -run=FuzzAnalyzeNeverPanics -count=1`

Expected: PASS for the seed corpus.

- [ ] **Step 7: Commit Task 3**

```bash
git add -- internal/store/store.go internal/target/analyzer.go internal/target/analyzer_test.go
git commit -m "feat: analyze target endpoints and parameters"
```

---

### Task 4: Target Generation Persistence and Queries

**Files:**
- Create: `internal/store/target.go`
- Modify: `internal/store/migrations.go`
- Modify: `internal/store/sqlite_test.go`

**Interfaces:**
- Consumes: target DTOs from Task 3 and the completed scope schema from Task 2.
- Produces: `store.TargetStore`, generation lifecycle methods, tree/detail/request/parameter query types.

- [ ] **Step 1: Add failing generation repository tests**

Test create, upsert, deduplication, progress, activation, rollback, and active-generation queries:

```go
func TestSQLiteTargetGenerationActivatesAtomically(t *testing.T) {
	repository := openTestStore(t)
	generationID, err := repository.CreateTargetGeneration(context.Background(), 4, 2)
	if err != nil { t.Fatal(err) }
	observation := TargetObservation{Key: TargetEndpointKey{Scheme: "https", Host: "example.test", Port: 443, Path: "/api", Method: "GET"}, ExchangeID: saveTargetExchange(t, repository), StartedAt: time.Unix(1, 0), Status: 200}
	if err := repository.UpsertTargetObservation(context.Background(), generationID, observation); err != nil { t.Fatal(err) }
	if err := repository.ActivateTargetGeneration(context.Background(), generationID); err != nil { t.Fatal(err) }
	tree, err := repository.ListTargetTree(context.Background())
	if err != nil || len(tree) != 1 || tree[0].Host != "example.test" { t.Fatalf("tree = %#v, err = %v", tree, err) }
}
```

Add tests proving duplicate observations increment counts but create one endpoint and one endpoint-exchange pair, parameter first/last/count metadata aggregates correctly, diagnostic codes contain no source value, parameter values cannot be stored, a failed pending generation does not replace the active generation, retired generations are pruned only after activation, and projects cannot read another project's target rows.

Define `saveTargetExchange(t *testing.T, repository *SQLiteStore) int64` in `sqlite_test.go`; save a minimal exchange through the normal `SaveExchange` method and return its assigned ID. Reuse the `openTestStore` helper from Task 2.

- [ ] **Step 2: Run focused persistence tests and verify RED**

Run: `go test ./internal/store -run 'TestSQLiteTarget' -count=1`

Expected: FAIL because target tables and methods do not exist.

- [ ] **Step 3: Add schema migration 4 for target projection**

Append `{version: 4, apply: applyTargetProjectionSchema}`. Migration 4 adds generation, endpoint, reference, and parameter tables with these uniqueness boundaries:

```sql
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
  scheme TEXT NOT NULL, host TEXT NOT NULL, port INTEGER NOT NULL,
  path TEXT NOT NULL, method TEXT NOT NULL,
  first_seen_unix_nano INTEGER NOT NULL, last_seen_unix_nano INTEGER NOT NULL,
  observation_count INTEGER NOT NULL, latest_exchange_id INTEGER NOT NULL REFERENCES exchanges(id),
  statuses_json TEXT NOT NULL, request_mimes_json TEXT NOT NULL, response_mimes_json TEXT NOT NULL,
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
  location TEXT NOT NULL, name TEXT NOT NULL, value_type TEXT NOT NULL,
  first_seen_unix_nano INTEGER NOT NULL, last_seen_unix_nano INTEGER NOT NULL,
  observation_count INTEGER NOT NULL,
  PRIMARY KEY(endpoint_id, location, name, value_type)
);
```

- [ ] **Step 4: Define target query DTOs and repository interface**

```go
type TargetGeneration struct { ID, ScopeVersion, Processed, Total int64; Status, Error string; StartedAt time.Time; CompletedAt *time.Time }
type TargetTreeNode struct { ID int64; Scheme, Host, Path, Method string; Port int; InScope bool; Statuses []int; RequestMIMEs, ResponseMIMEs []string; Count int64; LastSeen time.Time; Children []TargetTreeNode }
type TargetEndpoint struct { ID int64; Key TargetEndpointKey; InScope bool; FirstSeen, LastSeen time.Time; Count int64; Statuses []int; RequestMIMEs, ResponseMIMEs, ParseDiagnostics []string; ErrorSeen bool; LatestExchangeID int64 }
type TargetRequestRef struct { ExchangeID int64; StartedAt time.Time; Status int; Error bool }

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
```

- [ ] **Step 5: Implement transactional upserts and activation**

`UpsertTargetObservation` must upsert the endpoint, insert the endpoint/exchange pair with `ON CONFLICT DO NOTHING`, and increment endpoint/parameter counts only when that pair was newly inserted. Merge status, MIME, and bounded diagnostic-code sets in sorted JSON arrays; never persist parser error strings containing input data. Populate parameter first/last timestamps and counts from observation time. `ListTargetTree` deterministically groups scheme plus authority, exact path segments, and method; only method leaves carry endpoint IDs, while grouping nodes use ID `0`. Aggregate status and MIME sets into every returned tree level so client filters do not require detail requests. Persistence leaves `InScope` unset because Task 5 computes it against the current manager. `ActivateTargetGeneration` verifies status `building`, marks the previous active row `retired`, updates `scope_state.active_target_generation_id`, marks the new row `active`, and commits all changes atomically. `LatestTargetGeneration` returns the newest row for rebuild progress even when there is no active generation. After successful activation, `PruneRetiredTargetGenerations` deletes retired generations and cascading derived rows; pruning failure is logged but does not roll back the newly active map.

When there is no active generation, `ListTargetTree` returns an empty slice. Detail/request/parameter queries for unknown or inactive endpoint IDs return `sql.ErrNoRows`; every query joins through the active generation and current project to prevent stale-ID and cross-project reads.

- [ ] **Step 6: Run target persistence tests**

Run: `go test -race ./internal/store -run 'TestSQLiteTarget' -count=1`

Expected: PASS.

- [ ] **Step 7: Commit Task 4**

```bash
git add -- internal/store/target.go internal/store/migrations.go internal/store/sqlite_test.go
git commit -m "feat: persist target projection generations"
```

---

### Task 5: Incremental Projector and Background Rebuild Service

**Files:**
- Create: `internal/target/service.go`
- Create: `internal/target/service_test.go`

**Interfaces:**
- Consumes: `scope.Manager`, `store.ScopeStore`, `store.RebuildHistoryStore`, `store.TargetStore`, `Analyze`.
- Produces: `target.Service` used by proxy, API, and process wiring.

- [ ] **Step 1: Add failing service lifecycle tests**

Use a real temporary SQLite store and real scope manager. Cover:

```go
func TestServiceReplaceRulesRebuildsAndActivates(t *testing.T) {
	repository := openTargetRepository(t)
	saveExchange(t, repository, "https", "example.test", "/api", "q=1")
	initial, _ := scope.Compile(0, nil)
	service := NewService(repository, scope.NewManager(initial), events.NewHub(), Limits{MaxJSONDepth: 16, MaxFields: 1000, MaxMultipartFields: 100})
	t.Cleanup(service.Close)

	state, err := service.ReplaceRules(context.Background(), 0, []scope.Rule{{Enabled: true, Action: scope.ActionInclude, Scheme: "https", HostPattern: "example.test"}})
	if err != nil { t.Fatal(err) }
	waitForRebuildStatus(t, service, "active")
	tree, err := service.Tree(context.Background())
	if err != nil || len(tree) != 1 { t.Fatalf("tree = %#v, err = %v", tree, err) }
	if state.Version != 1 || !service.Scope().Current().Classify(scope.Target{Scheme: "https", Host: "example.test", Path: "/api"}).InScope { t.Fatalf("state = %#v", state) }
}
```

Add tests for optimistic conflict, malformed rule preserving prior state, rebuild failure preserving prior active generation, retry, newer rebuild cancellation, exchanges captured under the prior scope but completed during rebuild, version-mismatched observations not contaminating the old active generation, startup recovery from a `building` generation, event sequence/payloads, coalesced progress events, retired-generation pruning, and `Close` cancellation.

Define these test helpers in `service_test.go`:

```go
func openTargetRepository(t *testing.T) *store.SQLiteStore
func saveExchange(t *testing.T, repository *store.SQLiteStore, scheme, host, path, query string) int64
func waitForRebuildStatus(t *testing.T, service *Service, status string) store.RebuildStatus
```

`openTargetRepository` opens and cleans up a temporary migrated project database. `saveExchange` writes a complete minimal exchange and returns its ID. `waitForRebuildStatus` polls with a two-second deadline and reports the last status on timeout.

- [ ] **Step 2: Run service tests and verify RED**

Run: `go test ./internal/target -run 'TestService' -count=1`

Expected: FAIL because `Service` does not exist.

- [ ] **Step 3: Define service repository and public methods**

```go
type Repository interface {
	store.Store
	store.ScopeStore
	store.RebuildHistoryStore
	store.TargetStore
}

type Service struct {
	repository Repository
	scope      *scope.Manager
	events     *events.Hub
	limits     Limits

	mu                  sync.Mutex
	pendingObservers    sync.WaitGroup
	activeGenerationID  int64
	activeScopeVersion  int64
	rebuildCancel       context.CancelFunc
	rebuildDone         chan struct{}
	pendingGenerationID int64
	pendingRules        *scope.RuleSet
	closed              bool
}

func NewService(Repository, *scope.Manager, *events.Hub, Limits) *Service
func (s *Service) Recover(context.Context) error
func (s *Service) Scope() *scope.Manager
func (s *Service) Observe(context.Context, *store.Exchange) error
func (s *Service) State(context.Context) (scope.State, error)
func (s *Service) ReplaceRules(context.Context, int64, []scope.Rule) (scope.State, error)
func (s *Service) RetryRebuild(context.Context) error
func (s *Service) RebuildStatus(context.Context) (store.RebuildStatus, error)
func (s *Service) Tree(context.Context) ([]store.TargetTreeNode, error)
func (s *Service) Endpoint(context.Context, int64) (store.TargetEndpoint, error)
func (s *Service) Requests(context.Context, int64) ([]store.TargetRequestRef, error)
func (s *Service) Parameters(context.Context, int64) ([]store.TargetParameter, error)
func (s *Service) Close()
```

Add the API-facing status type in `internal/store/store.go`:

```go
type RebuildStatus struct {
	ID, ScopeVersion, ActiveScopeVersion, Processed, Total int64
	Status, Error string
}
```

When no generation exists, `RebuildStatus` returns `{Status: "idle"}`. Otherwise it maps the latest generation, including `building`, `active`, `failed`, or `cancelled`, and separately reports the currently readable generation's `ActiveScopeVersion`; a retired generation is never the newest generation after a replacement starts.

- [ ] **Step 4: Implement rule replacement and rebuild orchestration**

Validate with `scope.Compile(expectedVersion+1, rules)` before writing. Call `ReplaceScopeRules`, compile the returned rules with assigned IDs, publish the new manager pointer, emit `scope.changed`, and start a serialized rebuild. The rebuild captures `throughID := LatestExchangeID`, processes ascending pages of 200, reclassifies every historical exchange with the pending rule set rather than trusting its capture-time `InScope` field, updates progress at most every 100 records or 250 ms, then synchronizes pending observations completed after `throughID` before activation.

Keep one cancellation function, done channel, generation ID, and compiled pending rule set under a mutex. A newer rebuild cancels the old context, waits for the old worker to exit, marks the old generation cancelled, and then starts. On error, mark failed and emit `target.rebuild.failed`; on success, activate, emit `target.rebuild.completed`, and prune retired generations. `Recover` marks an interrupted `building` generation cancelled and starts a fresh rebuild for the current persisted scope; it leaves an explicit `failed` generation available for operator retry.

Publish only these value-free event payloads: `scope.changed` with `{version}`, `target.endpoint.updated` with `{endpointId, generationId}`, and each `target.rebuild.started|progress|completed|failed` event with the current `store.RebuildStatus`. Emit endpoint updates only for changes visible in the active generation; pending rebuild writes become visible through the completion event. Coalesce progress events to the specified 100-record/250-ms cadence.

- [ ] **Step 5: Implement incremental observation**

Without a rebuild, `Observe` writes only when the exchange is marked in scope and its `ScopeVersion` equals the cached active generation's scope version; no active generation or a version mismatch means a no-op. `Recover` initializes the cached active ID/version, and successful activation swaps them under `mu`. During a rebuild, `Observe` ignores the capture-time decision, classifies the completed exchange with `pendingRules`, and writes matching observations only to the pending generation. Under `mu`, increment `pendingObservers` before releasing the pending generation ID; before activation, clear the pending ID under the same mutex and wait for those pending observers. This prevents a late pending write from landing after activation without blocking new writes to the newly active generation. The previous active generation remains unchanged and queryable while visibly marked stale; the bounded History pass plus pending writes catches the new generation up before atomic activation.

`Tree` and `Endpoint` classify stored endpoint keys with the current manager before returning them and populate `InScope`; grouping nodes are in scope when any descendant is. This allows the UI to distinguish stale-map entries after a scope edit without mutating the last complete generation.

- [ ] **Step 6: Run service tests and verify GREEN**

Run: `go test -race ./internal/target -count=1`

Expected: PASS with no goroutine leaks or races.

- [ ] **Step 7: Commit Task 5**

```bash
git add -- internal/target/service.go internal/target/service_test.go
git commit -m "feat: rebuild and query target projections"
```

---

### Task 6: Proxy Scope Enforcement and Process Wiring

**Files:**
- Modify: `internal/proxy/proxy.go`
- Modify: `internal/proxy/proxy_test.go`
- Modify: `cmd/proxy/main.go`

**Interfaces:**
- Consumes: `scope.Manager` and `target.Service` from Tasks 1 and 5.
- Produces: classified History records, out-of-scope interception bypass, and generation-aware observation submissions.

- [ ] **Step 1: Add failing proxy behavior tests**

Add two integration tests using real local targets:

```go
func TestProxyForwardsAndStoresOutOfScopeWithoutIntercepting(t *testing.T) {
	rules, _ := scope.Compile(1, []scope.Rule{{ID: 1, Enabled: true, Action: scope.ActionInclude, HostPattern: "allowed.test"}})
	controller := intercept.NewController(intercept.NewQueue(time.Second), true, []intercept.Rule{{Enabled: true}})
	mem := &memoryStore{}
	srv := NewServer(Config{Store: mem, BodyLimitBytes: 1024, Intercept: controller, Scope: scope.NewManager(rules)})
	response := serveRequestThroughProxy(t, srv, outOfScopeTargetURL(t))
	if response.StatusCode != http.StatusOK { t.Fatalf("status = %d", response.StatusCode) }
	if len(controller.Queue().List()) != 0 { t.Fatal("out-of-scope request entered intercept queue") }
	exchange := waitForSavedExchanges(t, mem, 1)[0]
	if exchange.InScope || exchange.ScopeVersion != 1 || exchange.ScopeRuleID != nil { t.Fatalf("exchange = %#v", exchange) }
}
```

Add the in-scope counterpart proving the request queues, forwards after approval, records the winning rule ID, and calls a recording target observer only after the store assigns an exchange ID. Assert that an out-of-scope completion is also submitted after persistence so a concurrent pending generation can reclassify it, while the service test proves it is not projected under unchanged scope. Cover HTTP, HTTPS MITM, dropped requests, and preparation/upstream failures.

Define `serveRequestThroughProxy(t *testing.T, srv *Server, targetURL string) *http.Response` in `proxy_test.go`; it starts the proxy with `httptest.NewServer`, sends one request through an `http.Client` configured with that proxy, and registers all response/server cleanup. Define `outOfScopeTargetURL(t *testing.T) string` to start a local HTTP target that returns 200 and whose host cannot match `allowed.test`. Reuse the existing `waitForSavedExchanges` helper.

- [ ] **Step 2: Run focused proxy tests and verify RED**

Run: `go test ./internal/proxy -run 'TestProxy.*Scope' -count=1`

Expected: FAIL because proxy config and exchange classification are absent.

- [ ] **Step 3: Extend proxy configuration and prepared requests**

```go
type TargetObserver interface { Observe(context.Context, *store.Exchange) error }

type Config struct {
	Store             store.Store
	BodyLimitBytes    int64
	Transport         http.RoundTripper
	Authority         *certs.Authority
	Events            *events.Hub
	Intercept         *intercept.Controller
	StreamIdleTimeout time.Duration
	Scope             *scope.Manager
	Target            TargetObserver
}
```

Add `decision scope.Decision` to `preparedRequest`. Classify before `prepareRequest`; when no manager is configured, fail closed with `Decision{InScope: false, Reason: "no_include"}` while still forwarding and storing the request. Make `prepareRequest` skip the intercept controller when `decision.InScope` is false. Update existing interception tests to install explicit include rules for their local targets rather than relying on an implicit all-target scope.

- [ ] **Step 4: Persist decisions and project after durable History**

Every exchange constructor copies `InScope`, `ScopeVersion`, and `ScopeRuleID`. In `saveExchange`, call `Store.SaveExchange` first, publish the History event second, then call `Target.Observe` for every completed exchange. The service performs generation-specific classification and does no target write for unchanged out-of-scope traffic. Log projection errors without body data and without changing proxy output.

- [ ] **Step 5: Wire startup**

In `main`, load `ScopeState`, compile it, construct the manager and target service, call `targetService.Recover` before accepting traffic, and pass the service to both proxy and API. Ensure `defer targetService.Close()` runs before store close. A rule load, compile, or recovery failure is fatal because silently using broader or inconsistent scope violates the safety invariant.

- [ ] **Step 6: Run proxy and command tests**

Run: `go test -race ./internal/proxy ./cmd/proxy -count=1`

Expected: PASS.

- [ ] **Step 7: Commit Task 6**

```bash
git add -- internal/proxy/proxy.go internal/proxy/proxy_test.go cmd/proxy/main.go
git commit -m "feat: enforce scope in proxy pipeline"
```

---

### Task 7: Scope and Target REST API

**Files:**
- Create: `internal/api/target_handlers.go`
- Modify: `internal/api/server.go`
- Modify: `internal/api/handlers.go`
- Modify: `internal/api/server_test.go`

**Interfaces:**
- Consumes: `target.Service` query and mutation methods.
- Produces: the scope, tree, endpoint, request, parameter, and rebuild endpoints from the spec.

- [ ] **Step 1: Add failing API contract tests**

Add tests for all routes, including this scope update contract:

```go
func TestAPIReplacesScopeRulesAndReturnsAssignedIDs(t *testing.T) {
	srv, service := newTargetAPIServer(t)
	body := `{"version":0,"rules":[{"id":0,"enabled":true,"action":"include","scheme":"https","hostPattern":"example.test","port":0,"pathPrefix":"/api"}]}`
	request := httptest.NewRequest(http.MethodPut, "http://127.0.0.1:9080/api/scope/rules", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://127.0.0.1:9080")
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK { t.Fatalf("status = %d, body = %q", recorder.Code, recorder.Body.String()) }
	var state scope.State
	if err := json.NewDecoder(recorder.Body).Decode(&state); err != nil { t.Fatal(err) }
	if state.Version != 1 || len(state.Rules) != 1 || state.Rules[0].ID == 0 { t.Fatalf("state = %#v", state) }
	waitForServiceRebuild(t, service)
}
```

Add cases for `409 Conflict` on stale version, `400` invalid rules, `404` unknown endpoint, `503` missing target service, forbidden cross-origin writes, wrong content type, oversized JSON, unknown fields, tree ordering, value-free parameter DTOs, and rebuild retry status.

Define `newTargetAPIServer(t *testing.T) (*Server, *target.Service)` in `server_test.go`; it opens a temporary SQLite repository, creates the empty scope manager and real target service, wires `api.Config`, and registers cleanup. Define `waitForServiceRebuild(t *testing.T, service *target.Service)` to poll `RebuildStatus` until `active` or fail after two seconds with the last response.

- [ ] **Step 2: Run API tests and verify RED**

Run: `go test ./internal/api -run 'TestAPI(Scope|Target|Rebuild)' -count=1`

Expected: FAIL because routes and target config are absent.

- [ ] **Step 3: Register exact routes and DTOs**

Extend `api.Config` with `Target *target.Service` and register:

```go
srv.mux.HandleFunc("GET /api/scope/rules", srv.handleScopeRules)
srv.mux.HandleFunc("PUT /api/scope/rules", srv.handleScopeRulesUpdate)
srv.mux.HandleFunc("GET /api/target/tree", srv.handleTargetTree)
srv.mux.HandleFunc("GET /api/target/endpoints/{id}", srv.handleTargetEndpoint)
srv.mux.HandleFunc("GET /api/target/endpoints/{id}/requests", srv.handleTargetRequests)
srv.mux.HandleFunc("GET /api/target/endpoints/{id}/parameters", srv.handleTargetParameters)
srv.mux.HandleFunc("GET /api/target/rebuild", srv.handleTargetRebuild)
srv.mux.HandleFunc("POST /api/target/rebuild", srv.handleTargetRebuildRetry)
```

Use explicit request DTO `scopeUpdateDTO{Version int64; Rules []scope.Rule}` and explicit response DTOs; do not serialize SQLite structs directly.

- [ ] **Step 4: Implement handlers and error mapping**

Map `store.ErrScopeVersionConflict` to 409, validation errors to 400, `sql.ErrNoRows` to 404, unavailable service to 503, and repository failures to 500 with operator-safe text. Reuse `decodeJSON`, `historyID`-style numeric parsing, and `writeJSON`. A retry POST accepts `{}` and returns 202 plus the current rebuild DTO.

- [ ] **Step 5: Extend History DTOs**

Add `inScope`, `scopeVersion`, and nullable `scopeRuleId` to `exchangeDTO`, `toExchangeDTO`, and History responses. Confirm JSON `null` is emitted when no rule matched.

- [ ] **Step 6: Run API tests**

Run: `go test -race ./internal/api -count=1`

Expected: PASS.

- [ ] **Step 7: Commit Task 7**

```bash
git add -- internal/api/target_handlers.go internal/api/server.go internal/api/handlers.go internal/api/server_test.go
git commit -m "feat: expose scope and target api"
```

---

### Task 8: Frontend Target Data Layer and Workspace

**Files:**
- Modify: `web/src/types.ts`
- Modify: `web/src/api/client.ts`
- Create: `web/src/components/TargetWorkspace.tsx`
- Create: `web/src/components/SiteMapTree.tsx`
- Create: `web/src/components/ScopeEditor.tsx`
- Create: `web/src/components/TargetWorkspace.test.tsx`
- Modify: `web/src/styles.css`

**Interfaces:**
- Consumes: Task 7 REST DTOs.
- Produces: `TargetWorkspace` with a typed refresh signal for event-specific reloads.

- [ ] **Step 1: Add failing workspace tests**

Use real components and fetch fixtures, not component mocks:

```tsx
test('loads the site map and shows endpoint parameters without values', async () => {
  vi.stubGlobal('fetch', targetFetchFixture({ rebuildStatus: 'idle' }));
  render(<TargetWorkspace refresh={{ sequence: 0, type: 'initial' }} onOpenHistory={vi.fn()} onSendToRepeater={vi.fn()} />);
  await userEvent.click(await screen.findByRole('treeitem', { name: /example\.test/ }));
  await userEvent.click(screen.getByRole('treeitem', { name: /GET \/api\/users/ }));
  expect(await screen.findByText('user.email')).toBeInTheDocument();
  expect(screen.queryByText('secret@example.test')).not.toBeInTheDocument();
});

test('saves versioned scope rules and reports a conflict', async () => {
  const fetchMock = scopeConflictFetchFixture();
  vi.stubGlobal('fetch', fetchMock);
  render(<TargetWorkspace refresh={{ sequence: 0, type: 'initial' }} onOpenHistory={vi.fn()} onSendToRepeater={vi.fn()} />);
  await userEvent.click(await screen.findByRole('button', { name: 'Add include rule' }));
  await userEvent.type(screen.getByLabelText('Host pattern'), 'example.test');
  await userEvent.click(screen.getByRole('button', { name: 'Save scope' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('Scope changed in another session');
});
```

Add tests for text, method, status-family, and MIME filtering; endpoint request navigation; sending a representative request to Repeater; parameter timestamps/counts and parse diagnostics; rebuild progress; failed rebuild retry; disabled rules; exclude rules; mobile layout landmarks; and preserving the previous tree during rebuilding.

Define `targetFetchFixture` and `scopeConflictFetchFixture` in `TargetWorkspace.test.tsx` as local helpers returning `ReturnType<typeof vi.fn>`. The target fixture routes by method and pathname and returns literal scope/tree/endpoint/request/parameter/rebuild DTOs without parameter values. The conflict fixture delegates reads to the target fixture and returns HTTP 409 for the scope PUT. Any unexpected request must throw so missing API interactions fail the test.

- [ ] **Step 2: Run workspace tests and verify RED**

Run: `cd web && npm test -- TargetWorkspace.test.tsx`

Expected: FAIL because types, clients, and components do not exist.

- [ ] **Step 3: Add exact TypeScript DTOs and API calls**

```ts
export type ScopeAction = 'include' | 'exclude';
export interface ScopeRule { id: number; enabled: boolean; action: ScopeAction; scheme: '' | 'http' | 'https'; hostPattern: string; port: number; pathPrefix: string; }
export interface ScopeState { version: number; rules: ScopeRule[]; }
export interface TargetTreeNode { id: number; scheme: string; host: string; port: number; path: string; method: string; inScope: boolean; statuses: number[]; requestMimes: string[]; responseMimes: string[]; count: number; lastSeen: string; children: TargetTreeNode[]; }
export interface TargetEndpoint { id: number; scheme: string; host: string; port: number; path: string; method: string; inScope: boolean; firstSeen: string; lastSeen: string; count: number; statuses: number[]; requestMimes: string[]; responseMimes: string[]; parseDiagnostics: string[]; errorSeen: boolean; latestExchangeId: number; }
export interface TargetParameter { location: string; name: string; valueType: string; firstSeen: string; lastSeen: string; count: number; }
export interface TargetRequestRef { exchangeId: number; startedAt: string; status: number; error: boolean; }
export interface RebuildStatus { id: number; scopeVersion: number; activeScopeVersion: number; status: 'idle' | 'building' | 'active' | 'failed' | 'cancelled'; processed: number; total: number; error: string; }
export interface TargetRefresh { sequence: number; type: 'initial' | 'scope.changed' | 'target.endpoint.updated' | 'target.rebuild.started' | 'target.rebuild.progress' | 'target.rebuild.completed' | 'target.rebuild.failed'; }
```

Add `getScopeState`, `replaceScopeRules`, `getTargetTree`, `getTargetEndpoint`, `getTargetRequests`, `getTargetParameters`, `getRebuildStatus`, and `retryTargetRebuild` using the existing `api` and `jsonRequest` helpers.

- [ ] **Step 4: Implement focused components**

`TargetWorkspace` takes `refresh: TargetRefresh`, `onOpenHistory(exchangeId)`, and `onSendToRepeater(exchangeId)` props and owns loading/error/selection state. On `target.rebuild.started|progress|failed`, reload only rebuild status; on `target.endpoint.updated`, reload only the tree; on `scope.changed` or `target.rebuild.completed`, reload the affected scope/status/tree resources. It keeps text, current-scope (`all|in|out`), method, status-family, and MIME filters in local state and filters the aggregated tree metadata without extra detail requests. `SiteMapTree` receives immutable nodes plus selection callbacks and uses `role="tree"`, `role="group"`, and `role="treeitem"`. Endpoint request rows expose separate Open in History and Send to Repeater buttons. `ScopeEditor` works on a local rule draft, sends `{version, rules}`, and reloads server state after 409. Keep text filtering deferred with `useDeferredValue`; do not add `useMemo` or `useCallback` solely for performance.

Show the last complete tree while `building` or `failed`, label it stale when `scopeVersion != activeScopeVersion`, display bounded parse diagnostic labels, and derive an authentication indicator only from observed 401/403 statuses. A failed rebuild exposes Retry; an empty project renders an actionable empty state rather than treating the missing active generation as an error.

- [ ] **Step 5: Add responsive styles**

Use the existing visual language and CSS variables/colors. Desktop layout uses scope controls above a two-column tree/detail surface. Below 720 px, stack scope, tree, and endpoint detail; keep all controls keyboard reachable and avoid horizontal page overflow.

- [ ] **Step 6: Run workspace tests and build**

Run: `cd web && npm test -- TargetWorkspace.test.tsx`

Expected: PASS.

Run: `cd web && npm run build`

Expected: PASS with TypeScript type checking and Vite production output.

- [ ] **Step 7: Commit Task 8**

```bash
git add -- web/src/types.ts web/src/api/client.ts web/src/components/TargetWorkspace.tsx web/src/components/SiteMapTree.tsx web/src/components/ScopeEditor.tsx web/src/components/TargetWorkspace.test.tsx web/src/styles.css
git commit -m "feat: add target and scope workspace"
```

---

### Task 9: App Navigation, History Scope UX, and Live Events

**Files:**
- Modify: `web/src/App.tsx`
- Modify: `web/src/components/HistoryTable.tsx`
- Modify: `web/src/App.test.tsx`
- Modify: `web/src/styles.css`

**Interfaces:**
- Consumes: `TargetWorkspace`, scope API calls, and target WebSocket event names.
- Produces: complete operator navigation and History-to-scope flow.

- [ ] **Step 1: Add failing App integration tests**

```tsx
test('opens Target and refreshes it after target events', async () => {
  const socket = installFakeEventSocket();
  vi.stubGlobal('fetch', fullAppTargetFetchFixture());
  render(<App />);
  await userEvent.click(screen.getByRole('button', { name: 'Target' }));
  expect(await screen.findByRole('heading', { name: 'Site Map' })).toBeInTheDocument();
  socket.emit({ type: 'target.endpoint.updated', data: { endpointId: 7 } });
  await waitFor(() => expect(countFetches('/api/target/tree')).toBe(2));
});

test('adds a history origin to scope using the current version', async () => {
  vi.stubGlobal('fetch', historyAddToScopeFetchFixture());
  render(<App />);
  await screen.findByText('outside.test');
  await userEvent.click(screen.getByRole('button', { name: 'Add outside.test to scope' }));
  expect(lastScopeUpdate()).toEqual({ version: 2, rules: [expect.objectContaining({ action: 'include', scheme: 'https', hostPattern: 'outside.test' })] });
});
```

Add assertions for in-scope/out-of-scope badges, History scope filtering, selecting a target request opening the existing Inspector, sending it into the existing Repeater flow, and `target.rebuild.*` events refreshing progress without reloading unrelated History.

Define local helpers in `App.test.tsx`: `installFakeEventSocket()` returns a socket test double with `emit(message)`, `fullAppTargetFetchFixture()` handles every initial App and Target request, `countFetches(path)` reads the active fetch mock calls, `historyAddToScopeFetchFixture()` records the PUT body, and `lastScopeUpdate()` returns that parsed body. Reset their module-level recordings in `beforeEach`; every fixture throws on an unhandled request.

- [ ] **Step 2: Run App tests and verify RED**

Run: `cd web && npm test -- App.test.tsx`

Expected: FAIL because Target navigation and scope actions are absent.

- [ ] **Step 3: Integrate Target view and event revision**

Extend `view` to `'traffic' | 'target' | 'settings'`, add a `Target` navigation button using Lucide's `Map` icon, and render `TargetWorkspace` across columns 2 through 4. Pass an Open callback that selects the exchange and returns to Traffic, plus the existing `sendHistoryToRepeater` flow for Repeater actions. Store the latest target event as a monotonically sequenced `TargetRefresh` so the workspace reloads only affected resources; target events must not reload unrelated History. Keep History, Intercept, Repeater, and Settings behavior unchanged when their views are active.

- [ ] **Step 4: Add History scope controls**

Extend `HistoryTable` props with `query`, `scopeFilter: 'all' | 'in' | 'out'`, and `onAddOriginToScope(item: HistoryItem)`, then apply case-insensitive method/host/path/query search, render a compact scope badge, and add a scope filter control. Wire the existing History search input to `query`. Refactor each current outer row button into a non-interactive `role="row"` container with sibling selection and Add-to-scope buttons; do not nest interactive controls. In `App`, fetch the latest scope state, avoid duplicate equivalent include rules, parse the exchange host safely (including bracketed IPv6), append an origin rule with exact scheme/normalized host/effective port and path prefix `/`, then PUT with the current version. On 409, reload once and show a clear error rather than retrying the write automatically.

- [ ] **Step 5: Run frontend tests and build**

Run: `cd web && npm test`

Expected: all frontend tests PASS.

Run: `cd web && npm run build`

Expected: PASS.

- [ ] **Step 6: Commit Task 9**

```bash
git add -- web/src/App.tsx web/src/components/HistoryTable.tsx web/src/App.test.tsx web/src/styles.css
git commit -m "feat: connect target workspace to live traffic"
```

---

### Task 10: End-to-End Coverage, Documentation, and Release Verification

**Files:**
- Modify: `internal/proxy/proxy_test.go`
- Modify: `internal/api/server_test.go`
- Modify: `internal/store/sqlite_test.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: completed v2a backend and frontend.
- Produces: acceptance-level proof and operator documentation.

- [ ] **Step 1: Add failing end-to-end acceptance test**

Create one Go integration test that starts SQLite, scope manager, target service, proxy, API, local HTTP target, and local HTTPS target. It must prove in one controlled sequence:

```go
func TestTargetScopeEndToEnd(t *testing.T) {
	harness := newTargetHarness(t)
	harness.ReplaceScope([]scope.Rule{{Enabled: true, Action: scope.ActionInclude, HostPattern: "localhost", PathPrefix: "/allowed"}})
	harness.ProxyGET(harness.HTTPURL("/allowed?q=1"))
	harness.ProxyGET(harness.HTTPURL("/outside?secret=value"))
	harness.ProxyGET(harness.HTTPSURL("/allowed", "localhost"))

	history := harness.History()
	assertHistoryScope(t, history, "/allowed", true)
	assertHistoryScope(t, history, "/outside", false)
	tree := harness.TargetTree()
	assertTreeContains(t, tree, "localhost", "/allowed", "GET")
	assertTreeOmits(t, tree, "/outside")
	assertParameterNames(t, harness.ParametersFor("/allowed"), []string{"q"})

	harness.ReplaceScope([]scope.Rule{{Enabled: true, Action: scope.ActionInclude, HostPattern: "localhost"}})
	harness.WaitForRebuild()
	assertTreeContains(t, harness.TargetTree(), "localhost", "/outside", "GET")

	harness.ReplaceScope([]scope.Rule{{Enabled: true, Action: scope.ActionInclude, HostPattern: "localhost", PathPrefix: "/outside"}})
	harness.WaitForRebuild()
	assertTreeOmits(t, harness.TargetTree(), "/allowed")
}
```

The test must also enable interception and prove `/outside` bypasses the queue before the wider rule is activated.

Define `targetHarness` and its helpers in `proxy_test.go` as test-only code. `newTargetHarness(t)` creates and cleans up a temporary SQLite store, manager, target service, proxy/API servers, and HTTP/HTTPS targets. Its methods are `ReplaceScope([]scope.Rule)`, `ProxyGET(string)`, `HTTPURL(string) string`, `HTTPSURL(string, string) string`, `History() []store.HistoryItem`, `TargetTree() []store.TargetTreeNode`, `ParametersFor(string) []store.TargetParameter`, and `WaitForRebuild()`. Define `assertHistoryScope`, `assertTreeContains`, `assertTreeOmits`, and `assertParameterNames` beside the harness; each calls `t.Helper()` and fails with the complete observed collection.

- [ ] **Step 2: Run acceptance test and verify RED if any integration gap remains**

Run: `go test ./internal/proxy -run TestTargetScopeEndToEnd -count=1`

Expected before final integration fixes: FAIL at the first missing wiring or assertion.

- [ ] **Step 3: Make only integration-level corrections**

Correct constructor wiring, DTO field names, event names, SQL ordering, or test harness behavior revealed by the acceptance test. Do not add new product behavior beyond the approved spec.

- [ ] **Step 4: Run acceptance test and verify GREEN**

Run: `go test ./internal/proxy -run TestTargetScopeEndToEnd -count=1`

Expected: PASS.

- [ ] **Step 5: Document operator workflow**

Add README sections covering:

```markdown
## Target Scope

Open **Target**, add an include rule for an authorized host, and optionally add narrower path rules or exclusions. Traffic outside scope is still forwarded and visible in History, but it is not intercepted or added to the Site Map.

Changing scope starts a background Site Map rebuild from local History. The last complete Site Map remains available until the replacement finishes. Parameter inventory stores names and locations only; values remain in the original History exchange.
```

Include a safety note that an empty scope permits no interception and that future attack tools will require explicit scope.

- [ ] **Step 6: Run backend verification**

Run: `go test ./...`

Expected: PASS.

Run: `go test -race ./internal/scope ./internal/store ./internal/target ./internal/proxy ./internal/api`

Expected: PASS with no race reports.

Run: `go vet ./...`

Expected: exit 0 with no findings.

- [ ] **Step 7: Run frontend verification**

Run: `cd web && npm ci`

Expected: exit 0.

Run: `cd web && npm test`

Expected: all test files PASS.

Run: `cd web && npm run build`

Expected: TypeScript and Vite build PASS.

- [ ] **Step 8: Run cross-platform compilation and diff checks**

Run: `GOOS=windows GOARCH=amd64 go test -c -o /tmp/burpsuite-target.test.exe ./internal/target`

Expected: exit 0.

Run: `git diff --check`

Expected: exit 0.

- [ ] **Step 9: Commit Task 10**

```bash
git add -- internal/proxy/proxy_test.go internal/api/server_test.go internal/store/sqlite_test.go README.md
git commit -m "test: verify target scope workflow end to end"
```

---

## Completion Checklist

- [ ] Scope matching is normalized, deterministic, and shared by all consumers.
- [ ] Empty scope and exclude precedence enforce the safety boundary.
- [ ] Out-of-scope traffic forwards, persists, and bypasses interception.
- [ ] In-scope History projects incrementally into one active Site Map generation.
- [ ] Scope changes rebuild through a staging generation and atomically activate.
- [ ] Failed or cancelled rebuilds preserve the previous complete Site Map.
- [ ] Parameter inventory contains no request values.
- [ ] Target API routes retain existing local request validation.
- [ ] Target UI is live, accessible, responsive, and integrated with History and Repeater.
- [ ] Backend, race, vet, frontend, build, and Windows checks pass.
