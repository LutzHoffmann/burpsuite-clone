package intercept

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Action string

const (
	ActionForward Action = "forward"
	ActionDrop    Action = "drop"
)

type Item struct {
	Phase         string              `json:"phase,omitempty"`
	StatusCode    int                 `json:"statusCode,omitempty"`
	ID            string              `json:"id"`
	Method        string              `json:"method"`
	URL           string              `json:"url"`
	Headers       map[string][]string `json:"headers"`
	Body          []byte              `json:"-"`
	BodyEditable  bool                `json:"bodyEditable"`
	BodyTruncated bool                `json:"bodyTruncated"`
}

type RequestEdit struct {
	StatusCode int                 `json:"statusCode,omitempty"`
	Method     string              `json:"method"`
	URL        string              `json:"url"`
	Headers    map[string][]string `json:"headers"`
	Body       []byte              `json:"body"`
	BodySet    bool                `json:"-"`
}

type Decision struct {
	Action Action
	Edit   RequestEdit
}

type Queue struct {
	mu       sync.Mutex
	items    map[string]Item
	waiting  map[string]chan Decision
	timeout  time.Duration
	observer func(Change)
}

type Change struct {
	Type   string
	Item   Item
	Action Action
}

func NewQueue(timeout time.Duration) *Queue {
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	return &Queue{
		items:   make(map[string]Item),
		waiting: make(map[string]chan Decision),
		timeout: timeout,
	}
}

func (q *Queue) Enqueue(ctx context.Context, item Item) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	result := make(chan Decision, 1)

	q.mu.Lock()
	if len(q.items) >= 100 {
		q.mu.Unlock()
		return Decision{}, fmt.Errorf("intercept queue capacity reached")
	}
	if _, exists := q.items[item.ID]; exists {
		q.mu.Unlock()
		return Decision{}, fmt.Errorf("intercept item %q already queued", item.ID)
	}
	q.items[item.ID] = cloneItem(item)
	q.waiting[item.ID] = result
	observer := q.observer
	q.mu.Unlock()
	if observer != nil {
		observer(Change{Type: "queued", Item: cloneItem(item)})
	}

	timer := time.NewTimer(q.timeout)
	defer timer.Stop()

	select {
	case decision := <-result:
		return decision, nil
	case <-ctx.Done():
		if q.remove(item.ID, result) {
			return Decision{}, ctx.Err()
		}
		return <-result, nil
	case <-timer.C:
		if q.remove(item.ID, result) {
			return Decision{}, fmt.Errorf("intercept item %q timed out", item.ID)
		}
		return <-result, nil
	}
}

func (q *Queue) Forward(id string, edited RequestEdit) error {
	return q.decide(id, Decision{Action: ActionForward, Edit: edited})
}

func (q *Queue) Drop(id string) error {
	return q.decide(id, Decision{Action: ActionDrop})
}

func (q *Queue) List() []Item {
	q.mu.Lock()
	defer q.mu.Unlock()

	items := make([]Item, 0, len(q.items))
	for _, item := range q.items {
		items = append(items, cloneItem(item))
	}
	return items
}

func (q *Queue) Get(id string) (Item, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	item, ok := q.items[id]
	return cloneItem(item), ok
}

func (q *Queue) SetObserver(observer func(Change)) {
	q.mu.Lock()
	q.observer = observer
	q.mu.Unlock()
}

func (q *Queue) decide(id string, decision Decision) error {
	q.mu.Lock()
	result, exists := q.waiting[id]
	if !exists {
		q.mu.Unlock()
		return fmt.Errorf("intercept item %q not found", id)
	}
	item := q.items[id]
	delete(q.items, id)
	delete(q.waiting, id)
	observer := q.observer
	q.mu.Unlock()

	result <- decision
	if observer != nil {
		observer(Change{Type: "completed", Item: cloneItem(item), Action: decision.Action})
	}
	return nil
}

func (q *Queue) remove(id string, result chan Decision) bool {
	q.mu.Lock()

	if current, exists := q.waiting[id]; !exists || current != result {
		q.mu.Unlock()
		return false
	}
	item := q.items[id]
	delete(q.items, id)
	delete(q.waiting, id)
	observer := q.observer
	q.mu.Unlock()
	if observer != nil {
		observer(Change{Type: "completed", Item: cloneItem(item)})
	}
	return true
}

func cloneItem(item Item) Item {
	item.Body = append([]byte(nil), item.Body...)
	if item.Headers != nil {
		original := item.Headers
		item.Headers = make(map[string][]string, len(item.Headers))
		for name, values := range original {
			item.Headers[name] = append([]string(nil), values...)
		}
	}
	return item
}
