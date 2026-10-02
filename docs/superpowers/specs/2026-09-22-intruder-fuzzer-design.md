# Intruder/Fuzzer Design

## Status

Approved design direction:

- Extend the local-first manual testing suite with a bounded Intruder-style fuzzer.
- Require an explicit current scope match for every generated request.
- Support Sniper, Battering Ram, Pitchfork, and Cluster Bomb attack types.
- Persist jobs and results locally so interrupted runs remain inspectable and resumable.
- Keep the proxy, interception, History, Repeater, and Target workflows available while jobs run.

## Goal

Add a project-scoped HTTP request fuzzer that turns a request from History or Repeater into a bounded attack job. An operator can mark payload positions, configure one or more payload lists, preview the exact request count, run at a controlled rate, inspect response differences, pause and resume work, and abort immediately.

The milestone is complete when all four attack types produce deterministic requests, every send is re-authorized against current Target Scope, job state survives application restart, and the UI can create, control, filter, and inspect jobs without exposing unbounded execution.

## Non-Goals

- Automatic vulnerability claims or active scan checks.
- Credential stuffing presets, denial-of-service modes, or distributed workers.
- Recursive response-derived payload generation.
- WebSocket fuzzing.
- JavaScript execution or browser automation.
- External word-list downloads.
- Arbitrary user code or plugin payload processors.
- Infinite jobs or an option that disables all request limits.

## Operator Workflow

1. Send an HTTP request from History or Repeater to Intruder.
2. Confirm that the normalized destination is currently in scope.
3. Mark one or more non-overlapping payload positions in the editable raw request.
4. Select an attack type and assign bounded payload lists.
5. Review the generated-request count, concurrency, rate, timeout, and stop conditions.
6. Start the job and inspect live progress and results.
7. Pause, resume, or abort the job. Open any result in the existing Inspector or send it to Repeater.

Creating or editing a job never sends traffic. Starting or resuming requires an explicit operator action.

## Request Template and Positions

The job stores a request template containing method, absolute HTTP or HTTPS URL, ordered headers, and body bytes. A draft template may be replaced atomically; it becomes immutable after the first start. A source History identifier may be retained for navigation but is not required after job creation.

Payload positions are byte ranges over a canonical request representation. Each position has a stable identifier, start offset, end offset, and payload-set assignment. Positions must be non-empty, ordered, non-overlapping, and within the template. Templates and positions are revalidated before persistence and before execution.

Generated payload bytes replace the selected range exactly. The engine does not silently URL-encode, JSON-escape, or otherwise transform payloads in this milestone. Operators can load text or hexadecimal payloads; text is converted to UTF-8 bytes and hexadecimal input must contain complete byte pairs. Request framing is rebuilt safely after substitution, including `Content-Length`; hop-by-hop proxy headers are not copied.

## Attack Types

### Sniper

Each position is fuzzed independently. For every position, every payload assigned to that position produces one request while all other positions retain their template bytes.

### Battering Ram

One shared payload set is required. Each payload replaces every marked position in one request.

### Pitchfork

Each position has one payload set. Lists advance in parallel and the job length is the shortest assigned list. The preview identifies longer lists whose remaining payloads will not run.

### Cluster Bomb

Each position has one payload set. The engine produces the Cartesian product in stable position and payload order. Configuration is rejected when the product exceeds the job request limit; arithmetic uses overflow-safe bounds.

Unused payload sets may be empty. Every set assigned by the selected attack type must contain at least one payload. Empty payload values and duplicate payloads are valid and are counted exactly as supplied. At least one position and one assigned payload are required.

## Limits and Scheduling

Every job has explicit bounds:

- Maximum generated requests: default 1,000; configurable from 1 through 100,000.
- Concurrency: default 2; configurable from 1 through 20.
- Rate: default 5 requests per second; configurable from 0.1 through 100.
- Per-request timeout: default 30 seconds; configurable from 1 through 120 seconds.
- Maximum request template: 2 MiB.
- Maximum payload: 1 MiB.
- Maximum payload count across all sets: 100,000.
- Maximum retained response body per result: 2 MiB, with explicit truncation metadata.

Rate limiting applies to request starts, uses a monotonic clock, and does not permit startup bursts larger than concurrency. Job workers share one limiter. Pausing prevents new requests from starting but lets in-flight requests finish and records their outcomes. Aborting cancels in-flight requests and transitions to a terminal state after all workers join.

