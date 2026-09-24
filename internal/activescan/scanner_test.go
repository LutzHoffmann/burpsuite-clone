package activescan

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lutzifer/burpsuite-clone/internal/repeater"
	"github.com/lutzifer/burpsuite-clone/internal/store"
)

type historyFake struct{ exchange *store.Exchange }

func (h historyFake) GetExchange(context.Context, int64) (*store.Exchange, error) {
	return h.exchange, nil
}

type scopeFake struct{ allowed atomic.Bool }

func (s *scopeFake) Allows(string) bool { return s.allowed.Load() }

type senderFake struct {
	calls   []repeater.SendRequest
	options []repeater.SendOptions
	onSend  func()
}

func (s *senderFake) Send(_ context.Context, request repeater.SendRequest, options repeater.SendOptions) (repeater.SendResult, error) {
	s.calls = append(s.calls, request)
	s.options = append(s.options, options)
	if s.onSend != nil {
		s.onSend()
	}
	u, _ := url.Parse(request.URL)
	for _, values := range u.Query() {
		return repeater.SendResult{Status: 200, Body: []byte("echo:" + values[0])}, nil
	}
	return repeater.SendResult{}, errors.New("no query")
}

func scanFixture(t *testing.T) (*Scanner, *senderFake, *scopeFake) {
	t.Helper()
	scope := &scopeFake{}
	scope.allowed.Store(true)
	sender := &senderFake{}
	scanner, err := New(historyFake{&store.Exchange{ID: 7, Method: "GET", Scheme: "https", Host: "example.test", Path: "/search", Query: "b=private&b=other&a=secret", InScope: true, Request: store.RequestData{Headers: map[string][]string{"Cookie": {"sid=secret"}, "Authorization": {"Bearer private"}}}}}, scope, sender)
	if err != nil {
		t.Fatal(err)
	}
	return scanner, sender, scope
}

func TestScannerSendsOnlyBoundedCredentialFreeMarkers(t *testing.T) {
	scanner, sender, _ := scanFixture(t)
	if _, err := scanner.Scan(context.Background(), Request{HistoryID: 7, MaxProbes: 5}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("without acknowledgement: %v", err)
	}
	if len(sender.calls) != 0 {
		t.Fatal("sent without acknowledgement")
	}
	report, err := scanner.Scan(context.Background(), Request{HistoryID: 7, MaxProbes: 5, Acknowledge: true})
	if err != nil || report.ProbeCount != 2 || !report.Probes[0].Reflected || !report.Probes[1].Reflected {
		t.Fatalf("report=%+v, %v", report, err)
	}
	for _, request := range sender.calls {
		if request.Method != "GET" || len(request.Headers) != 1 || request.Headers["User-Agent"][0] == "" || len(request.Body) != 0 {
			t.Fatalf("unsafe request=%+v", request)
		}
		parsed, _ := url.Parse(request.URL)
		if len(parsed.Query()) != 1 || strings.Contains(request.URL, "secret") || strings.Contains(request.URL, "private") {
			t.Fatalf("original values forwarded: %s", request.URL)
		}
	}
	for _, options := range sender.options {
		if options.BodyLimitBytes != 64<<10 {
			t.Fatalf("unbounded response=%+v", options)
		}
	}
}

func TestScannerStopsWhenScopeIsRevoked(t *testing.T) {
	scanner, sender, scope := scanFixture(t)
	sender.onSend = func() { scope.allowed.Store(false) }
	report, err := scanner.Scan(context.Background(), Request{HistoryID: 7, MaxProbes: 2, Acknowledge: true})
	if err != nil || report.ProbeCount != 1 || report.StoppedReason != "scope_revoked" || len(sender.calls) != 1 {
		t.Fatalf("report=%+v, %v", report, err)
	}
}

func TestScannerRejectsOutOfScopeAndNonGET(t *testing.T) {
	scanner, sender, scope := scanFixture(t)
	scope.allowed.Store(false)
	if _, err := scanner.Scan(context.Background(), Request{HistoryID: 7, MaxProbes: 1, Acknowledge: true}); !errors.Is(err, ErrScopeDenied) {
		t.Fatalf("scope error=%v", err)
	}
	scope.allowed.Store(true)
	scanner.history = historyFake{&store.Exchange{ID: 7, Method: "POST", Scheme: "https", Host: "example.test", Query: "x=1", InScope: true}}
	if _, err := scanner.Scan(context.Background(), Request{HistoryID: 7, MaxProbes: 1, Acknowledge: true}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("POST error=%v", err)
	}
	if len(sender.calls) != 0 {
		t.Fatal("unsafe traffic sent")
	}
}

func TestScannerLocalEndToEnd(t *testing.T) {
	var seen atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Add(1)
		if r.Method != "GET" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || len(r.URL.Query()) != 1 {
			t.Errorf("unexpected request: %+v", r)
		}
		_, _ = w.Write([]byte(r.URL.Query().Get("q")))
	}))
	defer server.Close()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	scope := &scopeFake{}
	scope.allowed.Store(true)
	scanner, err := New(historyFake{&store.Exchange{ID: 1, Method: "GET", Scheme: "http", Host: u.Host, Path: "/", Query: "q=old-secret", InScope: true}}, scope, repeater.NewHTTPSender(nil))
	if err != nil {
		t.Fatal(err)
	}
	report, err := scanner.Scan(context.Background(), Request{HistoryID: 1, MaxProbes: 1, Acknowledge: true})
	if err != nil || seen.Load() != 1 || report.ProbeCount != 1 || !report.Probes[0].Reflected {
		t.Fatalf("report=%+v seen=%d err=%v", report, seen.Load(), err)
	}
}
