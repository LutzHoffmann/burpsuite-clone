# Target, Scope, and Site Map v2a Design

## Status

Approved design direction:

- Continue toward a manual web security testing suite before building an automated scanner.
- Build the target foundation before response interception, WebSocket tooling, or an Intruder-style fuzzer.
- Forward and retain out-of-scope traffic in proxy history, but exclude it from the Site Map and scope-aware tools.
- Keep the proxy and history usable while target projections are rebuilt or unavailable.

## Goal

Add a project-scoped target model that turns captured traffic into a navigable Site Map and reusable parameter inventory. The target model must provide the common scope decision required by later manual testing tools without changing the local-first architecture.

The milestone is complete when an operator can define target scope, browse discovered in-scope endpoints, inspect the requests behind an endpoint, and see which parameters were observed in query strings, request bodies, and cookies.

## Non-Goals

v2a does not include:

- Automated crawling or directory enumeration.
- JavaScript execution, SPA rendering, or DOM analysis.
- Response interception.
- Match-and-replace rules.
- WebSocket message capture or editing.
- Intruder-style fuzzing or brute forcing.
- Active or passive vulnerability findings.
- Automatic path-template inference such as converting `/users/42` into `/users/{id}`.
- Arbitrary regular expressions in scope rules.

These features may consume the target model later, but they must not be coupled to the v2a implementation.

## Product Behavior

### Scope

Scope rules belong to the active local project. A rule contains:

- Enabled state.
- Action: include or exclude.
- Optional scheme: `http` or `https`.
- Host pattern: exact host or a leading wildcard such as `*.example.test`.
- Optional port.
- Optional path prefix.

Exclusion rules take precedence over inclusion rules. If no enabled include rule matches, the request is out of scope. A new project therefore starts with an empty scope and the UI offers an explicit "Add to scope" action from History and Target views.

Scope matching uses normalized URL components rather than a raw URL string:

- Schemes and DNS host names are compared case-insensitively.
- Internationalized host names are compared in normalized ASCII form.
- Default ports are normalized to `80` for HTTP and `443` for HTTPS.
- Path prefixes match on segment boundaries so `/api` does not match `/apiv2`.
- URL fragments are irrelevant because they are not sent through HTTP.

All proxy traffic is still forwarded and stored in History. Each completed exchange records the scope decision and scope rule version used at capture time. Out-of-scope traffic:

- Remains searchable and inspectable in History.
- Is not added to the Site Map.
- Bypasses request interception.
- Is unavailable to later scope-aware attack tools unless the operator explicitly overrides that tool's safety control.

### Site Map

The Site Map is a derived projection of in-scope History. It groups traffic by:

1. Scheme, host, and effective port.
2. Exact path segments.
3. HTTP method.

An endpoint identity consists of scheme, normalized host, effective port, exact path, and method. Query values do not create separate endpoints. Different methods on the same path remain separate endpoint records.

Each endpoint tracks:

- First and last observed timestamps.
- Observation count.
- Observed response status codes.
- Observed request and response MIME types.
- Latest representative History exchange.
- Error and authentication-related status indicators derived from metadata only.

The Site Map does not copy request or response body values. It stores metadata and references to History exchanges so sensitive data has one source of truth.

### Parameter Inventory

Parameter extraction operates only on fully captured, text-safe request data. It records parameter names and locations, not values.

Supported locations in v2a:

- Query parameters, including repeated names.
- `application/x-www-form-urlencoded` fields.
- JSON object fields at any depth, represented with a stable path such as `user.email` or `items[].id`.
- Cookie names.
- Non-file multipart form field names when the multipart structure is complete and within parser limits.

Each parameter record contains endpoint identity, location, normalized name or JSON path, first and last observed timestamps, observation count, and a coarse observed type such as string, number, boolean, null, object, array, or unknown.

Truncated, malformed, compressed, unsupported, or binary bodies still produce endpoint observations but no body parameter records. Parsing failures are diagnostic metadata, not proxy failures.

