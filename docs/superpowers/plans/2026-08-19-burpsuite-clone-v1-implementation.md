# BurpSuite-Clone v1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a local proxy-first BurpSuite-like web application with HTTP/HTTPS interception, history, inspector, Repeater, SQLite project storage, and a GitHub-ready repository.

**Architecture:** One Go binary runs both the local API server and proxy listener. The Go core owns certificates, proxying, persistence, intercept queues, and Repeater sends. A React/TypeScript web UI talks to the local API over REST and WebSocket.

**Tech Stack:** Go 1.23, standard `net/http`, `github.com/gorilla/websocket`, `modernc.org/sqlite`, React 19, TypeScript, Vite, Vitest, Testing Library, SQLite migrations stored in Go.

**Spec:** `docs/superpowers/specs/2026-08-19-burpsuite-clone-v1-design.md`

## Global Constraints

- Product: BurpSuite-like local security testing tool.
- First release: proxy-first.
- Form factor: OS-independent web app with a local backend.
- HTTPS interception: included in v1.
- Core stack: Go for proxy/API, React/TypeScript for UI, SQLite for local project storage.
- Repository quality: maintained GitHub-ready repository is part of v1.
- Bind local services to `127.0.0.1` by default.
- Do not expose the API or proxy externally unless the user explicitly changes the bind address.
- Generate and store the local CA under the local project/application data path.
- Show CA trust status and HTTPS interception status clearly in the UI.
- Never upload captured traffic, certificates, project data, or telemetry to external services.
- Private CA keys must never be committed, exported automatically, or placed in project files intended for sharing.
- Response interception is not required in v1.
- Editing is initially limited to text-safe request bodies.
- One Go process for v1, with API server and proxy listener inside the same binary.
- Vite for React/TypeScript.
- SQLite with explicit migrations.
- MIT license.
- Default API address: `127.0.0.1:9080`.
- Default proxy address: `127.0.0.1:8080`.
- Default body capture limit: `1048576` bytes per request body and per response body.
- Default intercept timeout: `120s`.

---

## Planned File Structure

Create these files during the tasks:

- `go.mod`: Go module definition.
- `cmd/proxy/main.go`: process entrypoint.
- `internal/config/config.go`: runtime defaults and environment parsing.
- `internal/store/store.go`: store interfaces and domain DTOs shared by backend packages.
- `internal/store/memory.go`: in-memory store for API tests and lightweight development wiring.
- `internal/store/sqlite.go`: SQLite store implementation.
- `internal/store/migrations.go`: embedded SQL migrations.
- `internal/store/sqlite_test.go`: storage tests.
- `internal/certs/authority.go`: local CA generation, persistence, fingerprinting, and leaf certificate creation.
- `internal/certs/authority_test.go`: certificate tests.
- `internal/intercept/rules.go`: intercept rule model and matcher.
- `internal/intercept/queue.go`: paused request queue.
- `internal/intercept/rules_test.go`: rule and queue tests.
- `internal/events/hub.go`: WebSocket event hub.
- `internal/api/server.go`: API server wiring.
- `internal/api/handlers.go`: REST handlers.
- `internal/api/server_test.go`: API tests.
- `internal/proxy/proxy.go`: HTTP/HTTPS proxy listener and request pipeline.
- `internal/proxy/capture.go`: capture helpers for headers, bodies, timing, and truncation.
- `internal/proxy/proxy_test.go`: HTTP and HTTPS proxy tests.
- `internal/repeater/repeater.go`: Repeater service.
- `internal/repeater/repeater_test.go`: Repeater tests.
- `web/package.json`: frontend package metadata.
- `web/vite.config.ts`: frontend build config.
- `web/tsconfig.json`: frontend TypeScript config.
- `web/index.html`: frontend HTML entry.
- `web/src/main.tsx`: React entrypoint.
- `web/src/api/client.ts`: typed REST client.
- `web/src/api/events.ts`: WebSocket client.
- `web/src/types.ts`: frontend DTOs.
- `web/src/App.tsx`: main application layout.
- `web/src/components/StatusBar.tsx`: proxy/project/CA/intercept status.
- `web/src/components/HistoryTable.tsx`: history table and filters.
- `web/src/components/Inspector.tsx`: request/response inspector tabs.
- `web/src/components/InterceptPanel.tsx`: intercept queue and actions.
- `web/src/components/Repeater.tsx`: Repeater tabs, editor, send history.
- `web/src/components/Settings.tsx`: proxy and CA settings.
- `web/src/styles.css`: application styling.
- `web/src/App.test.tsx`: UI smoke and workflow tests.
- `.github/workflows/ci.yml`: Go and frontend CI.
- `.github/ISSUE_TEMPLATE/bug_report.md`: bug report template.
- `.github/ISSUE_TEMPLATE/feature_request.md`: feature request template.
- `.github/pull_request_template.md`: pull request template.
- `.gitignore`: generated and sensitive files.
- `README.md`: project overview and local commands.
- `SECURITY.md`: supported versions, disclosure, sensitive data handling.
- `CONTRIBUTING.md`: development workflow.
- `LICENSE`: MIT license text.
- `docs/setup/ca-trust.md`: CA trust instructions.
- `examples/targets/go.mod`: local target module.
- `examples/targets/main.go`: local HTTP/HTTPS test target.

---

### Task 1: Repository Foundation

**Files:**
- Create: `go.mod`
- Create: `cmd/proxy/main.go`
- Create: `internal/config/config.go`
- Create: `.gitignore`
- Create: `README.md`
- Create: `LICENSE`
- Create: `SECURITY.md`
- Create: `CONTRIBUTING.md`

**Interfaces:**
- Produces: `config.Config`, `config.Load() Config`
- Produces: runnable command `go run ./cmd/proxy`

- [ ] **Step 1: Create module and config test**

Create `go.mod`:

```go
module github.com/lutzifer/burpsuite-clone

go 1.23
```

Create `internal/config/config_test.go`:

