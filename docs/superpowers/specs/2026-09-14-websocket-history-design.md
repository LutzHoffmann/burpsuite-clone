# WebSocket Forwarding and History

## Scope

First stage: forward ws:// and wss:// connections and display captured messages
in both directions. Editing, dropping, replaying and regex replacement of
WebSocket messages are excluded. This is the technical design for the approved
feature scope, pending written-design review before implementation.

## Approach

Use a transparent bidirectional byte relay with a passive frame observer rather
than terminating and recreating two WebSocket protocol sessions. This preserves
masking, fragmentation, subprotocol negotiation, ping/pong and extension bytes.
A terminating bridge would simplify decoded messages but alter negotiation and
control-frame behavior; an opaque tunnel alone would not provide history.

Detect WebSocket HTTP/1.1 upgrade requests before ordinary request preparation.
Preserve required handshake headers and validate a successful upstream upgrade
before sending 101 to the client. Non-101 responses remain ordinary HTTP responses.
Reject unsupported successful upgrades without exposing a half-open connection.
Transport TLS verification remains unchanged; wss uses the existing local CA.

The handshake and WebSocket stream bypass manual HTTP interception/replacement
in this first stage; make this explicit in the UI and documentation. Record
capture-time scope using the existing HTTP/HTTPS equivalent of the URL. As with
HTTP History, passive recording includes out-of-scope traffic and labels it.

## Connection Ownership

After hijacking, preserve buffered client bytes and the upstream duplex body.
Run one relay per direction with bounded read/write buffers and existing idle
timeouts. Either terminal I/O error closes both ends and joins both relays.
Fix the nested HTTPS server lifecycle so StateHijacked does not prematurely
close the outer CONNECT connection while its WebSocket relay remains active.
Do not close a duplex upstream body through ordinary HTTP capture cleanup.

## Observation and Limits

Forward bytes unchanged; parse copies incrementally with bounded memory. Store
text/binary data messages after joining fragments, plus ping/pong/close control
frames. For each record expose direction, sequence, observation time, type,
observed payload length, retained prefix, truncation and completion flags.
The ordering across directions is observation order, not a causal guarantee.

Keep at most the configured body limit per observed message. Large messages
continue forwarding; count remaining bytes without retaining them. Binary and
invalid UTF-8 payloads display as hex, never rendered HTML. Negotiated compressed
or otherwise extended payloads remain opaque in this stage, with an explicit
encoding label; do not misrepresent compressed bytes as decoded text.

Malformed or unsupported framing must not change forwarded bytes. Stop decoding
that direction and mark capture incomplete rather than guessing message boundaries.
An interrupted fragment sequence produces an explicitly incomplete capture.

Use a bounded asynchronous persistence queue: at most 16 MiB retained payload
and 64 pending records across connections. Queue exhaustion skips observation
records, not network bytes; expose a capture-gap indicator and count. Bound
connection observer state to 64 simultaneous observed connections; additional
connections relay transparently without capture and report the limit.

## Persistence and Quota

Add SQLite connection and message tables with bounded scalar metadata and
paginated indexes. Charge retained payload, variable metadata and a 256-byte
allowance per stored connection/message to the existing capture budget, using
the same atomic reservations and pause state. Never automatically delete data.

Quota rejection or storage failure must not stop the relay. A connection whose
initial record could not be saved is not partially attached to history later;
after budget recovery, new connections can be recorded. On an already saved
connection, later messages can resume after budget recovery, with gaps shown.
Counters and terminal state update existing records without creating unbounded
error text. On restart mark previously open stored connections as interrupted.

## API and UI

Add a WebSockets workspace beside existing tools. List stored connections and
their messages in separate stable 100-entry ID-cursor pages. Show URL, scope,
open/closed/interrupted state and capture gaps. Message detail shows bounded
text or hex payload, direction, type, timestamp, size and truncation/encoding.

Read-only routes under /api/websockets provide connection lists, connection
detail, message lists and message detail. Retain local-only API protections.
Bound event notifications and use polling for recovery; do not broadcast payloads.
New traffic must not displace the operator while reading older message pages.
Reuse the application-wide quota warning and current visual language.

## Verification

Test real ws/wss clients through the proxy for both directions, fragmented and
large messages, binary bytes, control frames, negotiated subprotocols, extension
passthrough, non-101 handshakes, malformed frames and abrupt disconnections.
Include a regression for CONNECT ownership after nested hijack.

Verify quota and queue exhaustion preserve forwarded bytes, capture gaps are
visible, reservations roll back on failures, and restart recovery is accurate.
Test API pagination/local protections and UI selection, stale loads and warnings.
Run full Go race tests, vet, frontend tests/build, Windows compilation and review
before updating the existing draft PR. Do not merge or mark it ready.
