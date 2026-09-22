# Intruder/Fuzzer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a persistent, scope-bound, rate-limited HTTP Intruder that supports Sniper, Battering Ram, Pitchfork, and Cluster Bomb jobs with live controls and result analysis.

**Architecture:** A new `internal/intruder` package owns pure generation and concurrent job execution behind store, scope, sender, and event interfaces. SQLite persists immutable started configurations and ordered results; the local API maps bounded DTOs onto the service; React provides job configuration and live result inspection without moving execution policy into the browser.

**Tech Stack:** Go 1.24, `net/http`, SQLite through `modernc.org/sqlite`, React 19, TypeScript 5.8, Vitest, Testing Library.

**Spec:** `docs/superpowers/specs/2026-09-22-intruder-fuzzer-design.md`

## Global Constraints

- Only `http` and `https` targets are supported.
- Every create, start, resume, and generated send requires the current destination to match Target Scope; there is no override.
- Jobs never start or resume automatically, including after application restart.
- Generated request totals are limited to 1 through 100,000; concurrency to 1 through 20; rate to 0.1 through 100 requests per second; timeout to 1 through 120 seconds.
- Request templates are limited to 2 MiB, individual payloads to 1 MiB, total payload count to 100,000, and retained response bodies to 2 MiB.
- Redirects, environment proxy discovery, implicit credential forwarding, and conflicting request-framing headers are disabled.
- State-changing API routes retain Host, Origin, content-type, JSON size, and unknown-field validation.
- Payload bytes, headers, and bodies never appear in logs or events.
- Existing Proxy, History, Intercept, Repeater, Target, WebSocket, and storage behavior must remain compatible.

## Review Focus

- A scope rule changes after a worker selects an item but before dispatch: the sender must perform a final classification and send no request when excluded; Task 5 adds this race test.
- Cluster Bomb count multiplication overflows or exceeds 100,000: validation must reject it without allocating combinations; Task 1 adds boundary tests.
- Pause, abort, and shutdown race with a completed network response: exactly one ordered result and one terminal transition must be persisted; Task 5 adds race tests.
- SQLite reaches the capture budget after response metadata exists: metadata must persist with omitted bodies and an explicit storage status, or the job must pause before unrecorded traffic continues; Task 3 adds quota tests.
- A stale browser response arrives after another job or page is selected: UI state must ignore it; Task 9 adds deferred-response tests.

---

## File Structure

- `internal/intruder/types.go`: domain types, limits, state constants, and dependency interfaces.
- `internal/intruder/generator.go`: validation, exact count calculation, and lazy deterministic payload iteration.
- `internal/intruder/request.go`: canonical template construction and byte-safe substitutions.
- `internal/intruder/analysis.go`: bounded response comparison and result summaries.
- `internal/intruder/service.go`: draft CRUD, state machine, startup recovery, and public service methods.
- `internal/intruder/runner.go`: limiter, workers, final scope check, persistence ordering, pause, and abort.
- `internal/intruder/*_test.go`: focused unit and race tests.
- `internal/repeater/sender.go`: shared bounded HTTP sender used by Repeater and Intruder.
- `internal/store/intruder.go`: SQLite implementation of Intruder persistence and result pagination.
- `internal/store/intruder_test.go`: migration, atomicity, isolation, quota, and pagination tests.
- `internal/store/migrations.go`: schema version 8 and Intruder tables.
- `internal/api/intruder.go`: DTO conversion, validation mapping, lifecycle handlers, and result endpoints.
- `internal/api/intruder_test.go`: route, middleware, conflict, and bounded-response tests.
- `internal/api/server.go`: service dependency and route registration.
- `cmd/proxy/main.go`: Intruder service construction, recovery, and shutdown.
- `web/src/components/IntruderWorkspace.tsx`: job list, editor, configuration, preflight, controls, and result table.
- `web/src/components/IntruderWorkspace.test.tsx`: complete workspace behavior tests.
- `web/src/api/intruder.ts`: typed Intruder API client.
- `web/src/types.ts`: Intruder DTO types.
- `web/src/App.tsx`: Intruder navigation and Send-to-Intruder handoff.
- `web/src/components/HistoryTable.tsx`: Send-to-Intruder action.
- `web/src/components/Repeater.tsx`: Send-to-Intruder action using current draft.
- `web/src/styles.css`: responsive Intruder workspace styling.
- `internal/integration/intruder_test.go`: authorized HTTP/HTTPS, scope revocation, restart, and control integration.
- `docs/setup/intruder.md`: operator guide, limits, attack semantics, and safety notes.
- `README.md`: feature and documentation link.