## Architecture

v2a adds two focused backend packages and extends the existing storage, API, proxy, event, and web layers.

### `internal/scope`

The scope package owns:

- Rule validation and normalization.
- Immutable compiled rule sets.
- URL classification.
- The matching explanation used by the UI, including the winning rule or the absence of an include match.

The package has no dependency on HTTP handlers, SQLite, or UI DTOs. Proxy, target projection, and future tools call the same classifier so scope decisions cannot drift between components.

### `internal/target`

The target package owns:

- Endpoint identity normalization.
- Parameter extraction.
- Incremental projection of completed exchanges.
- Generation-based Site Map rebuilds after scope changes.
- Rebuild status and progress reporting.

The projector consumes store-level exchange data and a compiled scope rule set. It does not call proxy handlers or depend on React-facing types.

### Proxy Integration

Before request interception, the proxy classifies the request with the current compiled scope rules. Out-of-scope requests bypass interception but otherwise follow the existing forwarding and capture path.

When an exchange is completed, dropped, or fails, the stored History record includes:

- `in_scope`.
- `scope_version`.
- The matched rule identifier when one exists.

After the History write succeeds, an in-scope exchange is submitted to the target projector. Projection failure must not change the client response or discard History.

### Rebuild Model

Scope updates validate, persist, and atomically activate a new compiled rule set before incrementing the project scope version and starting one serialized background rebuild. Repeated edits cancel or supersede older pending rebuilds, so only the newest target generation can replace the active Site Map.

Rebuilds use a staging generation:

1. Persist and compile the new rules.
2. Create a pending target generation.
3. Stream existing History through the classifier and projector in bounded batches.
4. Merge exchanges completed while the rebuild is running.
5. Atomically activate the pending generation after it catches up.
6. Remove obsolete generations after activation.

The previous Site Map remains readable until the replacement generation is complete. If rebuilding fails, the previous generation remains active, the new rules remain visible with a failed rebuild state, and the operator can retry. Proxy forwarding and History capture continue throughout.

Only one rebuild runs per project. Starting a newer rebuild cancels the obsolete pending generation safely.

## Persistence

SQLite migrations add project-owned tables for:

- Scope rules and scope version metadata.
- Target generations and rebuild status.
- Target endpoints.
- Endpoint-to-History references.
- Target parameters.

History exchanges gain scope classification fields. Existing rows are migrated without inventing a scope decision; they are classified during the first rebuild.

Target tables are disposable derived data. They can be rebuilt from History and current scope rules. Foreign keys prevent target records from referencing missing projects or exchanges. Uniqueness constraints enforce endpoint and parameter deduplication within a generation.

## API

The local API adds explicit DTO-based endpoints:

- `GET /api/scope/rules`
- `PUT /api/scope/rules`
- `GET /api/target/tree`
- `GET /api/target/endpoints/{id}`
- `GET /api/target/endpoints/{id}/requests`
- `GET /api/target/endpoints/{id}/parameters`
- `GET /api/target/rebuild`
- `POST /api/target/rebuild`

History responses expose scope classification, and History actions can add an exchange's origin to scope through the same validated scope-rule API.

State-changing routes retain the existing Host, Origin, JSON content-type, body-size, and unknown-field protections. Scope rule validation rejects unsupported schemes, malformed hosts, invalid ports, ambiguous wildcard placement, and invalid path prefixes.

WebSocket events add:

- `scope.changed`
- `target.endpoint.updated`
- `target.rebuild.started`
- `target.rebuild.progress`
- `target.rebuild.completed`
- `target.rebuild.failed`

Progress events are rate-limited so rebuilding a large project cannot flood connected UI clients.

## Web UI

The primary navigation adds a Target workspace with three coordinated areas:

- Site Map tree for scheme, host, path, and method.
- Endpoint details for observations, requests, status codes, MIME types, and parameters.
- Scope management for adding, editing, enabling, disabling, and removing rules.

