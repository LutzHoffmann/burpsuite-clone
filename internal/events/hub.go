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

func NewHub() *Hub {
	return &Hub{subscribers: make(map[chan Event]struct{})}
}

func (h *Hub) Subscribe() (chan Event, func()) {
	subscriber := make(chan Event, 16)
	h.mu.Lock()
	h.subscribers[subscriber] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	return subscriber, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subscribers, subscriber)
			h.mu.Unlock()
		})
	}
}

func (h *Hub) Publish(event Event) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for subscriber := range h.subscribers {
		select {
		case subscriber <- event:
		default:
		}
	}
}