### Task 1: Domain Model and Lazy Attack Generator

**Files:**
- Create: `internal/intruder/types.go`
- Create: `internal/intruder/generator.go`
- Create: `internal/intruder/generator_test.go`

**Interfaces:**
- Produces: `ValidateConfig(Config) (int64, error)` and `NewIterator(Config, int64) (*Iterator, error)`.
- Produces: `(*Iterator).Next() (Combination, bool)` where `Combination` carries sequence and one payload selection per position.
- Consumes: no persistence, network, API, or UI dependencies.

- [ ] **Step 1: Define the domain and failing validation tests**

```go
type AttackType string
const (
    AttackSniper AttackType = "sniper"
    AttackBatteringRam AttackType = "battering_ram"
    AttackPitchfork AttackType = "pitchfork"
    AttackClusterBomb AttackType = "cluster_bomb"
)
type Position struct { ID string; Start, End int; PayloadSetID string }
type PayloadSet struct { ID string; Payloads [][]byte }
type Config struct {
    Attack AttackType
    Template Template
    Positions []Position
    PayloadSets []PayloadSet
    RequestLimit, Concurrency int
    RatePerSecond float64
    Timeout time.Duration
}
```

Add table tests for legal minimums/defaults; unsupported attacks; zero-width, overlapping, unsorted, and out-of-range positions; missing and empty assigned sets; 2 MiB template boundary; 1 MiB payload boundary; 100,000 payload boundary; NaN/Inf rates; and exact server-side limit ranges.

- [ ] **Step 2: Run the domain tests and confirm they fail**

Run: `go test ./internal/intruder -run 'TestValidateConfig|TestCount' -count=1`

Expected: FAIL because the package and validation functions do not exist.

- [ ] **Step 3: Implement bounded validation and overflow-safe counts**

Use division-before-multiplication for Cluster Bomb:

```go
if count > int64(cfg.RequestLimit)/listLength {
    return 0, ErrRequestLimit
}
count *= listLength
```

Return typed field errors containing field and stable code, never raw payload content.

- [ ] **Step 4: Add failing deterministic iteration tests**

Pin the complete emitted sequence for all four attack types, including duplicate and empty payload values, independent Sniper positions, shortest-list Pitchfork behavior, mixed-radix Cluster Bomb order, and resume from sequence 0, a middle sequence, and total count.

- [ ] **Step 5: Implement a lazy iterator**

Store indexes only. `Next` must copy exposed payload slices, never allocate the Cartesian product, and return `false` exactly at the validated count.

- [ ] **Step 6: Run focused tests and race detection**

Run: `go test -race ./internal/intruder -run 'TestValidateConfig|TestCount|TestIterator' -count=1`

Expected: PASS.

- [ ] **Step 7: Commit the generator**

```bash
git add internal/intruder/types.go internal/intruder/generator.go internal/intruder/generator_test.go
git commit -m "Add bounded Intruder attack generator"
```

### Task 2: Canonical Requests and Shared Bounded Sender

**Files:**
- Create: `internal/intruder/request.go`
- Create: `internal/intruder/request_test.go`
- Create: `internal/repeater/sender.go`
- Create: `internal/repeater/sender_test.go`
- Modify: `internal/repeater/repeater.go`
- Modify: `internal/repeater/repeater_test.go`

