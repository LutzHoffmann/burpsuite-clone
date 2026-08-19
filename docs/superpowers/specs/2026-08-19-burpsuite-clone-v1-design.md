# BurpSuite-Clone v1 Design

## Status

Approved design direction from the user:

- Product: BurpSuite-like local security testing tool.
- First release: proxy-first.
- Form factor: OS-independent web app with a local backend.
- HTTPS interception: included in v1.
- Core stack: Go for proxy/API, React/TypeScript for UI, SQLite for local project storage.
- Repository quality: maintained GitHub-ready repository is part of v1.

## Product Goal

Build a local web application for authorized web application security testing. The first version focuses on a reliable manual proxy workflow: capture traffic, inspect requests and responses, pause selected traffic, edit requests, replay requests, and persist work in local projects.

The tool must run locally by default. Traffic, certificates, project databases, request bodies, and notes stay on the user's machine. The browser UI provides an OS-independent operator experience, while the local Go backend handles network traffic and storage.

## Non-Goals For v1

The following features are intentionally excluded from v1:

- Crawler.
- Intruder/fuzzer.
- Active vulnerability scanner.
- Plugin or extension marketplace.
- Team collaboration.
- Cloud synchronization.
- Automated reporting.
- Full WebSocket message editing.
- HTTP/2 interception beyond safe pass-through or downgrade strategy chosen during implementation.

The architecture should leave room for these features, but v1 must not depend on them.

## Safety And Use Boundary

The application is for authorized testing only. It must make this boundary clear in documentation and onboarding.

Default safety behavior:

- Bind local services to `127.0.0.1` by default.
- Do not expose the API or proxy externally unless the user explicitly changes the bind address.
- Generate and store the local CA under the local project/application data path.
- Show CA trust status and HTTPS interception status clearly in the UI.
- Never upload captured traffic, certificates, project data, or telemetry to external services.
- Provide `SECURITY.md` with scope, responsible disclosure guidance, and handling rules for sensitive captured data.

## Architecture

The system has three main parts.

### core-proxy

The Go proxy is responsible for:

- Listening for HTTP proxy traffic.
- Handling plain HTTP requests.
- Handling HTTPS `CONNECT` tunnels.
- Performing HTTPS MITM when enabled and allowed by rules.
- Generating per-host leaf certificates signed by the local CA.
- Capturing request and response metadata.
- Capturing request and response bodies with size limits.
- Applying intercept rules.
- Pausing traffic that requires user action.
- Forwarding, dropping, or replaying traffic.

The proxy pipeline should be explicit and testable:

1. Accept connection.
2. Parse proxy request or `CONNECT`.
3. Decide whether interception applies.
4. Capture request metadata and body according to limits.
5. Pause for intercept if a rule matches.
6. Forward request upstream.
7. Capture response metadata and body according to limits.
8. Store completed exchange.
9. Notify the UI through WebSocket events.

### api-server

The Go API server is responsible for:

- Serving the React UI in production builds.
- Exposing REST endpoints for stored data and settings.
- Exposing WebSocket events for live traffic updates and intercept queues.
- Managing projects.
- Managing proxy lifecycle and status.
- Providing CA certificate download.
- Handling Repeater send operations.
- Mediating UI edits to paused traffic.

The API should be local-only by default. It should validate all incoming requests even though it normally binds to localhost.

### web-ui

The React/TypeScript UI is responsible for:

- Showing proxy status, CA status, active project, and intercept state.
- Displaying live proxy history.
- Providing request and response inspectors.
- Providing an intercept queue with edit, forward, and drop actions.
- Providing a Repeater view for manual request replay.
- Providing project and settings views.

The UI should feel like a dense operator tool, not a marketing page. The first screen should be the working application surface: navigation, history table, inspector, and status controls.

## v1 Functional Scope

### Proxy

v1 includes:

