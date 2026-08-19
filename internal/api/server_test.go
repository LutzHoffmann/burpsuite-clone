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