```go
package config

import "testing"

func TestLoadUsesLocalDefaults(t *testing.T) {
	t.Setenv("BC_API_ADDR", "")
	t.Setenv("BC_PROXY_ADDR", "")
	t.Setenv("BC_DATA_DIR", "")

	cfg := Load()

	if cfg.APIAddr != "127.0.0.1:9080" {
		t.Fatalf("APIAddr = %q", cfg.APIAddr)
	}
	if cfg.ProxyAddr != "127.0.0.1:8080" {
		t.Fatalf("ProxyAddr = %q", cfg.ProxyAddr)
	}
	if cfg.BodyLimitBytes != 1048576 {
		t.Fatalf("BodyLimitBytes = %d", cfg.BodyLimitBytes)
	}
	if cfg.InterceptTimeout.String() != "2m0s" {
		t.Fatalf("InterceptTimeout = %s", cfg.InterceptTimeout)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config`

Expected: FAIL because `Load` and `Config` are undefined.

- [ ] **Step 3: Implement config defaults**

Create `internal/config/config.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type Config struct {
	APIAddr          string
	ProxyAddr        string
	DataDir          string
	BodyLimitBytes   int64
	InterceptTimeout time.Duration
}

func Load() Config {
	cfg := Config{
		APIAddr:          "127.0.0.1:9080",
		ProxyAddr:        "127.0.0.1:8080",
		DataDir:          defaultDataDir(),
		BodyLimitBytes:   1048576,
		InterceptTimeout: 120 * time.Second,
	}
	if v := os.Getenv("BC_API_ADDR"); v != "" {
		cfg.APIAddr = v
	}
	if v := os.Getenv("BC_PROXY_ADDR"); v != "" {
		cfg.ProxyAddr = v
	}
	if v := os.Getenv("BC_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := os.Getenv("BC_BODY_LIMIT_BYTES"); v != "" {
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil && parsed > 0 {
			cfg.BodyLimitBytes = parsed
		}
	}
	return cfg
}

func defaultDataDir() string {
	if v, err := os.UserConfigDir(); err == nil && v != "" {
		return filepath.Join(v, "burpsuite-clone")
	}
	return ".burpsuite-clone"
}
```

- [ ] **Step 4: Add entrypoint**

Create `cmd/proxy/main.go`:

```go
package main

import (
	"fmt"

	"github.com/lutzifer/burpsuite-clone/internal/config"
)

func main() {
	cfg := config.Load()
	fmt.Printf("api=%s proxy=%s data=%s\n", cfg.APIAddr, cfg.ProxyAddr, cfg.DataDir)
}
```

- [ ] **Step 5: Add repository maintenance files**

Create `.gitignore`:

```gitignore
.DS_Store
bin/
dist/
coverage/
*.test
*.db
*.sqlite
*.sqlite3
*.pem
*.key
*.crt
.burpsuite-clone/
projects/
web/node_modules/
web/dist/
```

Create `README.md`:

```markdown
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
```

Create `SECURITY.md`:

```markdown
# Security Policy

## Supported Versions

Only the current `main` branch is supported during early development.

## Sensitive Data

Proxy captures can contain credentials, tokens, cookies, and private application data. Do not commit project databases, body files, CA private keys, or captured traffic.

## Reporting Vulnerabilities

Open a private security advisory or contact the maintainer before publishing details.
```

Create `CONTRIBUTING.md`:

```markdown
# Contributing

Run `go test ./...` before submitting backend changes. Frontend changes must pass `npm test` and `npm run build` from `web/` once the frontend is added.

Keep captured traffic, local databases, and certificates out of commits.
```

Create `LICENSE` with MIT license text and copyright holder `lutzifer25`.

- [ ] **Step 6: Run foundation checks**

Run: `go test ./...`

Expected: PASS.

Run: `go run ./cmd/proxy`

Expected output contains `api=127.0.0.1:9080 proxy=127.0.0.1:8080`.

- [ ] **Step 7: Commit**

```bash
git add go.mod cmd internal .gitignore README.md SECURITY.md CONTRIBUTING.md LICENSE
git commit -m "chore: scaffold local proxy application"
```

---

### Task 2: SQLite Store And Domain Types

**Files:**
- Create: `internal/store/store.go`
- Create: `internal/store/migrations.go`
- Create: `internal/store/sqlite.go`
- Create: `internal/store/sqlite_test.go`

**Interfaces:**
- Consumes: `config.Config`
- Produces: `store.Exchange`
- Produces: `store.RequestData`
- Produces: `store.ResponseData`
- Produces: `store.OpenSQLite(path string) (*SQLiteStore, error)`
- Produces: `(*SQLiteStore).SaveExchange(ctx context.Context, exchange *Exchange) error`
- Produces: `(*SQLiteStore).ListHistory(ctx context.Context, filter HistoryFilter) ([]HistoryItem, error)`
- Produces: `(*SQLiteStore).GetExchange(ctx context.Context, id int64) (*Exchange, error)`

- [ ] **Step 1: Write failing storage test**

Create `internal/store/sqlite_test.go`:

```go
package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteStoreSavesAndListsExchange(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "project.sqlite")
	st, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ex := &Exchange{
		Method:    "GET",
		Scheme:    "https",
		Host:      "example.test",
		Path:      "/login",
		Query:     "next=/app",
		Status:    200,
		MIMEType:  "text/html",
		StartedAt: time.Unix(1700000000, 0).UTC(),
		Duration:  25 * time.Millisecond,
		Request: RequestData{
			Headers: map[string][]string{"User-Agent": {"test"}},
			Body:    []byte("request-body"),
		},
		Response: ResponseData{
			Headers: map[string][]string{"Content-Type": {"text/html"}},
			Body:    []byte("response-body"),
		},
	}

	if err := st.SaveExchange(context.Background(), ex); err != nil {
		t.Fatal(err)
	}
	if ex.ID == 0 {
		t.Fatal("expected generated id")
	}

	items, err := st.ListHistory(context.Background(), HistoryFilter{Search: "example"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d", len(items))
	}
	if items[0].Host != "example.test" || items[0].Status != 200 {
		t.Fatalf("unexpected item: %+v", items[0])
	}

	loaded, err := st.GetExchange(context.Background(), ex.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(loaded.Response.Body) != "response-body" {
		t.Fatalf("response body = %q", loaded.Response.Body)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store`