- Configurable proxy listen host and port.
- Default bind address `127.0.0.1`.
- HTTP proxying.
- HTTPS interception with local CA.
- CONNECT pass-through for hosts excluded by rules.
- Per-host dynamic certificate generation.
- Certificate cache.
- Basic upstream timeout and error handling.
- Capture size limits for request and response bodies.

### Certificate Management

v1 includes:

- Local CA generation on first run.
- CA certificate download from UI.
- Display of CA fingerprint.
- Display of CA file path.
- Regenerate CA action with explicit warning.
- Documentation for trusting the CA on macOS, Windows, Linux, Firefox, Chrome, and common test devices.

Private CA keys must never be committed, exported automatically, or placed in project files intended for sharing.

### History

The proxy history table includes:

- Method.
- Scheme.
- Host.
- Path.
- Query indicator.
- Status code.
- MIME type.
- Request size.
- Response size.
- Duration.
- Timestamp.
- Intercepted flag.
- Error flag.

History supports:

- Text search.
- Filtering by method, host, status family, MIME type, and error state.
- Selecting an entry to inspect request and response details.
- Sending a request to Repeater.
- Tagging and notes.

### Intercept

Intercept mode includes:

- Global intercept on/off toggle.
- Rules for method, host substring, path substring, and MIME type.
- Paused request queue.
- Request editor for paused traffic.
- Forward action.
- Drop action.
- Continue without editing action.
- Response display after completion.

Response interception is not required in v1. The design should not prevent adding it later.

### Inspector

The inspector includes tabs for:

- Headers.
- Body.
- Raw.
- Cookies.
- Query.
- Timing.

Body display supports:

- Plain text.
- JSON pretty print.
- Form data.
- Hex or safe binary preview for unknown binary data.

The UI must not corrupt binary bodies. Editing is initially limited to text-safe request bodies.

### Repeater

Repeater includes:

- Create tab from history entry.
- Edit method, URL, headers, and body.
- Send request directly through the backend.
- Display response status, headers, body, duration, and size.
- Keep per-tab send history.
- Compare two responses at a basic metadata/body text level.

Repeater requests are stored with the active project.

### Project Storage

SQLite stores:

- Projects.
- Settings.
- History metadata.
- Request metadata.
- Response metadata.
- Headers.
- Cookies.
- Tags.
- Notes.
- Repeater sessions.
- Repeater send history.

Large request and response bodies are stored as files in the project directory. SQLite stores paths, hashes, sizes, truncation flags, and MIME metadata.

The storage layer must be behind a Go interface so future backends or export formats can be added without rewriting proxy logic.

## Repository And GitHub Maintenance

The repository should be GitHub-ready from v1.

Required repository files:

- `README.md` with purpose, scope, setup, run commands, screenshots placeholder, and safety notice.
- `LICENSE`.
- `SECURITY.md` with supported versions, disclosure process, and sensitive-data warning.
- `CONTRIBUTING.md` with local setup, tests, style, and PR process.
- `.gitignore` for Go, Node, local databases, certificates, build output, and project data.
- Issue templates for bug reports and feature requests.
- Pull request template.
- GitHub Actions workflow for Go tests, TypeScript checks, linting, and build.

Recommended repository layout:

```text
cmd/
  proxy/
internal/
  api/
  certs/
  config/
  intercept/
  proxy/
  repeater/
  store/
web/
  src/
docs/
  architecture/
  setup/
  security/
examples/
  targets/
.github/
  workflows/
```

Release readiness:

- The build should support local development commands first.
- Cross-platform release binaries for macOS, Linux, and Windows can be added after the first stable local build.
- CI must prevent obvious regressions before a pull request is merged.

## API Design

Initial REST API surface:

- `GET /api/status`
- `GET /api/settings`
- `PUT /api/settings`
- `GET /api/ca.pem`
- `POST /api/ca/regenerate`
- `GET /api/history`
- `GET /api/history/{id}`
- `PATCH /api/history/{id}`
- `POST /api/history/{id}/send-to-repeater`
- `GET /api/intercept/queue`
- `POST /api/intercept/{id}/forward`
- `POST /api/intercept/{id}/drop`
- `PUT /api/intercept/{id}/request`
- `GET /api/repeater/sessions`
- `POST /api/repeater/sessions`
- `POST /api/repeater/sessions/{id}/send`

