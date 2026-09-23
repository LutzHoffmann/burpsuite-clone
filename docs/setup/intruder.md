# Intruder (HTTP Fuzzer)

Intruder sends generated HTTP requests only to destinations currently included in
Target Scope. A scope rule is an execution boundary, not authorization: test only
systems you own or have explicit permission to assess. Start with a loopback test
server and a narrow include rule.

## Workflow

1. Open Target and add an include rule for the exact authorized scheme, host,
   port, and path. An empty scope blocks Intruder.
2. Open Intruder, use **Send to Intruder** from a complete History request or
   the current Repeater draft, or enter an absolute destination URL and a
   matching raw origin-form HTTP request manually. The `Host` header must match
   the URL authority. Handoff creates an unsaved draft; it never starts traffic.
3. Select bytes in the request editor and mark a position. Enter one payload per
   line for each position in text/UTF-8 or hex-byte mode. Hex input requires
   complete byte pairs; empty lines are empty payloads. The raw request editor
   normalizes HTTP line endings to CRLF before calculating byte offsets.
4. Choose Sniper, Battering Ram, Pitchfork, or Cluster Bomb. Set the request
   limit, concurrency, rate, and per-request timeout. Save the draft, then start
   it explicitly. Review results in pages of at most 100.
5. Pause to let in-flight requests finish, resume from the next persisted
   sequence, or abort to cancel in-flight work. Filter paged results by status,
   error, MIME, or payload text and open a result for a bounded body preview.
   Jobs never resume automatically after application restart.

## Attack semantics

- Sniper tests each position independently against its assigned payload list.
- Battering Ram applies one payload to every position in each request.
- Pitchfork advances position lists together and stops at the shortest list.
- Cluster Bomb emits the Cartesian product of assigned lists in deterministic
  order. Validate the exact generated count before starting.

## Limits and safety

The server enforces 1-100,000 generated requests, 1-20 workers per job,
0.1-100 request starts per second, and 1-120 second timeouts. A template is
limited to 2 MiB, each payload to 1 MiB, and the total payload count to 100,000.
Only HTTP and HTTPS are supported. Generated requests may change the target
path, headers, or body, but not the configured scheme, host, or port. The target
is checked again immediately before dispatch; removing it from scope pauses the
job. Results are saved locally in the active project. Capture quota may omit
body bytes while retaining metadata and a storage warning.

The current workspace supports text and hex payloads, History and Repeater
handoff, basic server-side result filters, and bounded result inspection. A
byte-safe hex editor for the entire raw request, baseline comparison controls,
and a polished preflight preview remain planned; do not treat this
implementation as feature parity with Burp Suite.
