package activechecks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/repeater"
	"github.com/lutzifer/burpsuite-clone/internal/scancreds"
	"github.com/lutzifer/burpsuite-clone/internal/store"
)

type testScope struct{ origin string }

func (s testScope) Allows(raw string) bool { return strings.HasPrefix(raw, s.origin) }

type revocableScope struct {
	origin string
	denied atomic.Bool
}

func (s *revocableScope) Allows(raw string) bool {
	return !s.denied.Load() && strings.HasPrefix(raw, s.origin)
}

func TestServiceChecksDiscoveredGETInputs(t *testing.T) {
	var mu sync.Mutex
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if r.Header.Get("Cookie") != "sid=explicit" || r.Header.Get("Authorization") != "Bearer explicit" {
			t.Errorf("incorrect explicit session headers")
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<p>" + r.URL.Query().Get("q") + r.URL.Query().Get("term") + "</p>"))
	}))
	defer server.Close()
	s, err := store.OpenSQLite(filepath.Join(t.TempDir(), "checks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seed := &store.Exchange{Method: "GET", Scheme: "http", Host: strings.TrimPrefix(server.URL, "http://"), Path: "/", Status: 200, InScope: true, StartedAt: time.Now()}
	if err := s.SaveExchange(context.Background(), seed); err != nil {
		t.Fatal(err)
	}
	crawlID, err := s.CreateCrawlRun(context.Background(), seed.ID, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendCrawlPage(context.Background(), crawlID, store.CrawlPage{URL: server.URL + "/search?q=original-secret", Status: 200}, []store.CrawlForm{{PageURL: server.URL + "/search", ActionURL: server.URL + "/find", Method: "GET", Fields: []store.CrawlField{{Name: "term", Type: "text"}, {Name: "password", Type: "password"}}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishCrawlRun(context.Background(), crawlID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	service, err := New(s, s, s, testScope{server.URL}, repeater.NewHTTPSender(nil))
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Start(context.Background(), Request{CrawlID: crawlID, Acknowledge: true, Session: scancreds.Credentials{Cookie: "sid=explicit", Authorization: "Bearer explicit"}})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		run, err := s.GetActiveCheckRun(context.Background(), report.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if run.State != "running" {
			if run.State != "completed" || run.ObservationCount != 2 || !run.Observations[0].Found || run.Observations[0].Context != "html_text" {
				t.Fatalf("run=%+v", run)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("checks timed out")
		case <-time.After(20 * time.Millisecond):
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 || requests[0] != "GET /search" || requests[1] != "GET /find" {
		t.Fatalf("requests=%v", requests)
	}
}

func TestServiceOptionalRedirectCheckDoesNotFollowLocation(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if strings.Contains(r.URL.RawQuery, "original-secret") {
			t.Error("original query value was forwarded")
		}
		if next := r.URL.Query().Get("next"); strings.HasPrefix(next, "https://redirect-check.invalid/") {
			w.Header().Set("Location", next)
			w.WriteHeader(http.StatusFound)
		}
		if origin := r.Header.Get("Origin"); strings.HasSuffix(origin, ".cors-check.invalid") {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
	}))
	defer server.Close()
	s, err := store.OpenSQLite(filepath.Join(t.TempDir(), "checks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seed := &store.Exchange{Method: "GET", Scheme: "http", Host: strings.TrimPrefix(server.URL, "http://"), Path: "/", Status: 200, InScope: true, StartedAt: time.Now()}
	if err := s.SaveExchange(context.Background(), seed); err != nil {
		t.Fatal(err)
	}
	crawlID, err := s.CreateCrawlRun(context.Background(), seed.ID, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendCrawlPage(context.Background(), crawlID, store.CrawlPage{URL: server.URL + "/go?next=original-secret", QueryNames: []string{"next"}, Status: 200}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendCrawlPage(context.Background(), crawlID, store.CrawlPage{URL: server.URL + "/go?next=another-secret", QueryNames: []string{"next"}, Status: 200}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishCrawlRun(context.Background(), crawlID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	service, err := New(s, s, s, testScope{server.URL}, repeater.NewHTTPSender(nil))
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Start(context.Background(), Request{CrawlID: crawlID, Acknowledge: true, CheckRedirects: true, CheckCORS: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.MaximumRequests != 3 {
		t.Fatalf("maximum requests = %d", report.MaximumRequests)
	}
	deadline := time.After(5 * time.Second)
	for {
		run, err := s.GetActiveCheckRun(context.Background(), report.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if run.State != "running" {
			if run.State != "completed" || len(run.Observations) != 3 || run.Observations[1].Source != "redirect_query" || !run.Observations[1].Found || run.Observations[1].Context != "redirect_location" || run.Observations[2].Source != "cors" || !run.Observations[2].Found || run.Observations[2].Context != "cors_credentials" {
				t.Fatalf("run=%+v", run)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("redirect check timed out")
		case <-time.After(20 * time.Millisecond):
		}
	}
	if requests.Load() != 3 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestServiceStopsWhenScopeRevoked(t *testing.T) {
	var requests atomic.Int32
	scope := &revocableScope{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		scope.denied.Store(true)
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(r.URL.RawQuery))
	}))
	defer server.Close()
	scope.origin = server.URL
	s, err := store.OpenSQLite(filepath.Join(t.TempDir(), "checks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seed := &store.Exchange{Method: "GET", Scheme: "http", Host: strings.TrimPrefix(server.URL, "http://"), Path: "/", Status: 200, InScope: true, StartedAt: time.Now()}
	if err := s.SaveExchange(context.Background(), seed); err != nil {
		t.Fatal(err)
	}
	crawlID, err := s.CreateCrawlRun(context.Background(), seed.ID, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendCrawlPage(context.Background(), crawlID, store.CrawlPage{URL: server.URL + "/?a=1&b=2", Status: 200}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishCrawlRun(context.Background(), crawlID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	service, err := New(s, s, s, scope, repeater.NewHTTPSender(nil))
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Start(context.Background(), Request{CrawlID: crawlID, Acknowledge: true})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(4 * time.Second)
	for {
		run, err := s.GetActiveCheckRun(context.Background(), report.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if run.State != "running" {
			if run.State != "scope_revoked" || run.ObservationCount != 1 {
				t.Fatalf("run=%+v", run)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("scope stop timeout")
		case <-time.After(20 * time.Millisecond):
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("requests=%d", requests.Load())
	}
}

func TestServiceCancelStopsBeforeNextRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(r.URL.RawQuery))
	}))
	defer server.Close()
	s, err := store.OpenSQLite(filepath.Join(t.TempDir(), "checks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seed := &store.Exchange{Method: "GET", Scheme: "http", Host: strings.TrimPrefix(server.URL, "http://"), Path: "/", Status: 200, InScope: true, StartedAt: time.Now()}
	if err := s.SaveExchange(context.Background(), seed); err != nil {
		t.Fatal(err)
	}
	crawlID, err := s.CreateCrawlRun(context.Background(), seed.ID, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendCrawlPage(context.Background(), crawlID, store.CrawlPage{URL: server.URL + "/?a=1&b=2", Status: 200}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishCrawlRun(context.Background(), crawlID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	service, err := New(s, s, s, testScope{server.URL}, repeater.NewHTTPSender(nil))
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Start(context.Background(), Request{CrawlID: crawlID, Acknowledge: true})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(4 * time.Second)
	for {
		run, err := s.GetActiveCheckRun(context.Background(), report.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if run.ObservationCount == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("first request timeout")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := service.Cancel(report.RunID); err != nil {
		t.Fatal(err)
	}
	for {
		run, err := s.GetActiveCheckRun(context.Background(), report.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if run.State != "running" {
			if run.State != "cancelled" || run.ObservationCount != 1 {
				t.Fatalf("run=%+v", run)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("cancel timeout")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("requests=%d", requests.Load())
	}
}
