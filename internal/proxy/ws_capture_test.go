package proxy

import (
	"context"
	"github.com/lutzifer/burpsuite-clone/internal/store"
	"testing"
	"time"
)

type blockedWSStore struct {
	store.WebSocketStore
	started chan struct{}
	release chan struct{}
	gaps    int64
	saved   int
}

func (s *blockedWSStore) SaveWSConnection(ctx context.Context, c *store.WSConnection) error {
	close(s.started)
	select {
	case <-s.release:
		c.ID = 1
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *blockedWSStore) SaveWSMessage(_ context.Context, m *store.WSMessage) error {
	s.saved++
	return nil
}
func (s *blockedWSStore) FinishWSConnection(_ context.Context, _ int64, _ string, gaps int64, _ bool) error {
	s.gaps = gaps
	return nil
}

func TestWSQueueLimitsDoNotBlockRelayObserver(t *testing.T) {
	for _, test := range []struct {
		name                  string
		payloadSize, accepted int
	}{{"records", 1, 64}, {"bytes", 1 << 20, 16}} {
		t.Run(test.name, func(t *testing.T) {
			repo := &blockedWSStore{started: make(chan struct{}), release: make(chan struct{})}
			s := NewServer(Config{})
			s.wsActive.Store(1)
			c := &wsCapture{server: s, repository: repo, queue: make(chan store.WSMessage, 64), done: make(chan struct{})}
			go c.persist()
			<-repo.started
			payload := make([]byte, test.payloadSize)
			for i := 0; i < test.accepted+1; i++ {
				c.emit("client-to-server", wsObservedMessage{Type: "binary", Payload: payload, Size: int64(len(payload)), Complete: true})
			}
			if c.gaps.Load() != 1 || s.wsBytes.Load() > wsQueueBytes || s.wsQueued.Load() > 64 {
				t.Fatal("queue budget exceeded")
			}
			close(repo.release)
			done := make(chan struct{})
			go func() { c.finish(); close(done) }()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("capture did not drain")
			}
			if repo.saved != test.accepted || repo.gaps != 1 || s.wsBytes.Load() != 0 || s.wsQueued.Load() != 0 || s.wsActive.Load() != 0 {
				t.Fatal("queue accounting leaked", repo.saved, repo.gaps)
			}
		})
	}
}