Expected: FAIL because store types are undefined.

- [ ] **Step 3: Define store types**

Create `internal/store/store.go` with:

```go
package store

import (
	"context"
	"time"
)

type Exchange struct {
	ID                int64
	Method            string
	Scheme            string
	Host              string
	Path              string
	Query             string
	Status            int
	MIMEType          string
	RequestSize       int64
	ResponseSize      int64
	Duration          time.Duration
	StartedAt         time.Time
	Intercepted       bool
	Error             bool
	ErrorMessage      string
	RequestTruncated  bool
	ResponseTruncated bool
	Request           RequestData
	Response          ResponseData
	Tags              []string
	Note              string
}

type RequestData struct {
	Headers map[string][]string
	Body    []byte
	Raw     []byte
}

type ResponseData struct {
	Headers map[string][]string
	Body    []byte
	Raw     []byte
}

type HistoryItem struct {
	ID           int64     `json:"id"`
	Method       string    `json:"method"`
	Scheme       string    `json:"scheme"`
	Host         string    `json:"host"`
	Path         string    `json:"path"`
	Query        string    `json:"query"`
	Status       int       `json:"status"`
	MIMEType     string    `json:"mimeType"`
	RequestSize  int64     `json:"requestSize"`
	ResponseSize int64     `json:"responseSize"`
	DurationMS   int64     `json:"durationMs"`
	StartedAt    time.Time `json:"startedAt"`
	Intercepted  bool      `json:"intercepted"`
	Error        bool      `json:"error"`
}

type HistoryFilter struct {
	Search string
	Method string
	Host   string
}

type Store interface {
	SaveExchange(ctx context.Context, exchange *Exchange) error
	ListHistory(ctx context.Context, filter HistoryFilter) ([]HistoryItem, error)
	GetExchange(ctx context.Context, id int64) (*Exchange, error)
	Close() error
}
```

- [ ] **Step 4: Implement migrations and SQLite store**

Create `internal/store/migrations.go` with a `schemaSQL` string containing `exchanges` and `exchange_bodies` tables. Create `internal/store/sqlite.go` using `database/sql` and `_ "modernc.org/sqlite"`. Store headers as JSON. Store captured bodies as BLOBs capped by `BodyLimitBytes`; set `RequestTruncated` and `ResponseTruncated` when the captured body was cut at the configured limit.

Required signatures:

```go
func OpenSQLite(path string) (*SQLiteStore, error)
func (s *SQLiteStore) SaveExchange(ctx context.Context, exchange *Exchange) error
func (s *SQLiteStore) ListHistory(ctx context.Context, filter HistoryFilter) ([]HistoryItem, error)
func (s *SQLiteStore) GetExchange(ctx context.Context, id int64) (*Exchange, error)
func (s *SQLiteStore) Close() error
```

- [ ] **Step 5: Add SQLite dependency and run tests**

Run: `go get modernc.org/sqlite`

Run: `go test ./internal/store`

Expected: PASS.

- [ ] **Step 6: Run full backend tests**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/store
git commit -m "feat: add sqlite project store"
```

---

### Task 3: Local Certificate Authority

**Files:**
- Create: `internal/certs/authority.go`
- Create: `internal/certs/authority_test.go`

**Interfaces:**
- Produces: `certs.Authority`
- Produces: `certs.LoadOrCreateAuthority(dir string) (*Authority, error)`
- Produces: `(*Authority).FingerprintSHA256() string`
- Produces: `(*Authority).CACertPEM() []byte`
- Produces: `(*Authority).CertificateForHost(host string) (tls.Certificate, error)`

- [ ] **Step 1: Write failing certificate tests**

Create `internal/certs/authority_test.go`:

```go
package certs

import (
	"crypto/x509"
	"encoding/pem"
	"testing"
)

