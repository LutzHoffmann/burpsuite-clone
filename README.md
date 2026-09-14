# BurpSuite Clone

Local proxy-first web application for authorized web application security testing.

## Scope

The application provides HTTP/HTTPS proxying, independent request and response interception,
ordered regex/literal replacements, history, inspection, Repeater, Target Scope,
Site Map, passive WebSocket history, and local project storage.

WebSocket forwarding supports `ws://` and `wss://`, with a read-only message
inspector. See [WebSocket history](docs/setup/websockets.md) for capture limits,
opaque compressed payloads, and the HTTP-interception bypass.

See [Response interception and replacement rules](docs/setup/response-intercept.md)
for the v2b workflow, examples, and body-editing limits.

Repeater requests have a 60-second total timeout, including connection setup and
response body transfer. A shorter caller deadline still takes precedence. Timed-out
requests return an explicit timeout error rather than a successful partial response.

History uses 100-entry pages and server-side search. A configurable 1 GiB capture
budget pauses new recording without deleting existing data or stopping proxy
traffic. See [Capture storage and history](docs/setup/capture-storage.md) for
accounting limits, warnings, and the paged API.

## Safety

Use this tool only against systems you own or are authorized to test. Captured traffic and generated certificates stay local by default.
The unauthenticated API and proxy are intentionally restricted to loopback IP addresses. Network-wide operation requires authentication and client access controls that are not part of v1.

## Target Scope

Target scope is an authorization boundary, not permission to test a system. Open Target and add include, exclude, scheme, host, port, and path rules only for systems you are explicitly authorized to assess.

- Out-of-scope traffic is forwarded and remains in History, but it is not intercepted or projected into Site Map.
- Scope changes rebuild Site Map in the background. The last complete map remains visible until the replacement is ready.
- The parameter inventory stores names, locations, and types only. It never stores parameter values; captured values remain in History.
- An empty scope permits no interception.
- Future active or attack tools must require explicit scope before they can run.

### Operator flow

1. Start the application and open `http://127.0.0.1:9080`.
2. Configure a dedicated test browser to use `127.0.0.1:8080` as its HTTP and HTTPS proxy. Plain HTTP traffic can now pass through the proxy.
3. For HTTPS interception, download the local CA from Settings and trust it only in that dedicated browser or test profile. Remove that trust when testing is complete.
4. In Target, define the narrowest include, exclude, and path rules that cover the authorized system.
5. Confirm the scope before enabling interception. Traffic outside it will still be recorded in History without entering the interception queue or Site Map.

## Run

```bash
cd web && npm ci && npm run build
cd ..
go run ./cmd/proxy
```

The proxy serves the built frontend from `web/dist`.

## Test

```bash
go test ./...
cd web && npm test && npm run build
```
