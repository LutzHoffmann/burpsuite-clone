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
parameter names. It does **not** forward original query values, captured
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
The crawler does not execute JavaScript or handle authenticated sessions.
