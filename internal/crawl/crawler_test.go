package crawl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/repeater"
	"github.com/lutzifer/burpsuite-clone/internal/store"
)

type allowScope struct{ origin string }

func (s allowScope) Allows(raw string) bool { return strings.HasPrefix(raw, s.origin) }

func TestCrawlerDiscoversWithoutSubmitting(t *testing.T) {
	var methods []string
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		methods = append(methods, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<a href="/next">next</a><a href="https://elsewhere.test/out">out</a><form action="/submit" method="post"><input name="password" value="secret"></form>`))
		} else if r.URL.Path == "/next" {
			w.Write([]byte("ok"))
		}
	}))
	defer server.Close()
	s, err := store.OpenSQLite(filepath.Join(t.TempDir(), "crawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seed := &store.Exchange{Method: "GET", Scheme: "http", Host: strings.TrimPrefix(server.URL, "http://"), Path: "/", Query: "token=secret", Status: 200, InScope: true, StartedAt: time.Now()}
	if err := s.SaveExchange(context.Background(), seed); err != nil {
		t.Fatal(err)
	}
	c, err := New(s, s, allowScope{origin: server.URL}, repeater.NewHTTPSender(nil))
	if err != nil {
		t.Fatal(err)
	}
	report, err := c.Start(context.Background(), Request{HistoryID: seed.ID, MaxPages: 2, MaxDepth: 1, Acknowledge: true})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		run, err := s.GetCrawlRun(context.Background(), report.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if run.State != "running" {
			if run.State != "completed" || run.PageCount != 2 || len(run.Forms) != 1 || run.Forms[0].Fields[0].Name != "password" {
				t.Fatalf("run=%+v", run)
			}
			for _, page := range run.Pages {
				if strings.Contains(page.URL, "secret") {
					t.Fatalf("stored query value: %+v", page)
				}
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("crawl timed out")
		case <-time.After(20 * time.Millisecond):
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(methods) != 2 || methods[0] != "GET /" || methods[1] != "GET /next" {
		t.Fatalf("requests=%v", methods)
	}
}

func TestCrawlerCancelStopsBeforeNextPage(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<a href="/next">next</a>`))
	}))
	defer server.Close()
	s, err := store.OpenSQLite(filepath.Join(t.TempDir(), "crawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seed := &store.Exchange{Method: "GET", Scheme: "http", Host: strings.TrimPrefix(server.URL, "http://"), Path: "/", Status: 200, InScope: true, StartedAt: time.Now()}
	if err := s.SaveExchange(context.Background(), seed); err != nil {
		t.Fatal(err)
	}
	c, err := New(s, s, allowScope{origin: server.URL}, repeater.NewHTTPSender(nil))
	if err != nil {
		t.Fatal(err)
	}
	report, err := c.Start(context.Background(), Request{HistoryID: seed.ID, MaxPages: 2, MaxDepth: 1, Acknowledge: true})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(4 * time.Second)
	for {
		run, err := s.GetCrawlRun(context.Background(), report.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if run.PageCount == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("first page not fetched")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := c.Cancel(report.RunID); err != nil {
		t.Fatal(err)
	}
	for {
		run, err := s.GetCrawlRun(context.Background(), report.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if run.State != "running" {
			if run.State != "cancelled" {
				t.Fatalf("state=%s", run.State)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("crawl not cancelled")
		case <-time.After(10 * time.Millisecond):
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 1 {
		t.Fatalf("requests=%d", requests)
	}
}
