# Capture Quota and Paged History

## Approved Behavior

History displays 100 entries per page and searches all retained history, not just
the visible page. A configurable storage budget pauses new capture persistence
when exhausted. Existing captures are never automatically deleted. Proxy traffic
continues to be forwarded. This document specifies the cross-component contract;
implementation follows its review.

## Choices

Use database-enforced logical capture accounting rather than filesystem-size
sampling: sampling cannot atomically reserve space for concurrent writers and
includes reusable SQLite pages. Automatic oldest-first deletion is explicitly
excluded. This is a capture-data budget, not a hard disk-space guarantee.

Use ID cursors and a snapshot boundary rather than offset pagination: newly
arriving traffic must not shift older pages or cause duplicates.

## Capture Budget

- Default: 1 GiB (1,073,741,824 bytes) per current local project, adjustable in
  Settings in MiB. Accept positive integer limits up to 1 TiB; zero is invalid.
- Count persisted HTTP/HTTPS exchanges and Repeater sends, including retained
  bodies, raw messages, headers and variable metadata. Count actual retained
  bytes after existing per-body truncation, not advertised upstream sizes.
- Define each record's charge as its retained byte fields plus UTF-8 bytes of
  stored text/JSON fields and a documented fixed 256-byte record allowance.
  Derived Site Map data, indexes, settings, certificates, SQLite page overhead
  and journal files are excluded. Explain these exclusions in Settings and docs.
- A migration computes charges for existing records without materializing all
  bodies in application memory. Existing data above the initial budget remains
  readable and starts in the paused state.
- Persist project budget, usage, paused state and skipped-record count. Reserve
  usage and insert the record in the same database transaction. Concurrent
  proxy/Repeater writes must not overshoot the budget; failed writes roll back
  their reservation. Do not rely solely on an in-process mutex.
- A record fits when charge <= remaining budget. If it does not fit, save none
  of that record and persist the paused state. While paused, save no new records,
  including smaller ones, until the user increases the budget above current
  usage. Lowering a budget never deletes data.
- Saving a larger limit above current usage resumes capture. New traffic is
  captured from then onward; missed traffic cannot be recovered.
- Updates to existing notes/tags account for their charge delta transactionally.
  Reads and non-growing edits remain available when paused. Reject edits that
  would exceed the budget without changing the existing data.
- Distinguish quota rejection from database failure with a typed store error.
  Never publish a successful history event or project a skipped exchange into
  Site Map. Do not log captured content in warnings.

## Proxy and Repeater

Quota rejection does not alter the upstream response, interception decision or
forwarding behavior. A visible application-wide warning says that capture
storage is paused and new traffic is not being saved.

Repeater still returns the received response when its history cannot be saved
because of quota. Add an explicit saved=false and storage warning to that result;
do not report a network failure or encourage repeating the request. Other
database failures remain distinct from quota rejection.

Expose GET /api/storage and PUT /api/storage with a validated limitBytes update.
Return limitBytes, usedBytes, paused and skippedRecords. Keep existing local-only
request protections. Publish storage.status.changed on pause/resume and settings
changes; periodically refresh status in the UI so reconnection cannot hide a
pause. Do not send one browser event for every skipped packet.

## History Contract

- Add GET /api/history/page returning items, nextBeforeId and snapshotId.
  Page size is fixed at 100; fetch at most 101 matching rows to detect more.
- First request establishes the maximum retained exchange ID as snapshotId.
  Subsequent pages constrain ID <= snapshotId and ID < beforeId, ordered DESC.
  Strictly validate positive cursor values and compatible boundaries.
- Support search, method, host and inScope filters in SQL before pagination.
  Search matches host, path and query as literal text, with parameterized SQL
  and escaped LIKE metacharacters. Memory-store behavior must match SQLite.
- Retain the existing /api/history array response for compatibility, but cap it
  at the newest 100 matching entries and document the bounded behavior. No UI
  fallback may fetch the entire history. Internal Site Map rebuild paging stays
  separate from user-facing history paging.
- The UI keeps only the current page plus cursor navigation state. Previous and
  Next show 100 entries at a time. Search/filter changes reset the cursor and
  snapshot. Ignore or cancel stale responses from earlier navigation/search.
- New traffic updates the first page only when browsing the newest snapshot;
  while browsing older pages, show a New traffic / Refresh control rather than
  resetting the operator's position. Explicit refresh begins a new snapshot.
- Move existing client-side search and scope filtering to server-side filters.
  Preserve a selected detail when possible; changing pages selects an entry on
  the new page. Metadata edits reload that page rather than the entire history.
- Preserve existing layout and mobile usability. Settings shows budget, retained
  capture usage, warning state and a validated limit editor.

## Verification

Tests cover empty history, 100/101-row boundaries, forward/back navigation,
global search beyond page one, scope filtering, concurrent inserts during paging,
stale UI responses and events received while browsing older pages.

Quota tests cover exact-fit/oversized records, retained-byte accounting, rollback,
concurrent writers, Repeater and note edits, restart persistence, existing data
above quota, invalid settings, pause/resume, and no deletion. Real HTTP/HTTPS
integration tests prove that traffic still reaches the client while persistence
is paused and skipped exchanges do not appear as successfully stored captures.

Run full Go tests and race checks, vet, frontend tests/build, Windows compilation,
and independent review. Update the existing draft PR without merging it.

## Out of Scope

Automatic deletion, manual bulk deletion, full-disk enforcement, data export,
WebSocket interception, arbitrary page sizes, Repeater-history pagination and
multi-project selection are separate changes. Capture quota does not bound
concurrent in-memory interception queues or total process memory.
