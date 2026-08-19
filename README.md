# BurpSuite Clone

Local proxy-first web application for authorized web application security testing.

## Scope

v1 focuses on HTTP/HTTPS proxying, interception, history, inspection, Repeater, and local project storage.

## Safety

Use this tool only against systems you own or are authorized to test. Captured traffic and generated certificates stay local by default.

## Development

```bash
go test ./...
go run ./cmd/proxy
```
