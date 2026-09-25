package api

import (
	"net/http"
	"testing"

	"github.com/lutzifer/burpsuite-clone/internal/store"
)

func TestWebSocketRoutesUnavailableAndProtected(t *testing.T) {
	s := NewServer(Config{Store: store.NewMemoryForTests(), APIAddr: "127.0.0.1:9080"})
	for _, path := range []string{"/api/websockets", "/api/websockets/1", "/api/websockets/1/messages", "/api/websockets/1/messages/1"} {
		if w := storageRequest(s, "GET", path, ""); w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: got %d, want 503", path, w.Code)
		}
		if w := serveAPIRequestWithOrigin(s, "GET", path, "", "", "https://evil.test"); w.Code != http.StatusForbidden {
			t.Errorf("foreign origin %s: got %d", path, w.Code)
		}
	}
}
