# WebSocket History Implementation Plan

> **For agentic workers:** Use superpowers:subagent-driven-development. Follow the approved spec and test each deliverable.

**Goal:** Transparent ws/wss forwarding with bounded passive message history.
**Architecture:** A byte relay feeds incremental observers; bounded asynchronous persistence uses the existing quota. Read-only API and UI browse stable pages.
**Tech Stack:** Go, SQLite, React, existing gorilla/websocket for test clients.
**Spec:** docs/superpowers/specs/2026-09-14-websocket-history-design.md

## Constraints

No editing/replay; handshake bypasses HTTP interception. Preserve all forwarded
bytes. 64 observed connections, 64 queued records, 16 MiB queued payload globally.
Per-message capture uses BodyLimitBytes. No deletion; quota rejection does not
stop traffic. Compressed/extended payloads are opaque and labeled. Pages hold 100.

## Contracts

```go
// store/websocket.go; all public fields have lower-camel JSON tags, Payload excluded.
type WSConnection struct {
 ID int64; URL string; InScope bool; OpenedAt time.Time; ClosedAt *time.Time
 State string; Gaps int64; CaptureIncomplete bool
}
type WSMessage struct {
 ID int64; ConnectionID int64; Sequence int64; Direction string; ObservedAt time.Time
 Type string; Size int64; Payload []byte; Truncated bool; Complete bool; Encoding string
}
type WSPageRequest struct { BeforeID, SnapshotID int64 }
type WSConnectionsPage struct { Items []WSConnection; NextBeforeID, SnapshotID int64 }
type WSMessagesPage struct { Items []WSMessage; NextBeforeID, SnapshotID int64 }
type WebSocketStore interface {
 SaveWSConnection(context.Context,*WSConnection) error
 SaveWSMessage(context.Context,*WSMessage) error
 FinishWSConnection(context.Context,int64,string,int64,bool) error
 ListWSConnections(context.Context,WSPageRequest)(WSConnectionsPage,error)
 GetWSConnection(context.Context,int64)(*WSConnection,error)
 ListWSMessages(context.Context,int64,WSPageRequest)(WSMessagesPage,error)
 GetWSMessage(context.Context,int64,int64)(*WSMessage,error)
}
// proxy/ws_observer.go callback gets complete records; no persistence dependencies.
type wsObservedMessage struct {
 Type string; Size int64; Payload []byte; Truncated, Complete bool; Encoding string
}
func newWSObserver(masked bool, extended bool, limit int64, emit func(wsObservedMessage)) *wsObserver
func (*wsObserver) Feed([]byte)
func (*wsObserver) Finish() bool // true if capture incomplete/malformed
```

## Task 1: Store

Own internal/store/websocket.go, websocket_test.go, migrations.go, sqlite.go only.
Migration 7 creates connections/messages and quota charges. Reserve quota in the
same transaction. Finish changes only pre-budgeted fixed metadata. Mark stale open
connections interrupted on startup. List queries omit payload; detail loads bounded
payload. Validate cursors and connection ownership. Add tests for quota rollback,
resume, migration, restart and stable pages. Run `go test -race ./internal/store`.

## Task 2: Incremental Observer

Own internal/proxy/ws_observer.go and ws_observer_test.go only. Parse frame headers,
extended lengths and masking incrementally, unmask retained copies only. Assemble
fragmented data messages with control messages interleaved. Retain <=limit bytes;
count remainder. Malformed framing stops observation, not relay. Label RSV payloads
opaque; don't decompress. Tests feed single bytes, coalesced frames, large payloads,
fragmentation/control/close, malformed data and interrupted messages. No network I/O.

## Task 3: API and UI

Own internal/api/websocket.go/tests, server.go and web/src only. Register GET
/api/websockets, /api/websockets/{id}, /api/websockets/{id}/messages,
/api/websockets/{id}/messages/{messageId}. List responses use items,nextBeforeId,
snapshotId and beforeId/snapshotId queries; zero means first/no-more. Message detail
returns metadata plus payload (text or hex) and payloadFormat, never raw HTML.
Add WebSockets workspace, connection and message pagination, detail, scope/state/
gaps/encoding labels and bypass notice. Poll every5s, guard stale loads and keep older
pages stable; show refresh indication. Test API validation and UI loading/navigation.

## Task 4: Relay and Integration (Coordinator)

Own proxy.go, tunnel.go, new websocket.go/ws_capture.go and integration tests/docs.
Detect handshake before prepareRequest, clone/preserve upgrade headers, RoundTrip
without redirects, validate101 accept/subprotocol and duplex body. Non101 uses
ordinary bounded HTTP capture without repeating the request. Hijack client and
preserve buffered bytes. Prevent outer CONNECT cleanup until relay completion.
Relay each direction; observers never mutate forwarded bytes. Queue writes bounded
globally by bytes and records. Sequence assigned under per-connection lock. Count
skips and finalize after pending records; quota/store errors never break forwarding.
Tests real ws/wss echo, fragments, binary/control, subprotocols, failed upgrades,
disconnect cleanup, quota and HTTP-intercept bypass. Run whole suites, review, then
update existing draft PR and verify CI; no merge.

## Ledger

- Approved scope and written design; execute without additional confirmation loops.
- Store, parser, relay, API, and UI implemented; coordinator integrated worker changes.
- Covered ws/wss echo, compressed and fragmented large messages, binary data,
  subprotocols, rejected upgrades without retry, quota gaps/resume, bounded queues,
  snapshot navigation, stale UI responses, and startup recovery.
- Final local verification: 98 frontend tests, production build, full Go race
  suite, go vet, Windows cross-build, and git diff --check passed. GitHub CI follows.
- Independent worker review was unavailable after worker usage limits; coordinator
  reviewed the integration paths directly. No visual browser smoke test claimed.
