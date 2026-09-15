package proxy

import (
	"context"
	"testing"

	"github.com/lutzifer/burpsuite-clone/internal/events"
	"github.com/lutzifer/burpsuite-clone/internal/store"
)

type quotaRejectStore struct{ *memoryStore }

func (s *quotaRejectStore) StorageStatus(context.Context) (store.StorageStatus, error) {
	return store.StorageStatus{Paused: true, Revision: 1}, nil
}
func (s *quotaRejectStore) SetStorageLimit(context.Context, int64) (store.StorageStatus, error) {
	return s.StorageStatus(context.Background())
}

func (s *quotaRejectStore) SaveExchange(context.Context, *store.Exchange) error {
	return store.ErrCaptureQuotaExceeded
}

func TestQuotaSkippedExchangeDoesNotPublishHistoryOrProjectTarget(t *testing.T) {
	hub := events.NewHub()
	changes, unsubscribe := hub.Subscribe()
	defer unsubscribe()
	observed := false
	srv := NewServer(Config{
		Store: &quotaRejectStore{&memoryStore{}}, Events: hub,
		Target: targetObserverFunc(func(context.Context, *store.Exchange) error { observed = true; return nil }),
	})
	for i := 0; i < 20; i++ {
		srv.saveExchange(&store.Exchange{})
	}
	if observed {
		t.Fatal("skipped capture projected into target")
	}
	select {
	case change := <-changes:
		if change.Type != "storage.status.changed" {
			t.Fatalf("unexpected event: %#v", change)
		}
	default:
		t.Fatal("missing storage warning")
	}
	select {
	case change := <-changes:
		t.Fatalf("duplicate or false history event: %#v", change)
	default:
	}
}
