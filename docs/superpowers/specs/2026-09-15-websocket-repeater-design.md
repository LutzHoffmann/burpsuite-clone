# WebSocket Repeater: First Stage

## Approved Direction

Provide a manual text/hex editor seeded from passive WebSocket history. Send on
a separate connection to a currently in-scope target. Never inject into a browser
connection or automatically replay opaque/incomplete captured messages.

## Proposed Interaction

This first stage is deliberately one-shot: each Send opens a fresh connection,
sends exactly one data message, collects up to 20 incoming data messages for up
to five seconds after sending, then closes. Incoming messages are labeled as
received messages, not necessarily replies; servers can send unsolicited events.
Opening, sending, and collecting have an overall 15-second deadline. Cancel and
leaving the workspace cancel the request and close its socket.

The editor supports URL, text/binary message type, text/hex payload, optional
Origin, authorization headers, cookies, and subprotocols entered by the operator.
Generated handshake headers cannot be overridden. Compression is disabled for
these new connections. TLS certificate verification remains enabled.

History transfer is explicit and available only for complete, untruncated,
identity-encoded client-to-server text or binary messages. Invalid UTF-8 cannot
be sent as text. The source URL and message are copied, not authentication state:
the current passive connection records do not retain handshake credentials.
Changing selection does not overwrite unsent edits without confirmation.

The UI explains that protocols requiring a login message sequence or a persistent
session are not supported by this one-shot stage. Each received payload is shown
as escaped text or hex with type, byte count, and truncation indicators.

## API and Service Boundaries

Add a dedicated WebSocket repeater service and POST endpoint, separate from the
HTTP repeater and passive relay. Source transfer uses a read-only draft endpoint
which validates source connection ownership and capture metadata server-side.
Sending accepts an explicit operator-authored draft; it never fetches raw capture
bytes by ID as an unchecked replay shortcut.

Check loopback/origin access controls and strict request validation before dialing.
Load current scope at send time, map ws/wss to http/https, and reject empty or
excluded scope. Reject non-WebSocket schemes, userinfo, fragments, invalid headers
and subprotocols. Do not follow redirects or inherit environment proxies.
Scope changes affect subsequent sends; already-started sends remain bounded by
their deadline. Existing hostname-based scope semantics apply.

Limit decoded outgoing payloads to the smaller of the configured body limit and
1 MiB; cap incoming aggregate payload at 1 MiB and each message at the same
outgoing bound. Allow at most four concurrent sends; reject excess work rather
than enqueueing it. Bound headers to 16 KiB and the HTTP JSON envelope to 3 MiB.
Read payloads incrementally; close on oversized frames/messages without buffering
the remainder. Retain a bounded prefix and mark the result as limited.

Distinguish handshake failure, TLS failure, timeout, cancellation, peer close,
collection-window completion, and size limits. A successful write means only
that a message was sent, not that the application accepted it. Never retry a send
automatically. Partial received messages remain visible with explicit status.

## Persistence and Limits

Results live in the repeater UI only for this stage; do not claim they are saved
in passive history or covered by the persistent capture budget. Navigation may
discard the editor/results with a warning for pending edits. Credentials are not
stored in localStorage, project settings, logs, or automatic history records.
Explicit session persistence, live interception, compressed capture decoding,
and multi-message scripts are follow-up work. Browser-socket injection is an
explicit non-goal of this repeater and must not be added as an implicit extension.

## Implementation and Verification

Create focused service/API/editor modules rather than expanding the passive
parser. Reuse existing scope, origin protection, display styles, and error patterns.
Test local ws/wss servers for text/binary transfer, scope denial before network
access, redirect refusal, credentials/header validation, invalid hex/UTF-8,
truncated/opaque source rejection, unsolicited messages, cancellation, silence,
oversized streams, concurrency bounds, TLS verification, and socket cleanup.
UI tests cover editing, source eligibility, stale responses, busy/cancel states,
and safe rendering. Run all Go tests with race detection, frontend tests/build,
go vet, and Windows compilation; update the existing draft PR without merging.