**Interfaces:**
- Consumes: `intruder.Combination`, `intruder.Template`, and validated positions from Task 1.
- Produces: `BuildRequest(Template, []Position, Combination) (repeater.SendRequest, error)`.
- Produces: `repeater.Sender` interface with `Send(context.Context, SendRequest, SendOptions) (SendResult, error)`.

- [ ] **Step 1: Write failing substitution and framing tests**

Cover substitutions before and after multibyte UTF-8, binary bytes, adjacent positions, empty payload values, ordered repeated headers, body length changes, and immutable scheme/host/port. Reject CR/LF header names or values, simultaneous `Content-Length` and `Transfer-Encoding`, unsupported schemes, userinfo, fragments, and oversized generated requests.

- [ ] **Step 2: Run request tests and confirm failure**

Run: `go test ./internal/intruder -run TestBuildRequest -count=1`

Expected: FAIL because `BuildRequest` does not exist.

- [ ] **Step 3: Implement byte-safe canonical construction**

Apply replacements from the highest byte offset downward, rebuild the URL and header/body structure, remove hop-by-hop proxy headers, and set `Content-Length` from final body bytes.

- [ ] **Step 4: Write failing shared-sender compatibility tests**

Verify no redirect following, no environment proxy, exact headers/body, timeout classification, 2 MiB response capture, content-length metadata, decompression bounds, and cancellation. Re-run all existing Repeater tests against the extracted sender.

- [ ] **Step 5: Extract the sender without changing Repeater behavior**

```go
type SendOptions struct { Timeout time.Duration; BodyLimitBytes int64 }
type Sender interface {
    Send(context.Context, SendRequest, SendOptions) (SendResult, error)
}
```

Keep `Service.Send` as the compatibility entry point delegating to the sender with the existing 60-second timeout.

- [ ] **Step 6: Verify focused and existing Repeater tests**

Run: `go test -race ./internal/intruder ./internal/repeater -count=1`

Expected: PASS.

- [ ] **Step 7: Commit request generation and transport reuse**

```bash
git add internal/intruder/request.go internal/intruder/request_test.go internal/repeater
git commit -m "Share bounded HTTP sender with Intruder"
```

### Task 3: SQLite Schema and Intruder Store

**Files:**
- Modify: `internal/store/migrations.go`
- Create: `internal/store/intruder.go`
- Create: `internal/store/intruder_test.go`
- Modify: `internal/intruder/types.go`

**Interfaces:**
- Produces: `intruder.Store` with atomic draft writes, lifecycle compare-and-swap, ordered result append, cursor pages, startup recovery, and deletion.
- Consumes: active project identity and existing capture quota primitives.

- [ ] **Step 1: Add failing migration and constraint tests**

Assert schema version 8 creates `intruder_jobs`, `intruder_positions`, `intruder_payload_sets`, `intruder_payloads`, and `intruder_results`; all rows are project-owned; foreign keys cascade; state/attack checks reject invalid values; sequence is unique per job; and existing schema versions migrate without data loss.

- [ ] **Step 2: Run migration tests and confirm failure**

Run: `go test ./internal/store -run 'TestIntruderMigration|TestIntruderConstraints' -count=1`

Expected: FAIL because schema version 8 is absent.

- [ ] **Step 3: Implement schema version 8**

Store binary templates and payloads as BLOBs, nanosecond timestamps as integers, rate as a validated decimal value, revisions as increasing integers, and indexes on `(project_id, updated_at)`, `(job_id, sequence)`, status, size, duration, and similarity.

- [ ] **Step 4: Define the persistence interface and failing store tests**