func TestLoadOrCreateAuthorityPersistsCA(t *testing.T) {
	dir := t.TempDir()
	a1, err := LoadOrCreateAuthority(dir)
	if err != nil {
		t.Fatal(err)
	}
	a2, err := LoadOrCreateAuthority(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a1.FingerprintSHA256() != a2.FingerprintSHA256() {
		t.Fatalf("fingerprints differ: %s != %s", a1.FingerprintSHA256(), a2.FingerprintSHA256())
	}
	block, _ := pem.Decode(a1.CACertPEM())
	if block == nil {
		t.Fatal("missing PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if !cert.IsCA {
		t.Fatal("certificate is not a CA")
	}
}

func TestCertificateForHostContainsDNSName(t *testing.T) {
	a, err := LoadOrCreateAuthority(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := a.CertificateForHost("app.example.test")
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(leaf.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(cert.DNSNames) != 1 || cert.DNSNames[0] != "app.example.test" {
		t.Fatalf("DNSNames = %#v", cert.DNSNames)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/certs`

Expected: FAIL because certificate authority functions are undefined.

- [ ] **Step 3: Implement CA persistence and leaf certificates**

Create `internal/certs/authority.go` using `crypto/rsa`, `crypto/x509`, `encoding/pem`, and `crypto/tls`. Persist files as `ca.pem` and `ca.key` inside the provided directory with file mode `0600` for the key. Cache generated host certificates in memory.

Required exported methods:

```go
type Authority struct {
	// private fields only
}

func LoadOrCreateAuthority(dir string) (*Authority, error)
func (a *Authority) FingerprintSHA256() string
func (a *Authority) CACertPEM() []byte
func (a *Authority) CertificateForHost(host string) (tls.Certificate, error)
```

- [ ] **Step 4: Run certificate tests**

Run: `go test ./internal/certs`

Expected: PASS.

- [ ] **Step 5: Run full backend tests**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/certs
git commit -m "feat: add local certificate authority"
```

---

### Task 4: Intercept Rules And Queue

**Files:**
- Create: `internal/intercept/rules.go`
- Create: `internal/intercept/queue.go`
- Create: `internal/intercept/rules_test.go`

**Interfaces:**
- Produces: `intercept.Rule`
- Produces: `intercept.Decision`
- Produces: `intercept.Matches(rule Rule, req MatchRequest) bool`
- Produces: `intercept.Queue`
- Produces: `(*Queue).Enqueue(ctx context.Context, item Item) (Decision, error)`
- Produces: `(*Queue).Forward(id string, edited RequestEdit) error`
- Produces: `(*Queue).Drop(id string) error`
- Produces: `(*Queue).List() []Item`

- [ ] **Step 1: Write failing intercept tests**

Create `internal/intercept/rules_test.go`:

```go
package intercept

import (
	"context"
	"testing"
	"time"
)

func TestMatchesHostPathAndMethod(t *testing.T) {
	rule := Rule{Enabled: true, Method: "POST", HostContains: "example", PathContains: "/login"}
	req := MatchRequest{Method: "POST", Host: "app.example.test", Path: "/login"}
	if !Matches(rule, req) {
		t.Fatal("expected rule to match")
	}
	req.Path = "/profile"
	if Matches(rule, req) {
		t.Fatal("expected rule not to match")
	}
}

func TestQueueForwardReturnsEditedRequest(t *testing.T) {
	q := NewQueue(2 * time.Second)
	done := make(chan Decision, 1)
	go func() {
		decision, err := q.Enqueue(context.Background(), Item{
			ID:     "one",
			Method: "GET",
			URL:    "https://example.test/",
			Headers: map[string][]string{
				"User-Agent": {"test"},
			},
		})
		if err != nil {
			t.Error(err)
			return
		}
		done <- decision
	}()

	waitForQueueLength(t, q, 1)
	if err := q.Forward("one", RequestEdit{Body: []byte("edited")}); err != nil {
		t.Fatal(err)
	}

	decision := <-done
	if decision.Action != ActionForward {
		t.Fatalf("Action = %s", decision.Action)
	}
	if string(decision.Edit.Body) != "edited" {
		t.Fatalf("Body = %q", decision.Edit.Body)
	}
}

func waitForQueueLength(t *testing.T, q *Queue, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(q.List()) == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("queue length did not reach %d", want)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/intercept`

Expected: FAIL because intercept types are undefined.

- [ ] **Step 3: Implement rule matcher**

Create `internal/intercept/rules.go` with exact action constants:

```go
package intercept

import "strings"

type Rule struct {
	Enabled      bool   `json:"enabled"`
	Method       string `json:"method"`
	HostContains string `json:"hostContains"`
	PathContains string `json:"pathContains"`
	MIMEContains string `json:"mimeContains"`
}

type MatchRequest struct {
	Method string
	Host   string
	Path   string
	MIME   string
}

func Matches(rule Rule, req MatchRequest) bool {
	if !rule.Enabled {
		return false
	}
	if rule.Method != "" && !strings.EqualFold(rule.Method, req.Method) {
		return false
	}
	if rule.HostContains != "" && !strings.Contains(strings.ToLower(req.Host), strings.ToLower(rule.HostContains)) {
		return false
	}
	if rule.PathContains != "" && !strings.Contains(strings.ToLower(req.Path), strings.ToLower(rule.PathContains)) {
		return false
	}
	if rule.MIMEContains != "" && !strings.Contains(strings.ToLower(req.MIME), strings.ToLower(rule.MIMEContains)) {
		return false
	}
	return true
}
```

- [ ] **Step 4: Implement queue**

Create `internal/intercept/queue.go` with a mutex-protected map of pending items. `Enqueue` stores an item, blocks until `Forward`, `Drop`, context cancellation, or timeout, then removes it from the queue.

Required types:

```go
type Action string

const (
	ActionForward Action = "forward"
	ActionDrop    Action = "drop"
)

type Item struct {
	ID      string              `json:"id"`
	Method  string              `json:"method"`
	URL     string              `json:"url"`
	Headers map[string][]string `json:"headers"`
	Body    []byte              `json:"body"`
}

type RequestEdit struct {
	Method  string              `json:"method"`
	URL     string              `json:"url"`
	Headers map[string][]string `json:"headers"`
	Body    []byte              `json:"body"`
}

type Decision struct {
	Action Action
	Edit   RequestEdit
}
```

- [ ] **Step 5: Run intercept tests**

Run: `go test ./internal/intercept`

Expected: PASS.

- [ ] **Step 6: Run full backend tests**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/intercept
git commit -m "feat: add intercept rules and queue"
```

---

### Task 5: HTTP Proxy Capture

**Files:**
- Create: `internal/proxy/capture.go`
- Create: `internal/proxy/proxy.go`
- Create: `internal/proxy/proxy_test.go`

**Interfaces:**
- Consumes: `store.Store`
- Consumes: `store.Exchange`
- Produces: `proxy.Server`
- Produces: `proxy.NewServer(proxy.Config) *Server`
- Produces: `(*Server).Serve(listener net.Listener) error`

- [ ] **Step 1: Write failing HTTP proxy test**

Create `internal/proxy/proxy_test.go`:

```go
package proxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lutzifer/burpsuite-clone/internal/store"
)

func TestHTTPProxyCapturesExchange(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("target response"))
	}))
	defer target.Close()

	mem := &memoryStore{}
	srv := NewServer(Config{Store: mem, BodyLimitBytes: 1024})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() { _ = srv.Serve(ln) }()

	client := &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyURL(mustParseURL(t, "http://"+ln.Addr().String())),
	}}
	resp, err := client.Get(target.URL + "/hello?x=1")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if string(body) != "target response" {
		t.Fatalf("body = %q", body)
	}
	if len(mem.saved) != 1 {
		t.Fatalf("saved exchanges = %d", len(mem.saved))
	}
	if mem.saved[0].Method != "GET" || mem.saved[0].Path != "/hello" || mem.saved[0].Status != 200 {
		t.Fatalf("exchange = %+v", mem.saved[0])
	}
}
```

Add test helpers in the same file: `mustParseURL` using `url.Parse`, and `memoryStore` implementing `store.Store`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/proxy`

Expected: FAIL because proxy server is undefined.

