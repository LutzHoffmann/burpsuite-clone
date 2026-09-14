package proxy

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/events"
	"github.com/lutzifer/burpsuite-clone/internal/store"
)

const wsQueueBytes int64 = 16 << 20

type wsCapture struct {
	server     *Server
	repository store.WebSocketStore
	connection store.WSConnection
	queue      chan store.WSMessage
	done       chan struct{}
	mu         sync.Mutex
	sequence   int64
	gaps       atomic.Int64
	incomplete atomic.Bool
	closeSeen  atomic.Int32
}

func (s *Server) startWSCapture(r *http.Request, inScope bool) *wsCapture {
	repository, ok := s.cfg.Store.(store.WebSocketStore)
	if !ok {
		return nil
	}
	if s.wsActive.Add(1) > 64 {
		s.wsActive.Add(-1)
		if s.cfg.Events != nil && !s.wsLimitWarning.Swap(true) {
			s.cfg.Events.Publish(events.Event{Type: "websocket.capture.limited", Data: map[string]any{"limit": 64}})
		}
		return nil
	}
	url := *r.URL
	url.Scheme = strings.Replace(url.Scheme, "http", "ws", 1)
	c := &wsCapture{server: s, repository: repository, connection: store.WSConnection{URL: url.String(), InScope: inScope, OpenedAt: time.Now().UTC(), State: "open"}, queue: make(chan store.WSMessage, 64), done: make(chan struct{})}
	go c.persist()
	return c
}

func (c *wsCapture) emit(direction string, m wsObservedMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sequence++
	if m.Type == "close" {
		c.closeSeen.Add(1)
	}
	s := c.server
	n := int64(len(m.Payload))
	if s.wsQueued.Add(1) > 64 {
		s.wsQueued.Add(-1)
		c.gaps.Add(1)
		return
	}
	if s.wsBytes.Add(n) > wsQueueBytes {
		s.wsBytes.Add(-n)
		s.wsQueued.Add(-1)
		c.gaps.Add(1)
		return
	}
	message := store.WSMessage{Sequence: c.sequence, Direction: direction, ObservedAt: time.Now().UTC(), Type: m.Type, Size: m.Size, Payload: m.Payload, Truncated: m.Truncated, Complete: m.Complete, Encoding: m.Encoding}
	select {
	case c.queue <- message:
	default:
		s.wsQueued.Add(-1)
		s.wsBytes.Add(-n)
		c.gaps.Add(1)
	}
}

func (c *wsCapture) finish() {
	close(c.queue)
	<-c.done
	c.server.wsActive.Add(-1)
	c.server.wsLimitWarning.Store(false)
}

func (c *wsCapture) persist() {
	defer close(c.done)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	err := c.repository.SaveWSConnection(ctx, &c.connection)
	cancel()
	if err != nil {
		c.reportQuota(err)
		c.incomplete.Store(true)
	}
	for message := range c.queue {
		if c.connection.ID != 0 {
			message.ConnectionID = c.connection.ID
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			err := c.repository.SaveWSMessage(ctx, &message)
			cancel()
			if err != nil {
				c.gaps.Add(1)
				c.reportQuota(err)
			}
			if updater, ok := c.repository.(interface {
				UpdateWSCapture(context.Context, int64, int64, bool) error
			}); ok && (c.gaps.Load() > 0 || c.incomplete.Load()) {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				_ = updater.UpdateWSCapture(ctx, c.connection.ID, c.gaps.Load(), c.incomplete.Load())
				cancel()
			}
		}
		c.server.wsBytes.Add(-int64(len(message.Payload)))
		c.server.wsQueued.Add(-1)
	}
	if c.connection.ID != 0 {
		state := "closed"
		if c.closeSeen.Load() < 2 {
			state = "interrupted"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = c.repository.FinishWSConnection(ctx, c.connection.ID, state, c.gaps.Load(), c.incomplete.Load())
		cancel()
		if c.server.cfg.Events != nil {
			c.server.cfg.Events.Publish(events.Event{Type: "websocket.connection.updated", Data: map[string]any{"id": c.connection.ID}})
		}
	}
}

func (c *wsCapture) reportQuota(err error) {
	if !errors.Is(err, store.ErrCaptureQuotaExceeded) || c.server.cfg.Events == nil {
		return
	}
	if quota, ok := c.server.cfg.Store.(store.QuotaStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if status, err := quota.StorageStatus(ctx); err == nil {
			c.server.cfg.Events.PublishStoragePaused(status.Paused, status.Revision)
		}
	}
}
