# Bounded Crawler Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Discover unauthenticated pages and form metadata from an authorized History seed without submitting forms or launching vulnerability probes.

**Architecture:** A separate `internal/crawl` service reuses the existing History reader, Scope interface, and `repeater.Sender`. It persists bounded run/page/form metadata in new SQLite tables. The API and Scanner workspace provide explicit start, cancel, list, detail, and delete actions.

**Tech Stack:** Go, SQLite, React/TypeScript, Vitest.

**Spec:** `docs/superpowers/specs/2026-09-25-bounded-crawler-design.md`

## Global Constraints

- GET only; never submit forms, forward captured credentials, or execute scripts.
- Same origin and current Target Scope checked immediately before every request.
- One job at a time, at least 500 ms between requests, 5-second request timeout, 64 KiB body cap, no redirects, no environment proxy, TLS verification enabled.
- Hard caps: 25 pages, depth 3, 60 seconds total, 4 KiB URL length, 100 discovered form fields per run.
- No response bodies, captured headers, or form values in crawl storage.
- Crawl results never trigger active probes automatically.

## Review Focus

- A malformed or non-HTTP link must not trigger a network request: Task 1 parser test and Task 3 fixture test.
- A link to another origin, even if in scope, must not be fetched: Task 3 fixture test.
- Redirect and scope changes must not escape the chosen target: Task 3 fixture test.
- Cancellation during pacing or a request must stop subsequent requests: Task 3 cancellation test.
- A storage failure after a page must stop traffic rather than silently lose results: Task 3 failure test.

---

### Task 1: Safe URL and HTML extraction

**Files:** Create `internal/crawl/extract.go`, `internal/crawl/extract_test.go`.

**Interfaces:** Produce `NormalizeURL(raw string) (string, error)` and `ExtractHTML(base string, body []byte) (links []string, forms []Form, err error)`. `Form` has `PageURL`, `ActionURL`, `Method`, and `Fields []Field`; `Field` has `Name` and `Type`.

- [ ] Write table tests for fragment removal, canonical host/port, duplicate URLs, relative links, `javascript:`/`data:` exclusion, malformed HTML, and forms with unnamed/value-bearing fields. Assert values never appear in extracted forms.
- [ ] Run `go test ./internal/crawl -run 'TestNormalize|TestExtract'`; confirm tests fail for missing implementation.
- [ ] Implement extraction with the existing `golang.org/x/net/html` dependency; resolve links against the response URL, keep only HTTP(S), drop fragments, and cap input URL/field lengths. Do not return form values.
- [ ] Run the targeted tests and commit parser plus tests.

### Task 2: Durable crawl results

**Files:** Modify `internal/store/migrations.go`; create `internal/store/crawl.go`, `internal/store/crawl_test.go`; update migration-version assertions in existing store tests.

**Interfaces:** Produce `CrawlStore` methods `CreateCrawlRun(ctx context.Context, historyID int64, maxPages, maxDepth int) (int64, error)`, `AppendCrawlPage(ctx context.Context, runID int64, page CrawlPage, forms []CrawlForm) error`, `FinishCrawlRun(ctx context.Context, runID int64, state, reason string) error`, `RecoverCrawlRuns(ctx context.Context) error`, `ListCrawlRuns(ctx context.Context) ([]CrawlRun, error)`, `GetCrawlRun(ctx context.Context, id int64) (CrawlRun, error)`, and `DeleteCrawlRun(ctx context.Context, id int64) error`.

- [ ] Write store tests: create from an existing project exchange; append one page and form; reject duplicate/over-limit pages; recover a running job as interrupted; reject deletion of a running job; delete a finished job without deleting History; reject an unknown ID.
- [ ] Run `go test ./internal/store -run Crawl`; confirm the new tests fail.
- [ ] Add a migration with foreign keys and cascade from run to pages/forms; store only bounded URLs, statuses, content types, depths, truncation, safe error codes, field names/types, states, and timestamps. Keep a fixed 10,000-run cap and list the latest 100.
- [ ] Run `go test ./internal/store` and commit migration, repository, and tests.

### Task 3: Bounded crawl engine

**Files:** Create `internal/crawl/crawler.go`, `internal/crawl/crawler_test.go`; modify `cmd/proxy/main.go` to wire crawler and call `RecoverCrawlRuns` on startup.

**Interfaces:** Produce `Request{HistoryID int64, MaxPages int, MaxDepth int, Acknowledge bool}`, `Crawler.Start(ctx context.Context, input Request) (Report, error)`, and `Crawler.Cancel(id int64) error`. `Start` validates and creates a run synchronously, then launches traversal under a service-owned context, so closing the HTTP request does not cancel the job. Dependencies are History `GetExchange`, `CrawlStore`, Scope `Allows(string) bool`, and `repeater.Sender`. `Report` contains run ID, state, visited count, and stop reason.

- [ ] Write HTTP fixture tests proving GET-only requests, no original cookies/authorization headers, same-origin and scope restrictions, redirect rejection, page/depth/time limits, cancellation during wait/request, and persistence failure stopping further sends. Assert a form is discovered but never submitted.
- [ ] Run `go test ./internal/crawl -run TestCrawler`; confirm red tests.
- [ ] Implement breadth-first traversal with normalized visited-set, per-request scope check, strict sender options, 500 ms pacing, 60-second overall deadline, bounded queue, sequential sends, and persistence before the next fetch. Do not reuse captured request headers/body. Keep one service-owned cancel function keyed by run ID; finish state with a short independent context after cancellation.
- [ ] Run `go test -race ./internal/crawl ./internal/store` and commit service, wiring, and tests.

### Task 4: API, UI, and user documentation

**Files:** Create `internal/api/crawl.go`, `internal/api/crawl_test.go`; modify `internal/api/server.go`, `web/src/components/ActiveScannerWorkspace.tsx`, its test, `web/src/styles.css`, `docs/setup/active-scanner.md`, and `README.md`.

**Interfaces:** `POST /api/crawl/runs` returns `202 Accepted` with run ID immediately; `GET /api/crawl/runs`, `GET /api/crawl/runs/{id}`, `DELETE /api/crawl/runs/{id}`, and `POST /api/crawl/runs/{id}/cancel`. Cancellation returns conflict for a different/non-running ID. The UI polls run detail while running, starts jobs only after confirmation, and displays saved runs, progress, pages, and forms.

- [ ] Write API tests for validation, missing seed, scope denial, busy job, unavailable service/store, list/detail/delete, cancel, and no-store responses; write UI tests for confirmation, bounded parameters, cancel, reopen, and delete.
- [ ] Run `go test ./internal/api -run Crawl` and `npm test -- --run src/components/ActiveScannerWorkspace.test.tsx` from `web`; confirm red tests.
- [ ] Implement routes using existing request validation and response helpers. Keep crawler controls separate from reflection scanning, label results as discovery only, and document limits and authorization requirement.
- [ ] Run `go test -race ./...`, `go vet ./...`, `npm test`, `npm run build`, and `git diff --check`; commit the feature and push the existing PR branch only after green local checks. Check GitHub backend, frontend, and Windows jobs before reporting success.
