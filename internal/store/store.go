package store

import (
	"context"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/scope"
)

type Exchange struct {
	ID                  int64
	Method              string
	Scheme              string
	Host                string
	Path                string
	Query               string
	Status              int
	MIMEType            string
	RequestSize         int64
	ResponseSize        int64
	Duration            time.Duration
	StartedAt           time.Time
	Intercepted         bool
	ResponseIntercepted bool
	AppliedRuleIDs      []string
	Error               bool
	ErrorMessage        string
	RequestTruncated    bool
	ResponseTruncated   bool
	InScope             bool
	ScopeVersion        int64
	ScopeRuleID         *int64
	Request             RequestData
	Response            ResponseData
	Tags                []string
	Note                string
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

type TargetEndpointKey struct {
	Scheme, Host string
	Port         int
	Path, Method string
}

type TargetParameter struct {
	Location, Name, ValueType string
	FirstSeen, LastSeen       time.Time
	Count                     int64
}

type TargetObservation struct {
	Key             TargetEndpointKey
	ExchangeID      int64
	StartedAt       time.Time
	Status          int
	RequestMIME     string
	ResponseMIME    string
	Error           bool
	Parameters      []TargetParameter
	ParseDiagnostic string
}

type RebuildStatus struct {
	ID, ScopeVersion, ActiveScopeVersion, Processed, Total int64
	Status, Error                                          string
}

type HistoryItem struct {
	ResponseIntercepted bool      `json:"responseIntercepted"`
	AppliedRuleIDs      []string  `json:"appliedRuleIds"`
	ID                  int64     `json:"id"`
	Method              string    `json:"method"`
	Scheme              string    `json:"scheme"`
	Host                string    `json:"host"`
	Path                string    `json:"path"`
	Query               string    `json:"query"`
	Status              int       `json:"status"`
	MIMEType            string    `json:"mimeType"`
	RequestSize         int64     `json:"requestSize"`
	ResponseSize        int64     `json:"responseSize"`
	DurationMS          int64     `json:"durationMs"`
	StartedAt           time.Time `json:"startedAt"`
	Intercepted         bool      `json:"intercepted"`
	Error               bool      `json:"error"`
	InScope             bool      `json:"inScope"`
	ScopeVersion        int64     `json:"scopeVersion"`
	ScopeRuleID         *int64    `json:"scopeRuleId"`
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

type ScopeStore interface {
	LoadScopeState(context.Context) (scope.State, error)
	ReplaceScopeRules(context.Context, int64, []scope.Rule) (scope.State, error)
}

type RebuildHistoryStore interface {
	LatestExchangeID(context.Context) (int64, error)
	CountExchangesThrough(context.Context, int64) (int64, error)
	ListExchangesPage(context.Context, int64, int64, int) ([]Exchange, error)
}