The scheduler executes jobs within the local process. One project may have at most two running jobs and the application may run at most four jobs in total. Additional starts receive a conflict response rather than an implicit queue.

## Scope Enforcement

Target Scope is an execution boundary:

- Job creation requires the destination to be in scope.
- Start and resume re-check the template destination against the current compiled scope version.
- Every generated request is classified immediately before dispatch.
- A scope change that excludes the target stops new sends and pauses the job with reason `scope_revoked`.
- Redirects are not followed automatically.
- Generated requests cannot change scheme, host, or port because positions are limited to the origin-form request target, headers, and body. Editing the absolute destination is a job configuration change that triggers full validation.

There is no per-run out-of-scope override in this milestone.

## Architecture

### `internal/intruder`

This package owns:

- Template and payload validation.
- Deterministic attack iteration without materializing the full Cartesian product.
- Job lifecycle and state transitions.
- Global and per-job scheduling limits.
- Rate limiting, cancellation, pause, resume, and shutdown joins.
- Request dispatch through a narrow sender interface.
- Baseline comparison and result summaries.

It depends on interfaces for scope classification, persistence, event publication, and HTTP sending. It does not depend on API handlers, React DTOs, or SQLite types.

### HTTP Sending

Intruder reuses the Repeater transport policy through a shared internal sender abstraction rather than calling an HTTP API. The sender preserves explicit headers and body bytes, applies the existing SSRF and proxy safeguards, disables redirects, enforces the job timeout, and returns bounded request/response captures plus timing metadata.

Refactoring Repeater transport is limited to extracting this common interface; existing Repeater behavior and tests must remain unchanged.

### Lifecycle

Persisted states are `draft`, `running`, `pausing`, `paused`, `aborting`, `aborted`, `completed`, and `failed`. Running-like states found at startup become `paused` with reason `application_restarted`; they never resume traffic automatically.

State transitions and result insertion are serialized per job. A monotonic sequence number orders results independently of database identifiers. Progress events are coalesced to at most five per second per job.

## Persistence

SQLite migrations add project-owned tables for:

- Intruder jobs and immutable request templates.
- Payload positions.
- Payload sets and ordered payload values.
- Job results.

Job records contain attack type, state, state reason, configuration limits, total request count, next sequence, completed count, error count, timestamps, and the scope version observed at the last start.

Each result contains sequence, position and payload indexes, substitutions used, method, URL, status, response MIME type, request and response sizes, duration, error category, response truncation state, and bounded request/response captures. Payload bytes and captures remain local project data and count toward the existing capture quota. If quota is exhausted, new result bodies are omitted with an explicit storage status; metadata and job progress continue.

Deleting a draft or terminal job cascades to its payloads and results. Running jobs cannot be deleted. Project isolation and foreign keys are mandatory.

## Result Analysis

The first successful response is the default baseline unless the operator chooses a completed result. Each result exposes:

- HTTP status and status difference.
- Response byte length and length delta.
- Duration and duration delta.
- Response MIME type and MIME difference.
- A bounded similarity score over response bodies.
- Network, timeout, cancellation, scope, and storage error categories.

Similarity is advisory, deterministic, and computed from bounded captured bytes. It must not label a response as vulnerable. Filters cover status, errors, MIME type, length range, duration range, similarity range, and payload text search.

## API

The local API adds:

- `POST /api/intruder/jobs`
- `GET /api/intruder/jobs`
- `GET /api/intruder/jobs/{id}`
- `PUT /api/intruder/jobs/{id}` for draft configuration only
- `DELETE /api/intruder/jobs/{id}`
- `POST /api/intruder/jobs/{id}/start`
- `POST /api/intruder/jobs/{id}/pause`
- `POST /api/intruder/jobs/{id}/resume`
- `POST /api/intruder/jobs/{id}/abort`
- `GET /api/intruder/jobs/{id}/results` with cursor pagination and filters
- `GET /api/intruder/jobs/{id}/results/{sequence}`

Creation accepts either an explicit bounded request template or a History exchange identifier. API DTOs use base64 for arbitrary bytes and distinguish absent data from empty data. State-changing routes retain Host, Origin, JSON content-type, body-size, unknown-field, and method protections. Optimistic job revisions reject stale updates with `409 Conflict`.

