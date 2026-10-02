# Bounded unauthenticated crawler

## Intent and scope

The user wants a staged expansion of the active scanner toward broad web
application coverage. This first stage discovers unauthenticated pages and
input points without automatically testing them. Later stages add active
checks, session support, and finding/report workflows. This stage does not
claim Burp Suite scanner parity.

## Entry point and behavior

The user selects an in-scope HTTP(S) History exchange as the seed and confirms
that they are authorized to crawl it. The crawler uses the exchange URL, not
captured request headers, cookies, credentials, or body. It sends GET requests
only. Each response may contribute links and form metadata; forms are never
submitted. The crawler follows HTTP(S) links on the seed origin only when the
current Target Scope allows the complete URL. Fragments are removed, URL
normalization avoids duplicate visits, and non-HTTP(S) links are ignored.

Controls exposed in the UI are maximum pages and maximum depth, each with a
small fixed upper bound. There is one crawl at a time, sequential requests,
at least 500 ms between requests, a 5-second request timeout, a 64 KiB response
body cap, and a total runtime ceiling. Redirects are not followed. Network
proxy settings are not inherited. TLS verification remains enabled. The
current scope is checked again immediately before each request, and a scope
revocation stops the crawl. The user can cancel a running crawl.

## Results and persistence

A run stores its seed History ID, limits, state, timestamps, counts, and
bounded records for visited pages and discovered form fields. Each page record
includes normalized URL with query values removed, status, content type, depth, truncation, and a safe
error code. Each form record includes page URL, action URL, method, and field
names/types; no query values, field values, response bodies, credentials, or captured
headers are retained. Results can be reopened after restart and deleted
without deleting source History. Interrupted runs are marked on startup.

The UI shows progress, cancellation, visited pages, and discovered forms.
It clearly labels discovery as distinct from vulnerability verification.
The existing query-reflection scanner remains an explicit, separate action;
crawled URLs do not trigger probes automatically.

## Design choice

A separate crawler service and persistence model are preferred over expanding
the current reflection-probe loop. It isolates discovery from active testing
and keeps independent budgets and states. Reusing the existing scope checker,
request sender, SQLite store, and Scanner workspace avoids parallel security
semantics or a second HTTP stack. An all-in-one scan pipeline would reduce
initial wiring but would make traffic and cancellation harder to reason about.

## Error handling and verification

Invalid or out-of-scope seeds are rejected before traffic. A page fetch error
is recorded without aborting the whole crawl unless context is cancelled or
scope is revoked. Persistence failure stops further requests. Tests cover
URL normalization and de-duplication, same-origin/scope enforcement, limits,
form extraction without submission, cancellation, restart recovery, storage
deletion, API validation, and frontend interactions. Local integration tests
use controlled HTTP fixtures and assert request counts and methods.

## Explicit non-goals

No JavaScript execution, browser-based crawling, robots.txt interpretation,
authentication, form submission, active payloads, or vulnerability claims in
this stage. These require separate design and review in later stages.