```go
type Store interface {
    CreateDraft(context.Context, Draft) (Job, error)
    ReplaceDraft(context.Context, string, int64, Config) (Job, error)
    ListJobs(context.Context) ([]JobSummary, error)
    GetJob(context.Context, string) (Job, error)
    Transition(context.Context, string, int64, State, State, string) (Job, error)
    AppendResult(context.Context, string, Result) (Job, error)
    ListResults(context.Context, string, ResultQuery) (ResultPage, error)
    GetResult(context.Context, string, int64) (Result, error)
    RecoverRunning(context.Context) error
    DeleteJob(context.Context, string) error
}
```

Test project isolation, atomic draft replacement, stale revision conflict, legal/illegal transitions, result idempotency, cursor stability under new results, every filter, baseline update, terminal deletion, running deletion rejection, and restart recovery.

- [ ] **Step 5: Implement the SQLite store and quota behavior**

Reserve capture quota in the same transaction as result insertion. If body reservation fails, persist bounded metadata with `BodyStored=false` and `StorageStatus="quota_exceeded"`; if metadata cannot be committed, return an error so the service pauses before another send.

- [ ] **Step 6: Run store and migration tests**

Run: `go test -race ./internal/store -run Intruder -count=1`

Expected: PASS.

- [ ] **Step 7: Commit persistence**

```bash
git add internal/store/migrations.go internal/store/intruder.go internal/store/intruder_test.go internal/intruder/types.go
git commit -m "Persist Intruder jobs and results"
```

### Task 4: Result Analysis

**Files:**
- Create: `internal/intruder/analysis.go`
- Create: `internal/intruder/analysis_test.go`

**Interfaces:**
- Produces: `Analyze(ResultCapture, *ResultCapture) Analysis`.
- Consumes: captured response metadata and at most 2 MiB of response bytes.

- [ ] **Step 1: Write failing analysis tests**

Cover no baseline, exact match, status-only change, MIME change with parameters normalized, empty bodies, truncated bodies, binary content, maximum captures, deterministic similarity, and duration/length deltas without integer overflow.

- [ ] **Step 2: Run analysis tests and confirm failure**

Run: `go test ./internal/intruder -run TestAnalyze -count=1`

Expected: FAIL because analysis is absent.

- [ ] **Step 3: Implement deterministic bounded analysis**

Use fixed-size chunk hashes to compute an integer similarity from 0 through 10,000, normalize MIME media types with `mime.ParseMediaType`, and mark similarity partial when either body is truncated or omitted.

- [ ] **Step 4: Verify tests and commit**

Run: `go test -race ./internal/intruder -run TestAnalyze -count=1`

```bash
git add internal/intruder/analysis.go internal/intruder/analysis_test.go
git commit -m "Add Intruder response analysis"
```

### Task 5: Job Service, Scheduler, and Controls

**Files:**
- Create: `internal/intruder/service.go`
- Create: `internal/intruder/service_test.go`
- Create: `internal/intruder/runner.go`
- Create: `internal/intruder/runner_test.go`

**Interfaces:**
- Consumes: `intruder.Store`, `repeater.Sender`, a current-scope classifier, and event publisher.
- Produces: `Create`, `Update`, `Delete`, `Start`, `Pause`, `Resume`, `Abort`, `List`, `Get`, `ListResults`, `GetResult`, and `Close` methods.

- [ ] **Step 1: Write failing lifecycle matrix tests**

Pin every accepted and rejected transition among `draft`, `running`, `pausing`, `paused`, `aborting`, `aborted`, `completed`, and `failed`; optimistic revisions; two running jobs per project; four globally; and startup recovery to `paused/application_restarted` without sends.

- [ ] **Step 2: Run service tests and confirm failure**

Run: `go test ./internal/intruder -run 'TestService|TestLifecycle' -count=1`

Expected: FAIL because the service is absent.

- [ ] **Step 3: Implement service ownership and transition serialization**

Maintain one runtime handle per running job with context cancellation, pause intent, and joined completion. Persist each transition before publishing its identifier-only event.

