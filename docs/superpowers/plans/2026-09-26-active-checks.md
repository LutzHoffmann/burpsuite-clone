# Bounded Active Checks Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Run explicitly authorized, bounded GET reflection observations against query inputs discovered by the crawler.

**Architecture:** A crawler migration preserves only query parameter names, never values. A separate active-check service selects bounded candidates from a completed crawl, sends one synthetic marker per input, classifies exact reflection conservatively, and persists redacted metadata. The API and Scanner UI start, monitor, cancel, reopen, and delete runs.

**Tech Stack:** Go 1.23, SQLite, `golang.org/x/net/html`, React/TypeScript, Vitest.

**Spec:** `docs/superpowers/specs/2026-09-26-active-checks-design.md`

## Global Constraints

- Only explicit, confirmed, same-origin, in-scope GET requests; no POST, captured credentials, cookies, headers, or original query values.
- One active-check job at a time; maximum 25 candidates, five names per candidate, 100 requests, 500 ms between requests, five-second request timeout, 64 KiB response cap, 60-second overall deadline.
- No redirects or environment proxy; keep TLS verification enabled.
- Do not store marker values, response bodies/snippets, URL query values, or form values.
- Reflection/context is an observation, never a confirmed vulnerability.

## Review Focus

- A crawl from an older schema has no parameter-name data and remains readable: Task 1 migration test.
- A form action with a different origin or method never becomes a candidate: Task 4 service test.
- A marker occurring only in headers or transformed text is not an exact body reflection: Task 2 classifier test.
- Cancellation while pacing prevents the next request: Task 4 cancellation test.
- Persistence failure after a send prevents any further requests: Task 4 failure test.

---

### Task 1: Save bounded query parameter names

**Files:** Modify `internal/store/migrations.go`, `internal/store/crawl.go`, `internal/store/crawl_test.go`, `internal/crawl/crawler.go`, `internal/crawl/crawler_test.go`; update migration-version assertions in store tests.

**Interfaces:** Extend `store.CrawlPage` with `QueryNames []string`; add `crawl_page_query_names(run_id,url_hash,sequence,name)` keyed to `crawl_pages`. `GetCrawlRun` returns sorted names with each page. `AppendCrawlPage` derives names from the full page URL before redaction, at most 20 distinct names of 1-256 bytes, and does not persist values.

- [ ] Test two query URLs whose values differ: both pages persist, `QueryNames` contains only distinct names, and a raw SQLite search cannot find either value; reopen a pre-migration fixture and expect empty names.
- [ ] Run `go test ./internal/store ./internal/crawl -run 'Crawl|QueryNames'`; confirm the new test fails.
- [ ] Add migration version 11 and update read/write paths. Preserve the existing `url_hash` identity and query-value redaction. Add the crawler test assertion for names.
- [ ] Run `go test ./internal/store ./internal/crawl`; commit migration and tests.

### Task 2: Exact-marker context classification

**Files:** Create `internal/activechecks/classify.go`, `internal/activechecks/classify_test.go`.

**Interfaces:** `Classify(body []byte, contentType, marker string, truncated bool) Observation` where `Observation{Found bool, Context string, Partial bool}` and context is `plain_text`, `html_text`, `html_attribute`, `raw_text`, or `unknown`. The classifier never returns a vulnerability label.

- [ ] Test exact marker in text, attribute, script/style, and plain text; altered marker, header-only marker, malformed HTML, and truncated capture return `unknown`/`Partial` conservatively.
- [ ] Run `go test ./internal/activechecks -run TestClassify`; confirm red.
- [ ] Implement bounded tokenizer-based classification. Search only response body; ambiguous or malformed contexts become `unknown` and truncated captures always set `Partial`.
- [ ] Run the targeted tests and commit classifier and tests.

### Task 3: Persist active-check runs and observations

**Files:** Modify `internal/store/migrations.go`; create `internal/store/active_checks.go`, `internal/store/active_checks_test.go`; update migration-version assertions.

