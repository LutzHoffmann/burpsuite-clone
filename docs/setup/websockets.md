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

Protocols requiring multiple login messages or persistent sessions are not yet
supported. This repeater has no automatic cookie/session management or saved runs.

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
