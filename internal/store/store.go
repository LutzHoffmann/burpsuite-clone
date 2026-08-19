package store

import (
	"context"
	"time"
)

type Exchange struct {
	ID                int64
	Method            string
	Scheme            string
	Host              string
	Path              string
	Query             string
	Status            int
	MIMEType          string
	RequestSize       int64
	ResponseSize      int64
	Duration          time.Duration
	StartedAt         time.Time
	Intercepted       bool
	Error             bool
	ErrorMessage      string
	RequestTruncated  bool
	ResponseTruncated bool
	Request           RequestData
	Response          ResponseData
	Tags              []string
	Note              string
}

type RequestData struct {
	Headers map[string][]string
	Body    []byte
	Raw     []byte
}

type ResponseData struct {
	Headers map[string][]string
	Body    []byte
	Raw     []byte
}

type HistoryItem struct {
	ID           int64     `json:"id"`
	Method       string    `json:"method"`
	Scheme       string    `json:"scheme"`
	Host         string    `json:"host"`
	Path         string    `json:"path"`
	Query        string    `json:"query"`
	Status       int       `json:"status"`
	MIMEType     string    `json:"mimeType"`
	RequestSize  int64     `json:"requestSize"`
	ResponseSize int64     `json:"responseSize"`
	DurationMS   int64     `json:"durationMs"`
	StartedAt    time.Time `json:"startedAt"`
	Intercepted  bool      `json:"intercepted"`
	Error        bool      `json:"error"`
}

type HistoryFilter struct {
	Search string
	Method string
	Host   string
}

type Store interface {
	SaveExchange(ctx context.Context, exchange *Exchange) error
	ListHistory(ctx context.Context, filter HistoryFilter) ([]HistoryItem, error)
	GetExchange(ctx context.Context, id int64) (*Exchange, error)
	Close() error
}