Initial WebSocket event types:

- `proxy.status.changed`
- `history.entry.created`
- `history.entry.updated`
- `intercept.item.queued`
- `intercept.item.completed`
- `repeater.send.completed`
- `settings.changed`

API payloads should use explicit DTOs rather than exposing database structs directly.

## Data Flow

Normal captured request:

1. Browser is configured to use the local proxy.
2. Proxy receives HTTP request or HTTPS `CONNECT`.
3. HTTPS traffic is intercepted if enabled and allowed by rules.
4. Proxy records request metadata and body according to limits.
5. Proxy forwards the request upstream.
6. Proxy records response metadata and body according to limits.
7. Store persists metadata and body references.
8. API broadcasts live update to the UI.
9. UI shows the new history row and inspector data.

Intercepted request:

1. Proxy receives a request matching intercept rules.
2. Proxy stores a pending intercept item.
3. API broadcasts queue update.
4. UI opens or updates the intercept queue.
5. User edits, forwards, or drops the request.
6. Proxy resumes or terminates the upstream flow.
7. Final history entry is stored and broadcast.

Repeater request:

1. UI sends edited request data to API.
2. Repeater service builds an outbound request.
3. Backend sends it without relying on browser CORS behavior.
4. Response is captured and stored under the repeater session.
5. API returns response and broadcasts completion.

## Error Handling

The proxy should capture errors as first-class history entries where possible.

Important error cases:

- Upstream DNS failure.
- Upstream TLS failure.
- Client disconnect.
- Body too large.
- Unsupported content encoding.
- Certificate generation failure.
- Storage write failure.
- Intercept timeout.

Errors should include a safe operator-facing message and a lower-level diagnostic string in local logs. Sensitive body data must not be written to logs by default.

## Testing Strategy

Go tests:

- Certificate generation and CA persistence.
- Host certificate cache behavior.
- HTTP proxy forwarding.
- HTTPS MITM handshake against local test server.
- Intercept rule matching.
- Forward/drop behavior.
- Storage migrations and repository methods.
- Repeater request sending.

Frontend tests:

- History table rendering and filtering.
- Inspector tab rendering.
- Intercept queue actions.
- Repeater edit/send flow using mocked API.
- Settings form validation.

Integration tests:

- Local HTTP target server.
- Local HTTPS target server with generated test certificate.
- Proxy capture of request and response.
- HTTPS MITM capture with test CA.
- Repeater send through backend.

CI should run:

- Go unit tests.
- Go vet or equivalent static checks.
- TypeScript typecheck.
- Frontend tests.
- Production build.

## Open Implementation Decisions

The implementation plan should decide:

- Exact Go HTTP router.
- Exact frontend build tool.
- Exact SQLite driver and migration tool.
- Whether the API and proxy run in one process for v1 or separate commands.
- Body size defaults.
- Intercept timeout default.
- License choice.

Recommended defaults unless changed during implementation:

- One Go process for v1, with API server and proxy listener inside the same binary.
- Vite for React/TypeScript.
- SQLite with explicit migrations.
- MIT or Apache-2.0 license.
- Conservative body capture limits with visible truncation flags.

## Acceptance Criteria

v1 is complete when:

- A user can start the local app with documented commands.
- The UI opens in a browser and shows proxy status.
- The user can download the local CA certificate.
- The proxy can capture plain HTTP traffic.
- The proxy can intercept HTTPS traffic after the CA is trusted.
- Captured traffic appears live in history.
- The user can inspect headers, body, raw data, cookies, query data, and timing.
- The user can enable intercept mode and forward or drop matching requests.
- The user can send a captured request to Repeater, edit it, resend it, and inspect the response.
- Project data persists locally across restarts.
- The repository contains baseline GitHub maintenance files and CI.
- Tests cover proxy, certificate, storage, API, and UI critical paths.