- [ ] **Step 4: Write failing worker, rate, and final-scope tests**

Use a fake monotonic clock, blocking sender, mutable scope classifier, and recording store. Verify no startup burst above concurrency; exact 0.1 and 100 rates; final scope revocation between selection and send; pause lets in-flight work record once; abort cancels and joins; shutdown produces resumable paused state; sender/store errors; panic recovery; and sequence resume without duplicates.

- [ ] **Step 5: Implement runner and ordered persistence**

Workers may execute concurrently but hand completed outcomes to one per-job persistence coordinator. The coordinator buffers out-of-order outcomes until the next sequence is available, persists exactly once, advances `NextSequence`, and emits coalesced progress at no more than 5 Hz.

- [ ] **Step 6: Run repeated race tests**

Run: `go test -race ./internal/intruder -run 'TestService|TestLifecycle|TestRunner' -count=20`

Expected: PASS with no leaks or data races.

- [ ] **Step 7: Commit the execution engine**

```bash
git add internal/intruder/service.go internal/intruder/service_test.go internal/intruder/runner.go internal/intruder/runner_test.go
git commit -m "Run controlled Intruder jobs"
```

### Task 6: Intruder API

**Files:**
- Create: `internal/api/intruder.go`
- Create: `internal/api/intruder_test.go`
- Modify: `internal/api/server.go`

**Interfaces:**
- Consumes: the Task 5 service through an `IntruderService` interface in `internal/api/intruder.go`.
- Produces: the eleven JSON routes specified by the design and stable bounded DTOs.

- [ ] **Step 1: Write failing route and security tests**

Test every method/path, unavailable service, malformed IDs, strict JSON, wrong content type, oversized body, hostile Host, cross-origin mutation, base64 absent/empty/invalid cases, stale revision `409`, state conflict `409`, scope rejection `403`, missing row `404`, request limit `422`, and internal error redaction.

- [ ] **Step 2: Run API tests and confirm failure**

Run: `go test ./internal/api -run Intruder -count=1`

Expected: FAIL because routes are not registered.

- [ ] **Step 3: Implement strict DTO conversion and handlers**

```go
type intruderService interface {
    Create(context.Context, intruder.CreateRequest) (intruder.Job, error)
    Update(context.Context, string, int64, intruder.Config) (intruder.Job, error)
    Start(context.Context, string, int64) (intruder.Job, error)
    Pause(context.Context, string, int64) (intruder.Job, error)
    Resume(context.Context, string, int64) (intruder.Job, error)
    Abort(context.Context, string, int64) (intruder.Job, error)
}
```

Extend the interface with read/delete methods used by handlers. Map typed domain errors centrally and set `Cache-Control: no-store` on job and result responses.

- [ ] **Step 4: Test pagination, filters, and event privacy**

Assert stable cursors, all documented filters, bounded detail bodies, and that published events contain only job ID, sequence, state, and numeric progress.

- [ ] **Step 5: Verify API tests and commit**

Run: `go test -race ./internal/api -run Intruder -count=1`

```bash
git add internal/api/intruder.go internal/api/intruder_test.go internal/api/server.go
git commit -m "Expose bounded Intruder API"
```

### Task 7: Application Wiring and Shutdown

**Files:**
- Modify: `cmd/proxy/main.go`
- Modify: `internal/api/server.go`
- Create: `internal/integration/intruder_test.go`

**Interfaces:**
- Consumes: SQLite store, current scope manager, shared sender, events hub, and Intruder service.
- Produces: one process-owned service closed before store shutdown.

- [ ] **Step 1: Add failing construction and recovery integration tests**

Start the application fixture with persisted running-like jobs and assert they recover paused without network traffic. Verify service-unavailable behavior is replaced by a live configured service.

- [ ] **Step 2: Run the integration tests and confirm failure**

Run: `go test ./internal/integration -run IntruderRecovery -count=1`