**Interfaces:** `ActiveCheckStore` exposes `CreateActiveCheckRun(ctx, crawlID int64) (int64,error)`, `AppendActiveCheck(ctx, runID int64, item ActiveCheckObservation) error`, `FinishActiveCheckRun(ctx,runID int64,state,reason string) error`, `RecoverActiveCheckRuns(ctx) error`, `ListActiveCheckRuns(ctx) ([]ActiveCheckRun,error)`, `GetActiveCheckRun(ctx,id int64) (ActiveCheckRun,error)`, and `DeleteActiveCheckRun(ctx,id int64) error`. `ActiveCheckObservation` stores redacted URL, source (`query` or `get_form`), parameter name, status, found/context/partial, and safe error code.

- [ ] Test create only from a completed crawl, bounded append, duplicate rejection, recovery, list/detail, delete preserving crawl/History, and absence of marker/query values in the database.
- [ ] Run `go test ./internal/store -run ActiveCheck`; confirm red.
- [ ] Add migration version 12 with foreign keys and constraints; cap each run at 100 observations, latest list at 100, total runs at 10,000. Persist each observation transactionally before the next send.
- [ ] Run `go test ./internal/store`; commit store and tests.

### Task 4: Bounded active-check service

**Files:** Create `internal/activechecks/service.go`, `internal/activechecks/service_test.go`; modify `cmd/proxy/main.go` for startup recovery and wiring.

**Interfaces:** `Request{CrawlID int64,Acknowledge bool}`; `Service.Start(ctx context.Context,input Request) (Report,error)` creates a run and starts service-owned background work; `Service.Cancel(id int64) error` cancels it. The service reads `store.GetCrawlRun`, uses `repeater.Sender` and Scope `Allows(string) bool`, and writes through `ActiveCheckStore`.

- [ ] Use controlled HTTP fixtures to assert same-origin/scope and GET-only requests, skipped hidden/password/file fields, one marker per input, bounded request count and pacing, no redirects, cancellation, and stop-on-storage-error.
- [ ] Run `go test ./internal/activechecks -run TestService`; confirm red.
- [ ] Implement candidate selection and bounded sequential sends, current-scope checks, synthetic marker generation, classifier invocation, and per-result persistence. Finish with completed/cancelled/scope_revoked/failed states.
- [ ] Run `go test -race ./internal/activechecks ./internal/store`; commit service and tests.

### Task 5: API, UI, and documentation

**Files:** Create `internal/api/active_checks.go`, `internal/api/active_checks_test.go`, `web/src/components/ActiveChecksWorkspace.tsx`, `web/src/components/ActiveChecksWorkspace.test.tsx`; modify `internal/api/server.go`, `web/src/components/ActiveScannerWorkspace.tsx`, `web/src/styles.css`, `docs/setup/active-scanner.md`, `README.md`.

**Interfaces:** `POST /api/active-checks/runs` returns 202 with run ID; `GET /api/active-checks/runs`, `GET /api/active-checks/runs/{id}`, `POST /api/active-checks/runs/{id}/cancel`, and `DELETE /api/active-checks/runs/{id}`. UI requires confirmation, shows maximum traffic estimate, polls running status, labels observations, and never calls them confirmed XSS.

- [ ] Test API invalid/unavailable/busy/missing-scope/list/detail/cancel/delete cases and UI confirmation, progress, cancel, reopen, delete, observation wording.
- [ ] Run `go test ./internal/api -run ActiveChecks` and `npm test -- --run src/components/ActiveChecksWorkspace.test.tsx` in `web`; confirm red.
- [ ] Implement endpoints using existing JSON/request guards and scanner-area UI. Document authorization, limits, privacy, and false-positive caveats.
- [ ] Run `go test -race ./...`, `go vet ./...`, `npm test`, `npm run build`, and `git diff --check`. Commit, push the existing PR branch, attach the PR, and verify backend/frontend/Windows CI.
