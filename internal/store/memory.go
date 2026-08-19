package store

import (
	"context"
	"database/sql"
	"strings"
	"sync"
)

type memoryStore struct {
	mu        sync.Mutex
	exchanges []Exchange
	nextID    int64
}

// NewMemoryForTests creates an in-memory Store suitable for tests.
func NewMemoryForTests() Store {
	return &memoryStore{}
}

func (s *memoryStore) SaveExchange(_ context.Context, exchange *Exchange) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	exchange.ID = s.nextID
	s.exchanges = append(s.exchanges, *exchange)
	return nil
}

func (s *memoryStore) ListHistory(_ context.Context, filter HistoryFilter) ([]HistoryItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var history []HistoryItem
	for index := len(s.exchanges) - 1; index >= 0; index-- {
		exchange := s.exchanges[index]
		if !matchesHistoryFilter(exchange, filter) {
			continue
		}
		history = append(history, HistoryItem{
			ID:           exchange.ID,
			Method:       exchange.Method,
			Scheme:       exchange.Scheme,
			Host:         exchange.Host,
			Path:         exchange.Path,
			Query:        exchange.Query,
			Status:       exchange.Status,
			MIMEType:     exchange.MIMEType,
			RequestSize:  exchange.RequestSize,
			ResponseSize: exchange.ResponseSize,
			DurationMS:   exchange.Duration.Milliseconds(),
			StartedAt:    exchange.StartedAt,
			Intercepted:  exchange.Intercepted,
			Error:        exchange.Error,
		})
	}
	return history, nil
}

func (s *memoryStore) GetExchange(_ context.Context, id int64) (*Exchange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, exchange := range s.exchanges {
		if exchange.ID == id {
			copy := exchange
			return &copy, nil
		}
	}
	return nil, sql.ErrNoRows
}

func (s *memoryStore) Close() error {
	return nil
}

func matchesHistoryFilter(exchange Exchange, filter HistoryFilter) bool {
	if filter.Method != "" && exchange.Method != filter.Method {
		return false
	}
	if filter.Host != "" && exchange.Host != filter.Host {
		return false
	}
	if filter.Search == "" {
		return true
	}
	return strings.Contains(exchange.Host, filter.Search) || strings.Contains(exchange.Path, filter.Search) || strings.Contains(exchange.Query, filter.Search)
}
