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
	ID      string              `json:"id"`
	Method  string              `json:"method"`
	URL     string              `json:"url"`
	Headers map[string][]string `json:"headers"`
	Body    []byte              `json:"body"`
}

type RequestEdit struct {
	Method  string              `json:"method"`
	URL     string              `json:"url"`
	Headers map[string][]string `json:"headers"`
	Body    []byte              `json:"body"`
}

type Decision struct {
	Action Action
	Edit   RequestEdit
}

type Queue struct {
	mu      sync.Mutex
	items   map[string]Item
	waiting map[string]chan Decision
	timeout time.Duration
}

func NewQueue(timeout time.Duration) *Queue {
	return &Queue{
		items:   make(map[string]Item),
		waiting: make(map[string]chan Decision),
		timeout: timeout,
	}
}

func (q *Queue) Enqueue(ctx context.Context, item Item) (Decision, error) {
	result := make(chan Decision, 1)

	q.mu.Lock()
	if _, exists := q.items[item.ID]; exists {
		q.mu.Unlock()
		return Decision{}, fmt.Errorf("intercept item %q already queued", item.ID)
	}
	q.items[item.ID] = item
	q.waiting[item.ID] = result
	q.mu.Unlock()

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
		items = append(items, item)
	}
	return items
}

func (q *Queue) decide(id string, decision Decision) error {
	q.mu.Lock()
	result, exists := q.waiting[id]
	if !exists {
		q.mu.Unlock()
		return fmt.Errorf("intercept item %q not found", id)
	}
	delete(q.items, id)
	delete(q.waiting, id)
	q.mu.Unlock()

	result <- decision
	return nil
}

func (q *Queue) remove(id string, result chan Decision) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	if current, exists := q.waiting[id]; !exists || current != result {
		return false
	}
	delete(q.items, id)
	delete(q.waiting, id)
	return true
}
