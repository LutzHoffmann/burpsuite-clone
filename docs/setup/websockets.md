# WebSocket History and Repeater

Configure the test browser's HTTP/HTTPS proxy as usual. For `wss://`, trust the
local proxy CA in that dedicated test profile. Open **WebSockets** to select a
connection, browse messages, and inspect their captured payload.

## Behavior

- Both directions are forwarded unchanged, including fragmentation, control
  frames, negotiated subprotocols, and compression.
- WebSocket handshakes bypass HTTP interception and replacement rules. Captured
  history stays read-only. The separate repeater below edits copies, not live
  browser traffic; live interception remains unavailable.
- A rejected upgrade is forwarded as an ordinary HTTP response and saved in HTTP
  History; the upstream request is not repeated.
- Connections show capture-time scope using the equivalent HTTP/HTTPS URL.
  Out-of-scope connections are still forwarded and recorded, not added to Site Map.
- Message details show direction, time, sequence, type, observed size, truncation,
  and completeness. Valid uncompressed text is displayed as text, never HTML;
  other payloads are hex. Negotiated extensions make payloads opaque: compressed
  data is not decompressed and sizes represent observed wire payload bytes.
- Connection and message lists use 100-entry snapshot pages. The newest page
  refreshes every five seconds. Older pages stay stable and offer **New traffic /
  Refresh** when newer records exist.

## Recording Limits

Capture is passive and best-effort. Each message retains at most the configured
body limit. Globally, at most 64 connections are observed, with 64 pending message
records and 16 MiB of queued payload. Excess connections continue forwarding but
are not recorded. A live UI notification reports the connection limit; reconnect
unrecorded connections after capacity becomes available. Notifications are not
durable and may be missed while the UI is disconnected.

WebSocket records share the configured capture budget with HTTP history. Queue
overflow or rejected message writes increment the connection's gap counter;
sequence numbers preserve the missing positions. Existing history is never deleted
automatically. If the budget is raised, existing recorded connections can resume
message recording. A connection whose initial record could not be saved requires
reconnection to appear in history.

Normal two-sided close frames mark a connection closed. Abrupt disconnects and
open records recovered at application startup are marked interrupted. Malformed
framing stops parsing, not forwarding, and marks capture incomplete. Stream idle
timeouts still apply. This is not a lossless packet recorder.

## Read-Only API

- `GET /api/websockets`
- `GET /api/websockets/{connectionId}`
- `GET /api/websockets/{connectionId}/messages`
- `GET /api/websockets/{connectionId}/messages/{messageId}`

Lists return `items`, `nextBeforeId`, and `snapshotId`; subsequent pages send
`beforeId` and `snapshotId`. List records omit payloads. Message detail returns
`payload` and `payloadFormat` (`text` or `hex`). The existing loopback and origin
restrictions apply to all endpoints.

## One-Shot Repeater

Open **WebSockets**, select a complete, untruncated, uncompressed client-to-server
text or binary message, then choose **Use in WebSocket Repeater**. Invalid source
records are rejected by the server, not just disabled in the UI. You can also
author a new message directly in the editor. Text requires valid UTF-8; hex input
requires complete byte pairs without spaces. Changing the format reinterprets
the input rather than converting it.

Each Send opens a fresh, independent connection and sends exactly one message.
It never injects into the original browser socket, follows redirects, inherits
environment proxies, or retries automatically. Current scope is checked before
connecting (ws maps to http, wss to https); empty scope denies sends. Scope
changes affect subsequent sends. TLS certificate verification is enabled.

Enter optional Origin, Authorization, or Cookie headers explicitly, one
`Name: value` per line. The original handshake's credentials are not copied.
Subprotocols are comma-separated. Generated/hop-by-hop headers cannot be
overridden; unoffered response subprotocols/extensions are rejected. Compression
is disabled on the new connection.

After sending, collect up to 20 incoming data messages for five seconds, with a
15-second overall deadline. Incoming messages may be unsolicited, not correlated
replies. A successful send indicates a socket write, not application acceptance.
Results show outcome, incoming text/hex payloads, observed sizes and truncation.
Cancel closes the independent connection; it cannot undo an already-sent message.
Unsent edits/results are protected by a navigation confirmation and discarded
when you leave. They and credentials are not saved to project history, settings,
or localStorage. The persistent capture quota does not apply to this transient UI.

