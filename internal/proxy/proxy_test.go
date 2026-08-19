package proxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func mustParseURL(t *testing.T, rawURL string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

type memoryStore struct {
	saved []*store.Exchange
}

func (s *memoryStore) SaveExchange(_ context.Context, exchange *store.Exchange) error {
	s.saved = append(s.saved, exchange)
	return nil
}

func (s *memoryStore) ListHistory(context.Context, store.HistoryFilter) ([]store.HistoryItem, error) {
	return nil, nil
}

func (s *memoryStore) GetExchange(context.Context, int64) (*store.Exchange, error) {
	return nil, nil
}

func (s *memoryStore) Close() error {
	return nil
}