Events add `intruder.job.updated`, `intruder.progress`, and `intruder.result.created`. Events contain identifiers and bounded metadata only, never headers, bodies, or payload bytes.

## Web UI

Primary navigation adds an Intruder workspace with:

- Job list and lifecycle status.
- Raw request editor with position marking and a positions table.
- Attack type and payload-set configuration.
- Preflight panel showing exact request count and all limits.
- Start confirmation for Cluster Bomb jobs above 10,000 requests.
- Live progress with pause, resume, and abort controls.
- Virtualized, filterable result table and result Inspector.

History and Repeater add `Send to Intruder`. Leaving a dirty draft or an active control operation requires confirmation. Binary templates and payloads remain editable through text/hex modes without lossy conversion.

## Error Handling

- Validation errors are field-specific and never partially persist a configuration.
- Unsupported request schemes, malformed URLs, invalid headers, overlapping positions, arithmetic overflow, and exceeded limits are rejected before start.
- Scope revocation pauses rather than fails a job.
- Individual network and HTTP errors create result records and do not fail the job.
- Persistence failure pauses the job and stops new sends; it never continues unrecorded traffic.
- Panics inside a worker are recovered at the job boundary, cancel sibling workers, and fail the job without crashing the proxy.
- Application shutdown aborts network operations, joins workers, and persists resumable paused state.

## Security

- Active traffic requires explicit operator start or resume.
- Every request is checked against current scope immediately before sending.
- Redirects, environment proxy discovery, and implicit credential forwarding are disabled.
- Sensitive headers and bodies are excluded from logs and events.
- Response decompression and capture use explicit size limits.
- Header validation prevents request smuggling through conflicting framing headers.
- Rate and request-count limits cannot be disabled through the API or UI.
- UI warnings state that scope is not authorization and active testing requires permission.

## Testing

### Generator Tests

- Exact request order for all four attack types.
- Empty, duplicate, text, hexadecimal, and binary payloads.
- Multiple positions, UTF-8 boundaries, and body substitutions.
- Overlap, range, count, and multiplication-overflow rejection.
- Stable resume from a persisted sequence without duplicate sends.

### Service Tests

- Draft validation and lifecycle transition matrix.
- Per-job and global concurrency bounds.
- Rate limits without startup bursts.
- Pause, resume, abort, timeout, and shutdown behavior under race detection.
- Scope removal between generated requests and during resume.
- Persistence failure and capture-quota exhaustion.
- Startup recovery of every non-terminal state.

### Store and API Tests

- Migration, project isolation, foreign keys, and cascading deletion.
- Atomic configuration writes and optimistic revision conflicts.
- Cursor pagination and every result filter.
- Existing API security middleware on every new state-changing route.
- Bounded DTOs and absence of sensitive payloads in events.

### Frontend Tests

- Position creation, editing, overlap prevention, and text/hex round trips.
- Attack configuration and exact preflight counts.
- Lifecycle controls and stale-response protection.
- Live progress, filtering, pagination, and result inspection.
- Dirty-draft and in-progress navigation guards.
- Scope revocation and storage-limit messaging.

### Integration and CI

- End-to-end jobs against authorized HTTP and HTTPS fixtures for all attack types.
- Out-of-scope and scope-revoked jobs send no unauthorized requests.
- Paused jobs recover after application restart without automatic traffic.
- Existing proxy, interception, Repeater, Target, WebSocket, and storage tests remain green.
- Go race tests, `go vet`, frontend tests/build, and Windows compilation pass.

## Acceptance Criteria

- Operators can create a job from History or Repeater and mark payload positions without byte corruption.
- Sniper, Battering Ram, Pitchfork, and Cluster Bomb produce the documented deterministic request sequence.
- The UI previews the exact bounded request count before any traffic is sent.
- No generated request starts without a current in-scope classification.
- Concurrency, rate, timeout, capture, and total-request bounds are enforced server-side.
- Jobs pause, resume, abort, survive restart, and never resume traffic automatically.
- Results are persisted, pageable, filterable, comparable to a baseline, and openable in Inspector or Repeater.
- Failures and quota limits are visible without silently losing progress or continuing unrecorded traffic.
- Backend, frontend, integration, race, cross-platform, and production-build checks pass in CI.