- [ ] **Step 3: Implement capture helper**

Create `internal/proxy/capture.go`:

```go
package proxy

import (
	"bytes"
	"io"
)

func readLimitedBody(body io.ReadCloser, limit int64) ([]byte, bool, error) {
	defer body.Close()
	var buf bytes.Buffer
	written, err := io.CopyN(&buf, body, limit+1)
	if err != nil && err != io.EOF {
		return nil, false, err
	}
	data := buf.Bytes()
	if written > limit {
		return data[:limit], true, nil
	}
	return data, false, nil
}
```

- [ ] **Step 4: Implement HTTP proxy**

Create `internal/proxy/proxy.go` with:

```go
type Config struct {
	Store          store.Store
	BodyLimitBytes int64
	Transport      http.RoundTripper
}

type Server struct {
	cfg Config
}

func NewServer(cfg Config) *Server
func (s *Server) Serve(listener net.Listener) error
```

Use `http.Server` with a handler that accepts absolute-form proxy requests. Forward using `http.Transport`, copy request headers, capture response headers and bodies, save a `store.Exchange`, and write the upstream response back to the client.

- [ ] **Step 5: Run HTTP proxy tests**

Run: `go test ./internal/proxy`

Expected: PASS.

- [ ] **Step 6: Run full backend tests**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/proxy
git commit -m "feat: capture http proxy traffic"
```

---

### Task 6: HTTPS MITM Proxy

**Files:**
- Modify: `internal/proxy/proxy.go`
- Modify: `internal/proxy/proxy_test.go`

**Interfaces:**
- Consumes: `certs.Authority`
- Modifies: `proxy.Config` with `Authority *certs.Authority`
- Produces: HTTPS `CONNECT` interception and capture

- [ ] **Step 1: Add failing HTTPS MITM test**

Add to `internal/proxy/proxy_test.go`:

```go
func TestHTTPSProxyMITMCapturesExchange(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer target.Close()

	authority, err := certs.LoadOrCreateAuthority(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mem := &memoryStore{}
	srv := NewServer(Config{Store: mem, BodyLimitBytes: 2048, Authority: authority})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() { _ = srv.Serve(ln) }()

	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(authority.CACertPEM()) {
		t.Fatal("failed to trust test CA")
	}
	client := &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyURL(mustParseURL(t, "http://"+ln.Addr().String())),
		TLSClientConfig: &tls.Config{
			RootCAs: roots,
		},
	}}
	resp, err := client.Get(strings.Replace(target.URL, "127.0.0.1", "localhost", 1) + "/secure")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if string(body) != `{"ok":true}` {
		t.Fatalf("body = %q", body)
	}
	if len(mem.saved) != 1 {
		t.Fatalf("saved exchanges = %d", len(mem.saved))
	}
	if mem.saved[0].Scheme != "https" || mem.saved[0].Path != "/secure" {
		t.Fatalf("exchange = %+v", mem.saved[0])
	}
}
```

Add imports: `crypto/tls`, `crypto/x509`, `strings`, and `github.com/lutzifer/burpsuite-clone/internal/certs`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/proxy -run TestHTTPSProxyMITMCapturesExchange`

Expected: FAIL because `Config.Authority` and `CONNECT` MITM are unsupported.

- [ ] **Step 3: Implement CONNECT MITM**

Update `internal/proxy/proxy.go`:

- Detect `r.Method == http.MethodConnect`.
- Hijack the client connection.
- Write `HTTP/1.1 200 Connection Established\r\n\r\n`.
- Wrap client connection in `tls.Server` using `Authority.CertificateForHost(hostWithoutPort)`.
- Read the inner HTTP request with `http.ReadRequest`.
- Set `inner.URL.Scheme = "https"` and `inner.URL.Host = connectHost`.
- Forward upstream with a transport using default TLS verification.
- Capture and store the exchange.
- Write the response back over the TLS client connection.

- [ ] **Step 4: Run HTTPS proxy tests**

Run: `go test ./internal/proxy`

Expected: PASS.

- [ ] **Step 5: Run full backend tests**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/proxy
git commit -m "feat: intercept https proxy traffic"
```

---

### Task 7: Backend API And Events

**Files:**
- Create: `internal/events/hub.go`
- Create: `internal/api/server.go`
- Create: `internal/api/handlers.go`
- Create: `internal/api/server_test.go`
- Modify: `cmd/proxy/main.go`

**Interfaces:**
- Consumes: `store.Store`
- Consumes: `certs.Authority`
- Consumes: `config.Config`
- Produces: `api.Server`
- Produces: `api.NewServer(api.Config) *Server`
- Produces: REST endpoints `GET /api/status`, `GET /api/ca.pem`, `GET /api/history`, `GET /api/history/{id}`
- Produces: WebSocket endpoint `GET /api/events`

- [ ] **Step 1: Write failing API test**

Create `internal/api/server_test.go`:

```go
package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lutzifer/burpsuite-clone/internal/certs"
	"github.com/lutzifer/burpsuite-clone/internal/store"
)

