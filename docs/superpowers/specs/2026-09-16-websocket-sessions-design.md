# Manual WebSocket Repeater Sessions

## Approved Direction

Extend the independent WebSocket repeater with longer-lived connections for
multiple manually authored messages and continuous incoming messages. Preserve
the existing one-shot mode. Never inject into a browser connection or introduce
automatic scripts, login sequences, reconnection, or message retries.

## Operator Workflow

Add a Session mode beside One-shot, using the established editor and styles.
Connect explicitly using URL, optional headers and subprotocols. Lock those
connection fields while connected; keep message type, text/hex format and payload
editable. Send exactly one message per click on the same connection. Display
incoming and successfully written outgoing messages in observation order, with
direction, type, timestamp, size and truncation. Incoming messages are not
automatically labeled as replies. Preserve the existing safe history-to-draft
eligibility checks and confirmation before replacing edits.

Expose Connecting, Connected, Closing and Closed states and a Close action.
Show close reason and capture gaps explicitly. Changing workspace or mode asks
before abandoning drafts/results or an active session, then closes it. A failed
send must distinguish not sent from possibly partially sent; never retry it.
Closing cannot undo previous sends. A fresh connection requires another Connect.

## Lifetime and Limits

- At most four active/connecting sessions globally, independently of the existing
  four one-shot sends. Reject excess connections immediately without a wait queue.
- A session closes after five minutes without a successful operator send (initially
  measured from connection). Polls and incoming/control frames do not reset this
  operator-idle timer. Maximum lifetime is 30 minutes regardless of traffic.
- The visible client polls once per second. Successful polls renew a 30-second
  lease; losing the tab, network or UI closes orphaned sessions when it expires.
  Background-tab throttling may therefore close a session; show this clearly.
- Connect has a 15-second deadline; each manual write has a five-second deadline.
  Concurrent sends on one session are rejected, not queued. The reader runs
  independently so unsolicited server messages remain visible.
- Retain a ring of at most 256 message records and 1 MiB of decoded payload per
  session. Evictions increment an explicit gap count and retain monotonic sequence
  numbers. The UI applies the same limits to its displayed log, including a visible
  local-eviction count. Hex/string/JSON overhead is outside the decoded-byte budget
  but remains bounded by the record and payload limits.
- Keep the existing per-message min(configured body limit, 1 MiB) cap, strict
  text/hex validation, 16 KiB combined operator-header/subprotocol budget, 3 MiB
  API envelope, 64 KiB upstream handshake-header cap and verified TLS. No compression,
  environment proxies, redirects, or unoffered subprotocol/extensions.
- An oversized incoming message retains its bounded prefix, is marked truncated,
  and closes the session. Malformed framing and interrupted reads also close it;
  partial retained data is explicitly incomplete, never a valid replay template.
- Retain at most four closed session records for up to 60 seconds so final state
  can be polled; evict the oldest closed record first. Closed retention does not
  occupy active connection slots. Explicit disposal can remove a closed record.

## Scope and Ownership

Use the shared current scope manager and equivalent HTTP/HTTPS URL semantics.
Check scope before dialing, after handshake before publishing a usable session,
and immediately before every manual send. Recheck all active sessions at least
once per second; close an excluded session with reason scope_revoked even while
the peer is silent. Already in-flight writes cannot be recalled. Do not depend
only on UI events or a lossy event stream for revocation.

Use cryptographically random session IDs with at least 128 bits of entropy, held
only in the initiating UI's memory. Do not expose a global session-list endpoint.
Apply the existing loopback Host and same-origin protections to every endpoint,
including polling. Session IDs, handshake credentials and payloads must not be
logged or persisted. This does not introduce account authentication: the existing
trusted-local-machine threat model still applies.

## Implementation Boundaries

Extract the existing validated, bounded ws/wss handshake into a shared helper
without changing one-shot behavior. A dedicated session manager owns registry,
socket lifecycle, reader, serialized writer, bounded log, timers and cleanup.
Timers and background work are shut down on manager Close. Register it in main;
API handlers must not own long-lived goroutines or leak request bodies/contexts.
Connection cancellation closes the socket and releases capacity. After successful
publication, the session has its own bounded lifetime, not the completed connect
request's context. A lost connect response is covered by the lease timeout.

Suggested endpoints:

- POST /api/websocket-repeater/sessions: connect only; no application message is sent.
- POST /api/websocket-repeater/sessions/{id}/send: send one validated message.
- GET /api/websocket-repeater/sessions/{id}?afterSequence=N: current state, close
  reason, counters and at most 100 retained records after the cursor. Return
  oldest/latest sequence and next cursor to make evictions unambiguous.
- POST /api/websocket-repeater/sessions/{id}/close: idempotent network close,
  retaining terminal state briefly. No retry or reconnect side effects.
- DELETE /api/websocket-repeater/sessions/{id}: close and dispose the record.

Return no-store responses. Strictly validate IDs, duplicate/malformed cursor
parameters and request bodies. Missing/expired IDs return 404; busy/invalid-state
sends return 409; global capacity returns 429. Network failures return structured
states without credential-bearing raw errors. Close is race-safe against reads,
writes, disposal, scope changes and timer expiry; release every slot exactly once.

The React session controller owns polling, cursors, request cancellation and stale
response guards. It performs best-effort close on workspace departure/unmount;
lease expiry is the server-side fallback. Neither credentials, IDs nor logs enter
localStorage, SQLite or the passive capture history. Browser restart does not
restore connections. No automatic cookie jar or authentication state transfer.

## Verification

Test local ws/wss peers requiring two messages on one socket; unsolicited receive;
text/binary payloads; handshake protections; current scope before connect/send;
silent-peer scope revocation; idle/lifetime/lease expiry; cancellation, disposal,
server shutdown and concurrent send/close races; capacity release; per-message and
ring limits; cursor gap reporting; expired IDs and origin rejection. Use controlled
short test durations and explicit synchronization rather than multi-minute sleeps.
Preserve all existing one-shot and passive-history tests. UI coverage includes
connect/send/receive/close, locked connection fields, stale polling, departure
confirmation, eviction notices and escaped payload display. Run Go race tests,
frontend tests/build, vet, Windows compilation, and browser checks. Update the
existing draft PR only after verification; do not merge.
