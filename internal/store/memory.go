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
	copy := *exchange
	copy.AppliedRuleIDs = append([]string{}, exchange.AppliedRuleIDs...)
	s.exchanges = append(s.exchanges, copy)
	return nil
}

func (s *memoryStore) ListHistory(ctx context.Context, filter HistoryFilter) ([]HistoryItem, error) {
	page, err := s.ListHistoryPage(ctx, HistoryPageRequest{Filter: filter})
	return page.Items, err
}

func (s *memoryStore) ListHistoryPage(ctx context.Context, request HistoryPageRequest) (HistoryPage, error) {
	if err := validateHistoryPageRequest(request); err != nil {
		return HistoryPage{}, err
	}
	if err := ctx.Err(); err != nil {
		return HistoryPage{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	snapshot := request.SnapshotID
	if snapshot == 0 {
		snapshot = s.nextID
	}
	history := make([]HistoryItem, 0, historyPageSize+1)
	for index := len(s.exchanges) - 1; index >= 0; index-- {
		if err := ctx.Err(); err != nil {
			return HistoryPage{}, err
		}
		exchange := s.exchanges[index]
		if exchange.ID > snapshot || (request.BeforeID > 0 && exchange.ID >= request.BeforeID) || !matchesHistoryFilter(exchange, request.Filter) {
			continue
		}
		history = append(history, HistoryItem{
			ResponseIntercepted: exchange.ResponseIntercepted,
			AppliedRuleIDs:      append([]string{}, exchange.AppliedRuleIDs...),
			ID:                  exchange.ID,
			Method:              exchange.Method,
			Scheme:              exchange.Scheme,
			Host:                exchange.Host,
			Path:                exchange.Path,
			Query:               exchange.Query,
			Status:              exchange.Status,
			MIMEType:            exchange.MIMEType,
			RequestSize:         exchange.RequestSize,
			ResponseSize:        exchange.ResponseSize,
			DurationMS:          exchange.Duration.Milliseconds(),
			StartedAt:           exchange.StartedAt,
			Intercepted:         exchange.Intercepted,
			Error:               exchange.Error,
			InScope:             exchange.InScope,
			ScopeVersion:        exchange.ScopeVersion,
			ScopeRuleID:         exchange.ScopeRuleID,
		})
		if len(history) == historyPageSize+1 {
			break
		}
	}
	return finishHistoryPage(history, snapshot), nil
}

func (s *memoryStore) GetExchange(_ context.Context, id int64) (*Exchange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, exchange := range s.exchanges {
		if exchange.ID == id {
			copy := exchange
			copy.AppliedRuleIDs = append([]string{}, exchange.AppliedRuleIDs...)
			return &copy, nil
		}
	}
	return nil, sql.ErrNoRows
}

func (s *memoryStore) Close() error {
	return nil
}

func matchesHistoryFilter(exchange Exchange, filter HistoryFilter) bool {
	if filter.InScope != nil && exchange.InScope != *filter.InScope {
		return false
	}
	if filter.Method != "" && exchange.Method != filter.Method {
		return false
	}
	if filter.Host != "" && exchange.Host != filter.Host {
		return false
	}
	if filter.Search == "" {
		return true
	}
	search := historyASCIILower(filter.Search)
	return strings.Contains(historyASCIILower(exchange.Host), search) || strings.Contains(historyASCIILower(exchange.Path), search) || strings.Contains(historyASCIILower(exchange.Query), search)
}
