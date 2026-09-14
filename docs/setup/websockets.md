# WebSocket History

Configure the test browser's HTTP/HTTPS proxy as usual. For `wss://`, trust the
local proxy CA in that dedicated test profile. Open **WebSockets** to select a
connection, browse messages, and inspect their captured payload.

## Behavior

- Both directions are forwarded unchanged, including fragmentation, control
  frames, negotiated subprotocols, and compression.
- WebSocket handshakes bypass HTTP interception and replacement rules. Messages
  are not editable, interceptable, or replayable in this first stage.
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