The Target workspace supports text filtering, in-scope status, method, status family, and MIME type. Selecting a representative request opens the existing Inspector, and an endpoint request can be sent to Repeater through the existing flow.

History adds an in-scope indicator and an "Add origin to scope" action. Out-of-scope rows remain visible by default and can be filtered.

During a rebuild, the UI continues showing the last complete Site Map with progress and rule-version status. A failed rebuild shows a retry action and diagnostic summary without presenting the stale map as current.

## Error Handling and Limits

- Invalid scope rules are rejected atomically; a partial rule set is never activated.
- Parameter parsing uses explicit depth, field-count, and byte limits.
- Parser panics are prevented through bounded iterative traversal and defensive decoding.
- Malformed inputs produce endpoint metadata plus a parse diagnostic, not a failed exchange.
- Projection write failures are logged locally without request or response bodies.
- Rebuild cancellation and application shutdown leave no generation marked complete unless activation succeeded.
- The active projection remains readable after a rebuild failure.

## Security and Safety

- Empty scope means no targets are in scope.
- Exclusions override inclusions.
- Out-of-scope requests bypass interception.
- No future active tool may run without an explicit in-scope decision or a deliberate per-run override.
- Parameter values are not duplicated into target tables or emitted in target update events.
- Scope changes and rebuild actions remain inside the local API trust boundary and are protected by the existing Host, Origin, and request validation.
- Target and scope data remain in the local project store and are never transmitted externally.

## Testing

### Scope tests

- Include and exclude precedence.
- Exact host and leading wildcard matching.
- Scheme, default port, explicit port, and path-boundary normalization.
- Empty scope behavior.
- Invalid rule rejection.
- Matching explanations and rule identifiers.

### Target tests

- Endpoint identity and method separation.
- Endpoint deduplication and observation aggregation.
- Query, form, nested JSON, cookie, and multipart field extraction.
- Repeated fields and stable JSON paths.
- Truncated, malformed, oversized, and binary body behavior.
- Concurrent incremental updates during a rebuild.
- Successful generation activation, failed rebuild rollback, retry, and cancellation.

### Store and API tests

- Migration from the v1 schema.
- Project isolation and foreign keys.
- Scope rule CRUD and atomic validation.
- Site Map tree, endpoint detail, request references, and parameter DTOs.
- Existing local API Host, Origin, and body validation on new routes.

### Frontend tests

- Tree rendering, filtering, and selection.
- Scope rule editing and validation feedback.
- History scope indicators and add-to-scope action.
- Live endpoint updates.
- Rebuild progress, completion, failure, and retry states.

### Integration tests

- In-scope HTTP and HTTPS traffic appears in History and the Site Map.
- Out-of-scope traffic is forwarded and stored in History but excluded from the Site Map and interception.
- Adding a previously observed host to scope rebuilds the Site Map from old History.
- Removing a host from scope replaces the active projection without interrupting proxy traffic.

## Acceptance Criteria

v2a is complete when:

- Operators can create include and exclude scope rules from the Target and History views.
- Scope classification is consistent across HTTP, HTTPS, History, interception, and target projection.
- Out-of-scope traffic remains forwarded and inspectable but does not enter the Site Map or interception queue.
- In-scope endpoints appear live in a navigable Site Map.
- Endpoint details show representative requests, observations, and parameter inventory without duplicating parameter values.
- Scope changes rebuild existing History into a new target generation without blocking the proxy.
- A failed rebuild preserves the last complete Site Map and can be retried.
- Backend, frontend, migration, integration, race, and cross-platform checks pass in CI.

## Follow-Up Milestones

After v2a, manual-suite development proceeds as separate specifications:

1. v2b: response interception and request/response match-and-replace.
2. v2c: WebSocket connection and message history, replay, and editing.
3. v2d: Intruder-style bounded fuzzer with payload positions, rate limits, result analysis, and explicit scope enforcement.
4. v2e: Decoder, Comparer, and token randomness analysis.

An integrated browser, crawler, passive scanner, active scanner, OAST service, and extension platform remain later product phases.
