# BurpSuite Clone

Local proxy-first web application for authorized web application security testing.

## Scope

v1 focuses on HTTP/HTTPS proxying, interception, history, inspection, Repeater, and local project storage.

## Safety

Use this tool only against systems you own or are authorized to test. Captured traffic and generated certificates stay local by default.

## Run

```bash
go run ./cmd/proxy
```

Open `http://127.0.0.1:9080`, configure your browser proxy to `127.0.0.1:8080`, download the CA from Settings, and trust it for HTTPS interception.

## Test

```bash
go test ./...
cd web && npm test && npm run build
```