func TestStatusAndCADownload(t *testing.T) {
	authority, err := certs.LoadOrCreateAuthority(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(Config{
		Store:     store.NewMemoryForTests(),
		Authority: authority,
		APIAddr:   "127.0.0.1:9080",
		ProxyAddr: "127.0.0.1:8080",
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status code = %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp, err = http.Get(ts.URL + "/api/ca.pem")
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/x-pem-file" {
		t.Fatalf("Content-Type = %q", got)
	}
	_ = resp.Body.Close()
}
```

Create `internal/store/memory.go` with `func NewMemoryForTests() Store`. The memory store should use a mutex, assign increasing IDs, keep `Exchange` values in a slice, implement `SaveExchange`, `ListHistory`, `GetExchange`, and return nil from `Close`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api`

Expected: FAIL because API server and memory test store are undefined.

- [ ] **Step 3: Implement event hub**

Create `internal/events/hub.go` with:

```go
package events

import "sync"

type Event struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

type Hub struct {
	mu          sync.Mutex
	subscribers map[chan Event]struct{}
}

func NewHub() *Hub
func (h *Hub) Subscribe() (chan Event, func())
func (h *Hub) Publish(event Event)
```

- [ ] **Step 4: Implement API handlers**

Create `internal/api/server.go` and `internal/api/handlers.go` with:

```go
type Config struct {
	Store     store.Store
	Authority *certs.Authority
	Events    *events.Hub
	APIAddr   string
	ProxyAddr string
}

type Server struct {
	cfg Config
	mux *http.ServeMux
}

func NewServer(cfg Config) *Server
func (s *Server) Handler() http.Handler
```

Implement:

- `GET /api/status`: JSON with API address, proxy address, CA fingerprint, and `httpsInterception: true`.
- `GET /api/ca.pem`: PEM body with `Content-Type: application/x-pem-file`.
- `GET /api/history`: JSON list from `Store.ListHistory`.
- `GET /api/history/{id}`: JSON exchange from `Store.GetExchange`.
- `GET /api/events`: WebSocket stream using `gorilla/websocket`.

- [ ] **Step 5: Add dependencies and run API tests**

Run: `go get github.com/gorilla/websocket`

Run: `go test ./internal/api`

Expected: PASS.

- [ ] **Step 6: Wire command**

Update `cmd/proxy/main.go` to:

- Load config.
- Create data directory.
- Open SQLite database at `<DataDir>/project.sqlite`.
- Load or create CA at `<DataDir>/ca`.
- Create event hub.
- Start proxy listener in a goroutine.
- Start API server in the main goroutine.

- [ ] **Step 7: Run full backend tests**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add cmd internal/api internal/events internal/store go.mod go.sum
git commit -m "feat: expose local api and events"
```

---

### Task 8: Repeater Backend

**Files:**
- Create: `internal/repeater/repeater.go`
- Create: `internal/repeater/repeater_test.go`
- Modify: `internal/api/handlers.go`
- Modify: `internal/api/server.go`
- Modify: `internal/store/store.go`
- Modify: `internal/store/sqlite.go`
- Modify: `internal/store/sqlite_test.go`

**Interfaces:**
- Produces: `repeater.Service`
- Produces: `repeater.SendRequest`
- Produces: `repeater.SendResult`
- Produces: `(*Service).Send(ctx context.Context, req SendRequest) (SendResult, error)`
- Produces: API endpoint `POST /api/repeater/sessions/{id}/send`

- [ ] **Step 1: Write failing Repeater test**

Create `internal/repeater/repeater_test.go`:

```go
package repeater

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSendReturnsCapturedResponse(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test") != "yes" {
			t.Fatalf("X-Test = %q", r.Header.Get("X-Test"))
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("repeater response"))
	}))
	defer target.Close()

	service := NewService(http.DefaultTransport, 2048)
	result, err := service.Send(context.Background(), SendRequest{
		Method: "POST",
		URL:    target.URL + "/repeat",
		Headers: map[string][]string{
			"X-Test": {"yes"},
		},
		Body: []byte("request"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != 200 {
		t.Fatalf("Status = %d", result.Status)
	}
	if string(result.Body) != "repeater response" {
		t.Fatalf("Body = %q", result.Body)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/repeater`

Expected: FAIL because Repeater service is undefined.

- [ ] **Step 3: Implement Repeater service**

Create `internal/repeater/repeater.go` with:

```go
type SendRequest struct {
	Method  string              `json:"method"`
	URL     string              `json:"url"`
	Headers map[string][]string `json:"headers"`
	Body    []byte              `json:"body"`
}

type SendResult struct {
	Status       int                 `json:"status"`
	Headers      map[string][]string `json:"headers"`
	Body         []byte              `json:"body"`
	DurationMS   int64               `json:"durationMs"`
	Size         int64               `json:"size"`
	Truncated    bool                `json:"truncated"`
	ContentType  string              `json:"contentType"`
}

func NewService(transport http.RoundTripper, bodyLimitBytes int64) *Service
func (s *Service) Send(ctx context.Context, req SendRequest) (SendResult, error)
```

- [ ] **Step 4: Add Repeater API route**

Update API config with `Repeater *repeater.Service`. Add `POST /api/repeater/sessions/{id}/send` that decodes `SendRequest`, calls `Repeater.Send`, returns `SendResult`, and publishes `repeater.send.completed`.

- [ ] **Step 5: Run backend tests**

Run: `go test ./internal/repeater ./internal/api`

Expected: PASS.

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/repeater internal/api internal/store
git commit -m "feat: add repeater backend"
```

---

### Task 9: Frontend Foundation

**Files:**
- Create: `web/package.json`
- Create: `web/vite.config.ts`
- Create: `web/tsconfig.json`
- Create: `web/index.html`
- Create: `web/src/main.tsx`
- Create: `web/src/types.ts`
- Create: `web/src/api/client.ts`
- Create: `web/src/api/events.ts`
- Create: `web/src/App.tsx`
- Create: `web/src/components/StatusBar.tsx`
- Create: `web/src/styles.css`
- Create: `web/src/App.test.tsx`

**Interfaces:**
- Consumes: API DTOs from Task 7
- Produces: frontend dev command `npm run dev`
- Produces: frontend tests `npm test`
- Produces: frontend build `npm run build`

- [ ] **Step 1: Create frontend package**

Create `web/package.json`:

```json
{
  "name": "burpsuite-clone-web",
  "private": true,
  "version": "0.1.0",
  "type": "module",
  "scripts": {
    "dev": "vite --host 127.0.0.1",
    "build": "tsc --noEmit && vite build",
    "test": "vitest run",
    "test:watch": "vitest"
  },
  "dependencies": {
    "@vitejs/plugin-react": "^5.0.0",
    "vite": "^7.0.0",
    "typescript": "^5.8.0",
    "react": "^19.0.0",
    "react-dom": "^19.0.0",
    "lucide-react": "^0.475.0"
  },
  "devDependencies": {
    "@testing-library/jest-dom": "^6.6.0",
    "@testing-library/react": "^16.2.0",
    "@testing-library/user-event": "^14.6.0",
    "jsdom": "^26.0.0",
    "vitest": "^3.0.0"
  }
}
```

- [ ] **Step 2: Write failing UI smoke test**

Create `web/src/App.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { App } from './App';

test('renders operator shell status', () => {
  render(<App />);
  expect(screen.getByText('Proxy')).toBeInTheDocument();
  expect(screen.getByText('History')).toBeInTheDocument();
  expect(screen.getByText('Repeater')).toBeInTheDocument();
});
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd web && npm install && npm test`

Expected: FAIL because `App` is undefined.

- [ ] **Step 4: Implement UI shell**

Create Vite, TypeScript, React entry files. `App.tsx` should render a dense app shell with left navigation, top status bar, main history region, inspector region, and right utility panel. Use compact controls, icons from `lucide-react`, and no marketing hero.

Create `web/src/components/StatusBar.tsx` with visible labels: `Proxy`, `CA`, `Project`, `Intercept`.

- [ ] **Step 5: Add API clients**

Create `web/src/types.ts` with `StatusDTO`, `HistoryItem`, `Exchange`, `SendRequest`, `SendResult`, and `InterceptItem` matching backend JSON names. Create `api/client.ts` with `getStatus`, `getHistory`, `getExchange`, `sendRepeater`, `forwardIntercept`, and `dropIntercept`.

- [ ] **Step 6: Run frontend checks**

Run: `cd web && npm test`

Expected: PASS.

Run: `cd web && npm run build`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web
git commit -m "feat: scaffold web operator shell"
```

---

### Task 10: History And Inspector UI

**Files:**
- Create: `web/src/components/HistoryTable.tsx`
- Create: `web/src/components/Inspector.tsx`
- Modify: `web/src/App.tsx`
- Modify: `web/src/App.test.tsx`
- Modify: `web/src/styles.css`

**Interfaces:**
- Consumes: `HistoryItem`
- Consumes: `Exchange`
- Produces: selectable history table and inspector tabs

- [ ] **Step 1: Add failing UI workflow test**

Update `web/src/App.test.tsx`:

```tsx
test('shows history columns and inspector tabs', () => {
  render(<App />);
  expect(screen.getByText('Method')).toBeInTheDocument();
  expect(screen.getByText('Host')).toBeInTheDocument();
  expect(screen.getByText('Status')).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: 'Headers' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: 'Body' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: 'Raw' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: 'Cookies' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: 'Query' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: 'Timing' })).toBeInTheDocument();
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npm test`

Expected: FAIL because history and inspector components are missing.

- [ ] **Step 3: Implement history table**

Create `HistoryTable.tsx` accepting:

```tsx
type HistoryTableProps = {
  items: HistoryItem[];
  selectedId: number | null;
  onSelect: (id: number) => void;
  onSendToRepeater: (id: number) => void;
};
```

Render columns: Method, Host, Path, Status, MIME, Size, Duration, Time.

- [ ] **Step 4: Implement inspector tabs**

Create `Inspector.tsx` accepting:

```tsx
type InspectorProps = {
  exchange: Exchange | null;
};
```

Render tabs: Headers, Body, Raw, Cookies, Query, Timing. Show an empty state when no exchange is selected.

- [ ] **Step 5: Wire components into App**

Use local sample history data for this task. Keep component props compatible with the API types that Task 12 will load from the backend.

- [ ] **Step 6: Run frontend checks**

Run: `cd web && npm test`

Expected: PASS.

Run: `cd web && npm run build`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web/src
git commit -m "feat: add history and inspector ui"
```

---

### Task 11: Intercept And Repeater UI

**Files:**
- Create: `web/src/components/InterceptPanel.tsx`
- Create: `web/src/components/Repeater.tsx`
- Modify: `web/src/App.tsx`
- Modify: `web/src/App.test.tsx`
- Modify: `web/src/styles.css`

**Interfaces:**
- Consumes: `InterceptItem`
- Consumes: `SendRequest`
- Consumes: `SendResult`
- Produces: intercept queue controls and Repeater editing surface

- [ ] **Step 1: Add failing test for controls**

Update `web/src/App.test.tsx`:

```tsx
test('shows intercept and repeater controls', () => {
  render(<App />);
  expect(screen.getByText('Intercept Queue')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Forward' })).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Drop' })).toBeInTheDocument();
  expect(screen.getByText('Request Editor')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Send' })).toBeInTheDocument();
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npm test`

Expected: FAIL because Intercept and Repeater components are missing.

- [ ] **Step 3: Implement InterceptPanel**

Create `InterceptPanel.tsx` with:

```tsx
type InterceptPanelProps = {
  items: InterceptItem[];
  onForward: (id: string) => void;
  onDrop: (id: string) => void;
};
```

Show method, URL, headers count, body size, Forward button, and Drop button.

- [ ] **Step 4: Implement Repeater**

Create `Repeater.tsx` with editable method, URL, headers text area, body text area, Send button, response status, response headers, response body, duration, and size.

- [ ] **Step 5: Wire components into App**

Use local component state for a single Repeater tab and an empty intercept queue. Keep callback names aligned with `api/client.ts`.

- [ ] **Step 6: Run frontend checks**

Run: `cd web && npm test`

Expected: PASS.

Run: `cd web && npm run build`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web/src
git commit -m "feat: add intercept and repeater ui"
```

---

### Task 12: Settings UI And Backend Integration

**Files:**
- Create: `web/src/components/Settings.tsx`
- Modify: `web/src/App.tsx`
- Modify: `web/src/api/client.ts`
- Modify: `web/src/api/events.ts`
- Modify: `web/src/App.test.tsx`
- Modify: `internal/api/handlers.go`

**Interfaces:**
- Consumes: `GET /api/status`
- Consumes: `GET /api/history`
- Consumes: `GET /api/events`
- Produces: live UI status and history updates
- Produces: CA download link `/api/ca.pem`

- [ ] **Step 1: Add failing integration-oriented UI test**

Update `web/src/App.test.tsx` with mocked `fetch`:

```tsx
test('loads status from api', async () => {
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    if (url.endsWith('/api/status')) {
      return new Response(JSON.stringify({
        apiAddr: '127.0.0.1:9080',
        proxyAddr: '127.0.0.1:8080',
        caFingerprint: 'AA:BB',
        httpsInterception: true
      }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    }
    if (url.endsWith('/api/history')) {
      return new Response(JSON.stringify([]), { status: 200 });
    }
    return new Response('{}', { status: 404 });
  }));
  render(<App />);
  expect(await screen.findByText('127.0.0.1:8080')).toBeInTheDocument();
  vi.unstubAllGlobals();
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npm test`

Expected: FAIL because App does not load API status.

- [ ] **Step 3: Implement status and history loading**

Update `App.tsx` to call `getStatus()` and `getHistory()` on mount. Render API errors in a compact status region. Keep sample data only as a fallback for tests that do not mock API calls.

- [ ] **Step 4: Implement Settings**

Create `Settings.tsx` showing API address, proxy address, CA fingerprint, HTTPS interception flag, and a CA download button linking to `/api/ca.pem`.

- [ ] **Step 5: Wire WebSocket events**

Update `api/events.ts`:

```ts
export function connectEvents(onEvent: (event: ServerEvent) => void): () => void
```

Use `ws://` or `wss://` based on current page protocol. Re-fetch history on `history.entry.created` and update status on `proxy.status.changed`.

- [ ] **Step 6: Run frontend checks**

Run: `cd web && npm test`

Expected: PASS.

Run: `cd web && npm run build`

Expected: PASS.

- [ ] **Step 7: Run full backend and frontend checks**

Run: `go test ./...`

Expected: PASS.

Run: `cd web && npm run build`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/api web/src
git commit -m "feat: connect web ui to local api"
```

---

### Task 13: Documentation, CI, And Example Target

**Files:**
- Create: `.github/workflows/ci.yml`
- Create: `.github/ISSUE_TEMPLATE/bug_report.md`
- Create: `.github/ISSUE_TEMPLATE/feature_request.md`
- Create: `.github/pull_request_template.md`
- Create: `docs/setup/ca-trust.md`
- Create: `examples/targets/go.mod`
- Create: `examples/targets/main.go`
- Modify: `README.md`
- Modify: `CONTRIBUTING.md`
- Modify: `SECURITY.md`

**Interfaces:**
- Produces: CI workflow for backend and frontend checks
- Produces: local target server for manual proxy testing
- Produces: complete setup documentation

- [ ] **Step 1: Create CI workflow**

Create `.github/workflows/ci.yml`:

```yaml
name: CI

on:
  pull_request:
  push:
    branches: [main]

jobs:
  backend:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.23'
      - run: go test ./...

  frontend:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: '22'
          cache: npm
          cache-dependency-path: web/package-lock.json
      - run: npm ci
        working-directory: web
      - run: npm test
        working-directory: web
      - run: npm run build
        working-directory: web
```

- [ ] **Step 2: Create issue and PR templates**

Create `.github/ISSUE_TEMPLATE/bug_report.md` with fields for version, OS, browser, proxy settings, expected behavior, actual behavior, logs with secrets removed, and reproduction steps.

Create `.github/ISSUE_TEMPLATE/feature_request.md` with fields for workflow, user value, constraints, and acceptance criteria.

Create `.github/pull_request_template.md` with checklist items for tests, docs, sensitive-data review, and screenshots for UI changes.

- [ ] **Step 3: Create example target**

Create `examples/targets/go.mod`:

```go
module github.com/lutzifer/burpsuite-clone/examples/targets

go 1.23
```

Create `examples/targets/main.go`:

```go
package main

import (
	"fmt"
	"log"
	"net/http"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"method":%q,"path":%q}`, r.Method, r.URL.Path)
	})
	log.Println("example target on http://127.0.0.1:9091")
	log.Fatal(http.ListenAndServe("127.0.0.1:9091", mux))
}
```

- [ ] **Step 4: Write CA trust documentation**

Create `docs/setup/ca-trust.md` with sections for macOS Keychain, Windows Certificate Manager, Linux NSS/system store, Firefox certificate manager, Chrome using system trust, and test device proxy configuration. Include a warning to remove the CA when testing is finished.

- [ ] **Step 5: Update README commands**

Update `README.md` with:

```markdown
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
```

- [ ] **Step 6: Run full verification**

Run: `go test ./...`

Expected: PASS.

Run: `cd web && npm test`

Expected: PASS.

Run: `cd web && npm run build`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add .github docs examples README.md CONTRIBUTING.md SECURITY.md
git commit -m "chore: add ci docs and example target"
```

---

## Final Verification

After all tasks are complete:

- [ ] Run `go test ./...`
- [ ] Run `cd web && npm test`
- [ ] Run `cd web && npm run build`
- [ ] Run `go run ./cmd/proxy`
- [ ] Open `http://127.0.0.1:9080`
- [ ] Configure a browser or HTTP client to use proxy `127.0.0.1:8080`
- [ ] Confirm plain HTTP traffic appears in History
- [ ] Download and trust the local CA
- [ ] Confirm HTTPS traffic appears in History
- [ ] Send a History request to Repeater
- [ ] Edit and resend the Repeater request
- [ ] Confirm project data persists after restart

## Spec Coverage Review

- Product goal: covered by Tasks 1, 5, 6, 7, 8, 9, 10, 11, and 12.
- Safety boundary: covered by Tasks 1, 3, 7, 12, and 13.
- Go proxy and API architecture: covered by Tasks 1, 5, 6, 7, and 8.
- React web UI architecture: covered by Tasks 9, 10, 11, and 12.
- HTTP proxying: covered by Task 5.
- HTTPS interception and CA: covered by Tasks 3 and 6.
- History and inspector: covered by Tasks 2, 7, 10, and 12.
- Intercept mode: covered by Tasks 4, 7, and 11.
- Repeater: covered by Tasks 8 and 11.
- SQLite project storage: covered by Task 2.
- GitHub-ready repository: covered by Tasks 1 and 13.
- Testing and CI: covered throughout each task and finalized by Task 13.
