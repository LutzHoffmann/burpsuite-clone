# WebSocket Sessions Implementation

Approved specification: ../specs/2026-09-16-websocket-sessions-design.md.

## Tasks

1. Extract the bounded handshake and implement a session manager with independent
   reader, nonqueued writes, bounded history, scope checks, expiry and cleanup.
   Add failing focused tests first, preserve one-shot tests, then run race tests.
2. Add strict same-origin, no-store session API routes and main lifecycle wiring.
   Test malformed input, origin protections, cursor bounds and response budgets.
3. Add session mode while preserving the default one-shot editor and source guards.
   Test connect/send/poll/close, stale responses, departure and bounded UI history.
4. Review the integrated change, run Go race/vet, Windows compilation and frontend
   tests/build, then update the existing draft PR without merging it.

## Shared Contract

Manager: NewSessionManager(scopeManager, bodyLimit), Close(),
Connect(ctx, ConnectRequest) (SessionSnapshot, error),
Send(ctx, id, SessionSendRequest) (SessionSnapshot, error),
Poll(id, afterSequence) (SessionSnapshot, error),
CloseSession(id) (SessionSnapshot, error), Dispose(id) error.

ConnectRequest: url, headers, subprotocols. SessionSendRequest: type, payload,
payloadFormat. Snapshot: id, url, state, reason, subprotocol, messages,
oldestSequence, latestSequence, nextSequence, droppedMessages.
Messages extend existing Message with sequence, direction, timestamp and complete.
Directions: client-to-server/server-to-client. States: connected/closing/closed.
Errors: ErrInvalid, ErrOutOfScope, ErrBusy (capacity), ErrSessionNotFound,
ErrSessionConflict. Network failures are bounded closed snapshots, not raw errors.
Session IDs are 32 lowercase hex characters; timestamps are RFC3339 strings.

## Progress

- Session manager, strict API, lifecycle wiring and React session mode implemented.
- Review findings for terminal races and unconfirmed final collection addressed.
- Go race tests, vet, Windows build, 123 frontend tests and production build pass.
- Browser smoke verified two sends on one connection, final receive/close, departure
  confirmation, desktop layout and a 390 px layout without horizontal overflow.
