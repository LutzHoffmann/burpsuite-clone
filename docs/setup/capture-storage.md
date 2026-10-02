# Capture Storage and History

The capture budget defaults to 1 GiB. Change it in Settings, in MiB, up to 1 TiB.
It accounts for retained proxy exchanges and Repeater history: stored bodies,
raw messages, headers, text/JSON metadata and a 256-byte allowance per record.
Upstream advertised message sizes do not determine the charge. Existing
per-message body limits still apply before storage accounting.

This is not a hard disk-space limit. SQLite pages, indexes and journals,
certificates, settings and derived Site Map data need additional space. Leave
free space on the disk; the budget also does not bound total process memory.

## When the Budget Is Full

If a new capture cannot fit, recording pauses. Existing captures are not deleted
or shortened. Traffic continues through the proxy, including HTTPS interception.
The application shows a warning that new traffic is not being saved. Skipped
captures are not added to History or Site Map. Their contents are not retained
for later recovery.

Repeater still displays the response when quota prevents saving its history,
with an explicit not-saved warning. Do not resend a request just to save its
history: sending again can repeat actions on the target application.

To resume recording, increase the limit above the current usage. An unchanged
or lower setting does not clear a paused state. Lowering the limit below usage
never deletes existing data. Reads and non-growing metadata edits still work.
The budget, usage and skipped-record counter survive restarts. Old projects
above the initial budget open with their data intact and recording paused.

## Browsing History

History shows 100 entries per page. Search examines retained host, path and query
text across the whole history, not response bodies or only the visible page.
Scope filtering is also applied before paging. Search treats percent signs and
underscores literally, not as SQL wildcard patterns.

Previous and Next navigate within a stable snapshot. Traffic arriving while you
read older pages does not shift their boundaries. Use Refresh to begin with the
newest traffic. Changing search or scope filters starts again on page one.

## Local API

- `GET /api/storage`: `limitBytes`, `usedBytes`, `paused`, `skippedRecords`.
- `PUT /api/storage`: JSON `{ "limitBytes": 1073741824 }`. Positive integer bytes,
  maximum 1 TiB; settings UI uses MiB.
- `GET /api/history/page`: `{ "items": [], "nextBeforeId": 0, "snapshotId": 0 }`.
  The initial request omits cursors. Send returned `snapshotId` and
  `nextBeforeId` as `beforeId` for the next page. Zero nextBeforeId means no more.
  An empty initial snapshot is zero and must not be sent as a cursor.
- Page filters: `search`, `method`, `host`, `inScope=true|false`.
- Legacy `GET /api/history` retains its array shape, but returns at most the
  newest 100 matching entries. Use the page endpoint for older entries.
- Repeater send results include `saved`; quota rejection adds `storageWarning`
  while returning the received response normally.

All endpoints retain the existing local-only access restrictions. A successful
HTTP response from a target is not proof that its capture was saved.
