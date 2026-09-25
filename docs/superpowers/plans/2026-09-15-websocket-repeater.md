# WebSocket Repeater Implementation Plan

> Execute in the existing isolated worktree using test-driven development and focused subagent work.

**Goal:** One-shot, scoped WebSocket sends on independent connections.
**Architecture:** Dedicated bounded service; strict API draft/send endpoints; an editor
inside the existing WebSockets workspace. No persistence or changes to passive relay.
**Tech Stack:** Go, gorilla/websocket, React/TypeScript, SQLite read-only draft loading.
**Approved spec:** ../specs/2026-09-15-websocket-repeater-design.md

## Shared Contracts

New package `internal/wsrepeater`:
`NewService(manager *scope.Manager, bodyLimit int64) *Service`;
`(*Service).Send(ctx context.Context, request Request) (Result, error)`.
`Request`: URL string, Type string (text/binary), Payload string, PayloadFormat
string (text/hex), Headers map[string][]string, Subprotocols []string. Lower-camel JSON.
Origin/auth/cookies are entered in Headers; generated and hop-by-hop headers denied.
`Result`: Sent bool, Outcome string, Messages []Message, DurationMS int64,
Subprotocol string. `Message`: Type, Payload, PayloadFormat string, Size int64,
Truncated bool. Outcomes: window_complete, peer_closed, message_limit, size_limit,
timeout, canceled, read_error, write_error, handshake_error, tls_error.
Errors returned before sending: ErrInvalid, ErrOutOfScope, ErrBusy. Errors after
network work return bounded Result status with no raw credential-bearing error text.
Expose `Validate(Request, limit int64) error` for API source-draft checks and tests.

GET `/api/websockets/{id}/messages/{messageId}/draft` returns Request. Check source
belongs to connection, complete/untruncated/identity client-to-server data and payload
limit; no credentials copied. POST `/api/websocket-repeater/send` accepts Request.
Use 3 MiB JSON cap, no unknown fields/trailing JSON/null, UTF-8 input validation.
API Config adds WSRepeater *wsrepeater.Service; main wires current shared scope manager.

## Tasks

- [x] Service: create `internal/wsrepeater/service.go`, handshake.go and tests.
  Write rejection/echo tests first; run `go test ./internal/wsrepeater` red then green.
  Cover ws/wss, invalid URLs/headers/payloads, scope-before-dial, deadline/cancel,
  oversized streams, 20-message/1 MiB caps, four sends, no redirects/proxies/retries.
- [x] API: create `internal/api/ws_repeater.go` and tests; modify server.go and
  cmd/proxy/main.go. Test endpoints first: source eligibility, controls, JSON limits,
  missing/foreign IDs, unavailable service, and out-of-scope denial.
- [x] UI: add `web/src/components/WSRepeater.tsx` and tests; modify WebSocketsWorkspace,
  App, client, types, styles. First test editable draft/send/cancel/late responses.
  Protect unsent changes on source transfer/navigation and cancel on unmount.
- [x] Integration: test actual API send against local ws and service-level wss, strict scope and no
  capture/credential persistence. Review spec compliance and code quality.
- [x] Documentation: update docs/setup/websockets.md and README with one-shot limits.
- [x] Local verification: full Go race suite, go vet, Windows build, 108 frontend
  tests and production build, diff check. Desktop browser smoke test verified the
  editor and scope denial. No mobile visual smoke test performed.
- [ ] Commit and push existing branch; update existing draft PR, watch CI; no merge.

## Execution Notes

User approved the written one-shot spec. Continue through implementation without
additional scope-approval prompts. Keep tool output and progress reports concise.

Final review found two bound gaps, both fixed and re-reviewed: subprotocols now
share the 16 KiB operator-header budget, and upstream response headers are capped
at 64 KiB before HTTP parsing, including decrypted WSS. Exact-boundary, oversized,
unterminated, cancellation and TLS tests pass. Reviewer approved the final fix.
An existing quota integration test now waits for async connection finalization
before deleting SQLite; it failed twice in 10 runs before the fix and passed
10 race-enabled runs afterward. Full Go race suite passed after integration.

First GitHub run passed backend/Windows but exposed an existing HTTP response
editor race: the mount effect could reset the first user edit. Removed the
redundant reset (editors already remount by queue ID). Four focused tests cover
early status/body/header edits and same-ID refresh/new-ID initialization; three
fail with the old code and pass after the fix. Independent review approved it.