Expected: FAIL because the service is not wired.

- [ ] **Step 3: Wire dependencies and ordered shutdown**

Construct the sender, service, and recovery before serving HTTP. On shutdown, stop accepting new starts, cancel and join runners, persist paused recovery state, then close target services and SQLite.

- [ ] **Step 4: Verify integration and existing startup tests**

Run: `go test -race ./cmd/proxy ./internal/integration -run 'Intruder|Startup|Shutdown' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit application wiring**

```bash
git add cmd/proxy/main.go internal/api/server.go internal/integration/intruder_test.go
git commit -m "Wire Intruder service lifecycle"
```

### Task 8: Typed Frontend Client and Editor Primitives

**Files:**
- Modify: `web/src/types.ts`
- Create: `web/src/api/intruder.ts`
- Create: `web/src/api/intruder.test.ts`
- Create: `web/src/components/IntruderEditor.tsx`
- Create: `web/src/components/IntruderEditor.test.tsx`

**Interfaces:**
- Produces: typed API functions for all Intruder routes.
- Produces: `IntruderEditor` accepting a draft, returning validated template/position changes, and preserving arbitrary bytes in text/hex modes.

- [ ] **Step 1: Write failing API encoding tests**

Test base64 conversion for empty and binary bytes, strict response decoding assumptions, cursor/filter encoding, stale revision bodies, and `ApiError` propagation.

- [ ] **Step 2: Add DTO types and client functions**

Define discriminated state and attack unions, byte fields as base64 strings at the API boundary, and exact request/response types rather than untyped records.

- [ ] **Step 3: Write failing editor interaction tests**

Test selecting bytes and adding a position, rejecting overlaps, deleting/reassigning positions, editing URL/headers/body, UTF-8 and hex round trips, malformed hex feedback, binary preservation, and request-size feedback.

- [ ] **Step 4: Implement the editor with byte offsets**

Keep canonical bytes as state; derive text only when strict UTF-8 decoding succeeds. Convert DOM selection offsets through UTF-8 encoding before creating byte ranges.

- [ ] **Step 5: Verify frontend primitives and commit**

Run from `web/`: `npm test -- src/api/intruder.test.ts src/components/IntruderEditor.test.tsx --maxWorkers=1`

```bash
git add web/src/types.ts web/src/api/intruder.ts web/src/api/intruder.test.ts web/src/components/IntruderEditor.tsx web/src/components/IntruderEditor.test.tsx
git commit -m "Add Intruder client and byte-safe editor"
```

### Task 9: Intruder Workspace and Navigation

**Files:**
- Create: `web/src/components/IntruderWorkspace.tsx`
- Create: `web/src/components/IntruderWorkspace.test.tsx`
- Modify: `web/src/App.tsx`
- Modify: `web/src/App.test.tsx`
- Modify: `web/src/components/HistoryTable.tsx`
- Modify: `web/src/components/Repeater.tsx`
- Modify: `web/src/styles.css`

**Interfaces:**
- Consumes: Task 8 client/editor and event stream refresh signals.
- Produces: complete job configuration, preflight, controls, pageable results, and Send-to-Intruder handoffs.

- [ ] **Step 1: Write failing job-list and draft workflow tests**

Test empty state, create from scratch, create from History, current Repeater draft handoff, attack selection, payload list text/hex entry, exact preview counts, Cluster Bomb confirmation above 10,000, field errors, optimistic conflict reload, and dirty navigation guard.

- [ ] **Step 2: Implement draft configuration and preflight**

Show server-returned exact count as authoritative. Disable Start until the persisted revision is current, scope is confirmed, and all validation errors are clear.

- [ ] **Step 3: Write failing lifecycle and live-result tests**

Test start, pause, resume, abort, scope-revoked status, restart-paused status, coalesced event refresh, result cursor navigation, filters, baseline selection, Inspector detail, Send-to-Repeater, quota warning, and stale deferred responses after switching jobs/pages.

- [ ] **Step 4: Implement controls and bounded result rendering**

Render only the current server page and use fixed-height rows, so the browser never mounts the full result set. Guard asynchronous responses with request revisions and `AbortController`.

- [ ] **Step 5: Add responsive intentional styling**

Extend the existing visual language with a three-column desktop workspace and stacked mobile workflow. Preserve the established typography and colors; use progress motion only while a job is running and respect reduced-motion preferences.

- [ ] **Step 6: Verify workspace and regression tests**

Run from `web/`: `npm test -- src/components/IntruderWorkspace.test.tsx src/App.test.tsx --maxWorkers=1`

Expected: PASS.

- [ ] **Step 7: Commit the workspace**

```bash
git add web/src/components/IntruderWorkspace.tsx web/src/components/IntruderWorkspace.test.tsx web/src/App.tsx web/src/App.test.tsx web/src/components/HistoryTable.tsx web/src/components/Repeater.tsx web/src/styles.css
git commit -m "Add Intruder workspace"
```

### Task 10: End-to-End Safety, Documentation, and Full Verification

**Files:**
- Modify: `internal/integration/intruder_test.go`
- Create: `docs/setup/intruder.md`
- Modify: `README.md`

**Interfaces:**
- Consumes: all prior tasks.
- Produces: acceptance-level evidence and operator documentation.

- [ ] **Step 1: Add failing authorized HTTP and HTTPS acceptance tests**

Run each attack type against deterministic local fixtures and assert exact request bytes/order, result metadata, TLS behavior, timeout handling, and baseline analysis.

- [ ] **Step 2: Add scope, control, restart, and quota acceptance tests**

Assert an out-of-scope draft cannot start; scope revocation sends no later request; pause/resume has no duplicates; abort cancels in-flight work; restart causes no traffic; and quota exhaustion is explicit while metadata remains consistent.

- [ ] **Step 3: Run integration tests repeatedly under race detection**

Run: `go test -race ./internal/integration -run Intruder -count=10`

Expected: PASS.

- [ ] **Step 4: Write the operator guide and update README**

Document all four attack types, position semantics, limits, scope behavior, lifecycle controls, result interpretation, binary modes, quota behavior, and the authorization warning. Include a local-fixture walkthrough that cannot target a public system accidentally.

- [ ] **Step 5: Run complete backend verification**

Run: `go test -race ./... -count=1`

Run: `go vet ./...`

Run: `env GOOS=windows GOARCH=amd64 go build ./...`

Expected: all commands exit 0.

- [ ] **Step 6: Run complete frontend verification**

Run from `web/`: `npm test -- --maxWorkers=1`

Run from `web/`: `npm run build`

Expected: all tests pass and the production bundle builds.

- [ ] **Step 7: Perform browser smoke testing**

Use only loopback HTTP/HTTPS fixtures. Verify create, positions, each attack preview, start, live results, pause/resume, abort, filtering, Inspector, Repeater handoff, dirty navigation, 1280 px desktop, and 390 px mobile without horizontal overflow.

- [ ] **Step 8: Commit docs and acceptance coverage**

```bash
git add internal/integration/intruder_test.go docs/setup/intruder.md README.md
git commit -m "Document and verify Intruder workflow"
```

- [ ] **Step 9: Request whole-feature review, address findings, and repeat full verification**

Review against the design acceptance criteria, with special focus on scope timing, unbounded allocations, state races, sensitive event data, SQLite atomicity, and byte-preserving UI behavior. Fix validated findings in separate commits and rerun Steps 5 through 7.

- [ ] **Step 10: Push and verify the existing draft pull request**

Push `implement-burpsuite-clone-v1`, update PR #1 with the Intruder milestone and verification evidence, attach the PR if needed, and wait until backend, frontend, and Windows CI jobs all complete successfully. Keep the PR as a draft and do not merge without an explicit request.
