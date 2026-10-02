# Active scanner: query reflection probe

This is a narrowly scoped first active check, not a general vulnerability
scanner. It sends new requests, so use it only on systems you own or are
explicitly authorized to test. A GET endpoint can still have side effects.

1. Add a narrow include rule in Target Scope.
2. Capture a successful in-scope GET request with query parameters in History.
3. Select that request, open Scanner, choose a maximum of 1-5 probes, and
   confirm the target before starting.
4. Review each parameter's HTTP status and whether the random marker appeared
   in the first 64 KiB of the response body. Reflection is not proof of XSS.

The scanner sends one random marker for each of up to five distinct query
parameter names. Other query parameter names remain present with empty values;
their original values are not replayed. It does **not** forward captured
cookies, authorization headers, or request bodies. One scan runs at a time;
requests are sequential and start no faster than twice per second. Each
request has a five-second timeout. Current scope is checked before each send,
redirects are not followed, environment proxies are not used, and TLS
verification remains enabled. If scope is revoked, the scan stops before the
next request. Closing the Scanner view cancels its current request.

The last 100 run summaries are shown in Scanner and can be reopened after
restart. Up to 10,000 runs are retained in the local project. Each finished
probe is saved before the next request starts; a database error stops traffic.
Finished runs can be deleted individually to free space; deleting a run does
not delete its source History entry. Running scans cannot be deleted.
Markers, original query values, credentials, headers, and response bodies are
not stored in scan records. Runs interrupted by application restart are marked
as interrupted. Results are not represented as confirmed vulnerabilities. This
reflection check does not use crawl results, test form bodies or authenticated
flows, or run injection payloads.

## Bounded site crawler

The Scanner workspace also offers a separate, explicitly confirmed crawl of a
selected in-scope GET History entry. It follows links on the same origin only,
rechecks current scope before each request, and does not forward captured
headers or cookies. It sends GET requests only, never submits forms, never
follows redirects, and does not launch vulnerability probes automatically.

Choose 1-25 pages and depth 0-3. The crawler runs one job at a time with at
least 500 ms between requests, a 5-second per-request timeout, a 60-second
overall deadline, and a 64 KiB response-body cap. You can cancel a running
job. Pages and form field names/types are saved without response bodies,
query values, or form values; finished runs can be reopened or deleted. The last 100 runs are
listed, with a 10,000-run project cap. Restarted jobs are marked interrupted.
The crawler does not execute JavaScript or perform automatic login.

For an authenticated GET-only run, enter a session Cookie and/or Authorization
header explicitly before starting the crawler. These values are sent only to
same-origin, currently in-scope targets, are held in memory for that run, and
are not copied from History or written to crawl records. Re-enter them for
active checks; a crawl session is not reused automatically. Do not supply
credentials for a plain-HTTP target unless you accept their exposure on the
network. This is static-header replay, not automatic login, token refresh, or
session-expiry detection.

## Active checks from a crawl

After a crawl completes, refresh the crawl list under Active checks and select
that run. Optionally select **Check open redirects** or **Check credentialed CORS**. Starting checks requires
a separate authorization confirmation. The
service tests up to 25 discovered same-origin query or GET-form targets, up to
five parameter names per target, and at most 100 requests total. Hidden,
password, and file fields are skipped. Each GET request marks one parameter and
keeps other query names with empty values; it never replays captured values,
captured cookies, captured authorization headers, or POST
forms. Scope is rechecked before every request; requests are sequential, at
least 500 ms apart, and capped at five seconds and 64 KiB each. The whole run
has a 60-second limit. Redirects are not followed and environment proxies are not used.

When enabled, the redirect check sends a second synthetic value for each
selected parameter, within the same 100-request cap. It uses a unique
`https://redirect-check.invalid/` URL as the value and records an observation
only when a 3xx response contains that exact absolute URL in `Location`.
The client never requests that URL. This can indicate an open redirect but
does not establish exploitability. The option is off by default.

When enabled, the CORS check sends one additional GET per selected target,
within the same 100-request cap. Original query values are cleared. A random
`https://<nonce>.cors-check.invalid` Origin header is sent to the in-scope
target; that hostname is never requested. An observation is recorded only
when the response contains that exact Origin in `Access-Control-Allow-Origin`
and `Access-Control-Allow-Credentials: true`. This is not proof that sensitive
data can be read by an attacker. The option is off by default.

Reflection results record whether an exact random marker appeared in the response
body and, if recognizable, its HTML context. These are observations, **not**
confirmed XSS or injection vulnerabilities. Truncated and ambiguous responses
are marked conservatively. No response body, marker value, original query
value, or form value is stored. Runs can be cancelled, reopened, and deleted;
restart marks unfinished runs interrupted. Automatic login, POST forms,
JavaScript-driven inputs, and exploit confirmation are not covered.
Finished runs can be downloaded as a self-contained HTML observation report;
the report contains only the same redacted metadata shown in the workspace.
