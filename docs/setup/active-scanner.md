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

Results are shown in the current browser view only. They are not retained as
project findings or represented as confirmed vulnerabilities. This version does
not crawl, test form bodies or authenticated flows, or run injection payloads.
