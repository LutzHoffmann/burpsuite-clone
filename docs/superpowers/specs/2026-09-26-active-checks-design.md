# Bounded active checks from crawl discovery

## Intent

Continue the staged scanner build toward broad web-application coverage while
preserving the approved default: only non-destructive checks run without a
separate high-impact opt-in. This stage adds repeatable, evidence-backed
observations for URL query parameters and discovered GET forms. It is not a
claim of Burp Suite scanner parity or confirmed exploitability.

## Discovery data needed

The crawler currently stores a URL with query values removed, plus a hash of
the full URL. Extend each saved page with a bounded, sorted list of query
parameter **names**, never values. Store up to 20 distinct names per page,
each at most 256 bytes. Keep existing redaction of page/action URL query
values. Existing runs have an empty name list and remain readable.

GET forms already retain action URL, method, and field names/types. Only GET
forms whose action is on the same origin as the crawl seed and allowed by
current Target Scope become candidate input points. Hidden/password/file
fields are excluded from automatic tests. No captured field values are used.

## Explicit scan run

The user selects a completed crawl run and explicitly starts active checks.
The UI displays the target origin, estimated maximum number of requests,
limitations, and an authorization confirmation. The server validates the
source run, its seed History item, and current scope. A separate, asynchronous
active-check run records its own state; it can be cancelled, reopened after
restart, or deleted without deleting the crawl or History source.

One active-check run at a time. It tests at most 25 candidate URLs/forms, five
parameter names per candidate, and 100 requests total. Requests are sequential
and at least 500 ms apart; each has a five-second timeout, a 64 KiB response
body cap, and a 60-second overall deadline. The current scope is checked
immediately before each send. No redirects, environment proxy, captured
headers/cookies, or original query values. TLS verification remains enabled.
Only GET is sent. Persistence failure stops further traffic. Scope revocation
or cancellation stops before the next request.

## Check module and evidence

For each candidate parameter, send a fresh random marker in that parameter
only. Do not concatenate marker with a captured value. Inspect only the
bounded response body. Classify exact marker occurrences as plain-text,
HTML text, HTML attribute, script/style raw text, or unknown/ambiguous using
an HTML tokenizer with conservative handling of malformed markup. Record
status, truncation, marker-found flag, context class, and a safe error code.
Do not store response bodies, snippets, marker values, credentials, or test
URLs with values. A reflected marker is an **observation**, not a confirmed
XSS or injection finding. Context classification can be incomplete when the
response is truncated or malformed and must say so in the UI.

Results are deduplicated by candidate URL, input source, and parameter name.
The UI shows which input point was tested and why the result is observational.
Passive header/cookie findings remain a separate category; active observations
do not silently elevate them or change existing passive-finding semantics.

## Design choice

Implement a separate active-check service and SQLite model, reusing crawler
candidate metadata, `repeater.Sender`, Target Scope, and the scanner workspace.
This avoids embedding payload traffic inside crawl discovery and preserves a
clear authorization boundary. A generic plugin/payload framework is deferred:
its extensibility would add complexity before the first module has a reliable
evidence contract.

## Verification

Tests must show: query values are never persisted or forwarded; hidden and
password fields are skipped; only in-scope same-origin GET candidates are
sent; limits, pacing, redirects, cancellation, and scope revocation stop
traffic correctly; false reflection outside the body or with altered markers
is not reported; malformed/truncated HTML is classified conservatively;
restart recovery marks unfinished runs interrupted; deletion preserves crawl
and History; API and UI require confirmation and show observation language.

## Out of scope

No POST/form submission, authentication/session handling, JavaScript browser
execution, SQL injection payloads, deserialization probes, file upload tests,
destructive checks, or exploit confirmation in this stage. These require
separate designs, consent controls, and verification criteria.
