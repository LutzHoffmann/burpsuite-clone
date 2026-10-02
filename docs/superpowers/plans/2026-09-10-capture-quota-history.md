# Capture Quota and Paged History Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task.

**Goal:** Bound retained capture data without deletion and browse globally filtered history in stable 100-entry pages.

**Architecture:** SQLite transactions reserve capture charges alongside inserts. A separate paged-history interface provides snapshot/keyset navigation. API and React expose storage state without changing proxy forwarding.

**Tech Stack:** Go, SQLite, React, TypeScript, Vitest.

**Spec:** docs/superpowers/specs/2026-09-10-capture-quota-history-design.md

## Global Constraints

- Default 1 GiB, maximum 1 TiB, positive integer bytes. No automatic deletion.
- Capture accounting excludes derived data and SQLite overhead; label this clearly.
- 100 entries per page, ID snapshot, literal global search and server scope filters.
- Preserve local-only API protections and the existing draft PR.
- Workers edit disjoint files, do not commit other workers' edits, and report tests.

## Interfaces

```go
// internal/store/quota.go (Task 1)
var ErrCaptureQuotaExceeded = errors.New("capture storage quota exceeded")
type StorageStatus struct {
    LimitBytes int64 `json:"limitBytes"`
    UsedBytes int64 `json:"usedBytes"`
    Paused bool `json:"paused"`
    SkippedRecords int64 `json:"skippedRecords"`
}
type QuotaStore interface {
    StorageStatus(context.Context) (StorageStatus, error)
    SetStorageLimit(context.Context, int64) (StorageStatus, error)
}
// internal/store/history_page.go (Task 2)
type HistoryPageRequest struct {
    Filter HistoryFilter
    BeforeID int64
    SnapshotID int64
}
type HistoryPage struct {
    Items []HistoryItem `json:"items"`
    NextBeforeID int64 `json:"nextBeforeId"`
    SnapshotID int64 `json:"snapshotId"`
}
type HistoryPageStore interface {
    ListHistoryPage(context.Context, HistoryPageRequest) (HistoryPage, error)
}
// HistoryFilter gains InScope *bool.
```

### Task 1: Transactional Capture Budget

Files: new internal/store/quota.go and quota_test.go; modify sqlite.go, project.go,
migrations.go. Only this worker edits those existing files.

- [ ] Add failing SQLite tests for exact-fit/reject, pause/resume, restart, old data,
  concurrent store handles, rollback, notes/tags and Repeater accounting.
- [ ] Run `go test ./internal/store -run Quota -count=1`; confirm missing behavior.
- [ ] Add schema migration with per-record charges and singleton project quota.
  Compute old charges using SQL byte lengths; no full body loading.
- [ ] Reserve charge with an atomic conditional UPDATE as first transaction write;
  insert and commit together. Rejected inserts persist pause/skipped count but no
  capture. Maintain usage for metadata updates and do not delete records.
- [ ] Implement StorageStatus and SetStorageLimit; unchanged/lower limits must not
  accidentally clear a previously latched pause. Raising above usage resumes.
- [ ] Run `go test -race ./internal/store`; report migration and concurrency evidence.

### Task 2: Bounded History Queries

Files: new internal/store/history_page.go, history_page_test.go; modify store.go,
memory.go. Do not edit sqlite.go (coordinator replaces its legacy query afterward).

- [ ] Seed 201 records, fetch three pages, insert new records between pages and
  assert snapshot stability. Test filtered records beyond first 100 and literal %/_.
- [ ] Run `go test ./internal/store -run HistoryPage -count=1` to confirm failure.
- [ ] Implement independent SQL paging query returning at most 101 rows with bound
  parameters, literal search and ASCII-case-insensitive parity with memory store.
  Validate cursors in API; store must reject invalid negative/incompatible values.
- [ ] Implement memory paging without unbounded output and nullable InScope filter.
- [ ] Run package/race tests and report exact contract to coordinator.

### Task 3: Operator UI

Files: web/src only. Consume GET /api/history/page with beforeId, snapshotId,
search, method, host, inScope=true|false; zero nextBeforeId means no next page.
GET/PUT /api/storage use StorageStatus above and PUT {limitBytes:number}.
Repeater response adds saved:boolean and optional storageWarning:string.

- [ ] Add failing UI tests for paging, global search, scope, stale requests,
  new-traffic refresh while on old pages, quota warning and budget editing.
- [ ] Use current-page state and cursor stack; reset on query/filter change.
  Debounce search and discard outdated responses. Never fetch full history.
- [ ] Poll storage status every five seconds and on storage.status.changed.
  Display paused warning globally and saved=false warning for Repeater.
- [ ] Add Settings usage/limit editor in MiB, validate positive integers and max.
  Explain excluded database overhead; retain current visual language/mobile layout.
- [ ] Run `npm test` and `npm run build`, updating existing mocks to bounded API.

### Task 4: API, Forwarding and Whole-System Verification

Files: internal/api/storage.go, history_page.go, their tests, server.go, handlers.go;
internal/proxy/proxy.go; integration tests; README/setup docs.

- [ ] Add failing API tests for cursor validation, page DTOs, storage limits, and
  successful Repeater results when persistence is refused for quota.
- [ ] Register page/storage routes. Cap legacy history output at 100 by routing to
  paging (no full-query fallback). Update metadata quota rejection to HTTP 409.
- [ ] Expose storage status changes on pause/resume and settings updates. Detect
  transitions, not individual skips; UI polling is recovery for missed events.
- [ ] Catch quota errors in proxy persistence without altering response or emitting
  a success/history projection. Preserve response for Repeater with saved=false.
- [ ] Add real HTTP/HTTPS full-quota tests proving response forwarding and no new
  history entry. Update SQLite legacy ListHistory to call bounded paging after
  Task 1 relinquishes sqlite.go.
- [ ] Run `go test -race ./...`, `go vet ./...`, Windows cross-compilation, frontend
  tests/build, independent spec/quality review and fix actionable findings.
- [ ] Document behavior and update draft PR; push tested code and wait CI green.

## Execution Ledger

- Ruling: use the existing isolated worktree and approved defaults without another
  execution-choice prompt; user has approved implementation and prior PR updates.
- Ruling: legacy history endpoint becomes bounded at 100, retaining array shape;
  paged endpoint has explicit cursor metadata. This avoids an unbounded escape path.
- Tasks 1-4 implemented. Full Go race suite, vet, Windows cross-compilation,
  93 frontend tests and production build passed on 2026-09-14.
- Backend review found malformed query parsing and out-of-order storage events.
  Fixed strict RawQuery parsing and persisted transition revisions; regression
  tests cover both. The earlier reviewer was unavailable for a final re-review.
- Existing data is retained. Budget accounting excludes database overhead.
  HTTP/HTTPS quota-full integration tests verify continued forwarding and resume.
- Live API smoke checks used an isolated temporary project. No final visual
  browser check was performed for this increment.