Outgoing and individual incoming payloads are bounded by the smaller of 1 MiB and
the configured body limit; aggregate retained incoming payload is capped at 1 MiB.
Oversized input is rejected; oversized incoming messages retain a bounded prefix
and terminate collection. At most four sends run simultaneously, without a queue.
Operator handshake headers and the serialized subprotocol header share a 16 KiB
budget; subprotocol input is additionally capped at 4 KiB. The API JSON envelope
has a 3 MiB limit, so heavily escaped content may reach that limit before 1 MiB.
Upstream handshake response headers are capped at 64 KiB before HTTP parsing,
including after TLS decryption for `wss://`. Oversized or unterminated headers
cannot consume unbounded memory while waiting for the overall deadline.

For protocols requiring multiple manual login messages, use Session mode below.
Neither mode has automatic cookie/session management or saved runs.

### Repeater API

- `GET /api/websockets/{connectionId}/messages/{messageId}/draft`: validated copy
  containing `url`, `type`, `payload`, `payloadFormat`, empty `headers` and
  `subprotocols`. Does not connect or send.
- `POST /api/websocket-repeater/send`: accepts that editable request shape;
  returns `sent`, `outcome`, `messages`, `durationMs`, and `subprotocol`.

Preflight failures use 400 (invalid input), 403 (out of scope), 429 (busy), or 503
(unavailable). Network attempts return an outcome such as `peer_closed`,
`window_complete`, `timeout`, `handshake_error`, `tls_error`, `size_limit`, or
`message_limit`, preserving bounded partial received messages. Raw network errors
and credential-bearing request data are not echoed in errors.

## Manual Sessions

Choose **Session** in the repeater and explicitly connect. Connect sends no
application message. URL, handshake headers and subprotocols stay locked while
connected; edit the message and send once per click on the same socket. Incoming
messages appear continuously, including unsolicited traffic. The log shows both
directions in observation order, not request/reply correlation. Text is rendered
as text, never executable HTML.

Close explicitly when finished. Leaving the workspace, replacing a draft or
changing mode asks for confirmation and makes a best-effort close. A canceled or
failed write may already have partly reached the peer: it closes the connection,
reports unknown delivery and is never automatically retried. A successful write
does not prove application acceptance. Preflight validation errors leave the
connection usable.

Current scope is checked before and after the handshake, before every send, and
at least once a second while connected. Removing a target from scope closes even
a silent session; already in-flight writes cannot be recalled. Sessions retain
verified TLS, disabled compression and the one-shot handshake/input limits.

### Session Limits

- Four connecting/active sessions globally, separate from four one-shot sends.
  Busy sessions reject concurrent sends instead of queueing them.
- Connect deadline 15 seconds; write deadline five seconds. Operator close has
  at most one second for its control frame before forcing the socket closed.
- Five minutes without a successful manual send, or 30 minutes total lifetime,
  closes the session. Incoming messages and polling do not reset operator idle.
- The UI polls every second. A successful poll renews a 30-second lease; losing
  the tab/network or background-tab throttling can expire the connection.
- Server and UI logs retain at most 256 records and 1 MiB decoded payload, with
  visible eviction counts. Oversized incoming messages retain a truncated prefix
  and close the connection; interrupted partial messages are marked incomplete.
- Poll pages contain at most 100 records with an 8 MiB encoded response cap. The
  incoming JSON cap remains 3 MiB, including escaping and metadata.
- At most four terminal records remain for 60 seconds; they do not consume active
  slots. Explicit disposal removes a record immediately.

IDs, credentials and logs live only in memory, not localStorage, SQLite or passive
history. There is no session list, reconnect, automatic login sequence or browser
socket injection. Restarting the application/browser does not restore sessions.

### Session API

- `POST /api/websocket-repeater/sessions`: `{url, headers, subprotocols}`.
- `POST /api/websocket-repeater/sessions/{id}/send`:
  `{type, payload, payloadFormat}`.
- `GET /api/websocket-repeater/sessions/{id}?afterSequence=N`: state, reason,
  messages, oldest/latest sequence, `nextSequence` cursor and `droppedMessages`.
  Pass `nextSequence` as the next poll's `afterSequence`.
- `POST /api/websocket-repeater/sessions/{id}/close`: empty body, idempotent close
  while the terminal record remains available.
- `DELETE /api/websocket-repeater/sessions/{id}`: empty body, close and dispose;
  returns 204. Missing or expired IDs return 404.

All routes are no-store and apply the loopback/same-origin checks, including GET.
Invalid input returns 400, excluded targets 403, busy/closed sends 409 and exhausted
connection capacity 429. Network failures use bounded terminal reasons, not raw
peer errors. A `send_failed` reason means delivery is unknown. Session IDs are
unpredictable bearer references for the trusted-local-machine model, not account
authentication.
