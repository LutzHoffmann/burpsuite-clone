package intruder

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	ErrRevisionConflict = errors.New("intruder revision conflict")
	ErrStateConflict    = errors.New("intruder state conflict")
	ErrResultExists     = errors.New("intruder result already exists")
	ErrSequenceConflict = errors.New("intruder result sequence conflict")
)

const (
	MaxTemplateBytes = 2 << 20
	MaxPayloadBytes  = 1 << 20
	MaxPayloadCount  = 100_000
	MaxRequests      = 100_000
	MaxConcurrency   = 20
	MinRatePerSecond = 0.1
	MaxRatePerSecond = 100.0
	MinTimeout       = time.Second
	MaxTimeout       = 120 * time.Second
)

type AttackType string

const (
	AttackSniper       AttackType = "sniper"
	AttackBatteringRam AttackType = "battering_ram"
	AttackPitchfork    AttackType = "pitchfork"
	AttackClusterBomb  AttackType = "cluster_bomb"
)

type Header struct {
	Name  string
	Value string
}

type Template struct {
	Method  string
	URL     string
	Headers []Header
	Body    []byte
	Raw     []byte
}

type Position struct {
	ID           string
	Start, End   int
	PayloadSetID string
}

type PayloadSet struct {
	ID       string
	Payloads [][]byte
}

type Config struct {
	Attack                    AttackType
	Template                  Template
	Positions                 []Position
	PayloadSets               []PayloadSet
	RequestLimit, Concurrency int
	RatePerSecond             float64
	Timeout                   time.Duration
}

type Selection struct {
	PositionID   string
	PayloadSetID string
	PayloadIndex int
	Payload      []byte
}

type Combination struct {
	Sequence   int64
	Selections []Selection
}

type State string

const (
	StateDraft     State = "draft"
	StateRunning   State = "running"
	StatePausing   State = "pausing"
	StatePaused    State = "paused"
	StateAborting  State = "aborting"
	StateAborted   State = "aborted"
	StateCompleted State = "completed"
	StateFailed    State = "failed"
)

type Draft struct {
	ID           string
	Config       Config
	ScopeVersion int64
}

type Job struct {
	ID               string
	Config           Config
	State            State
	StateReason      string
	Revision         int64
	TotalRequests    int64
	NextSequence     int64
	CompletedCount   int64
	ErrorCount       int64
	ScopeVersion     int64
	BaselineSequence *int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type JobSummary struct {
	ID             string
	Attack         AttackType
	State          State
	StateReason    string
	Revision       int64
	TotalRequests  int64
	CompletedCount int64
	ErrorCount     int64
	UpdatedAt      time.Time
}

type Result struct {
	Sequence          int64
	Selections        []Selection
	Method, URL       string
	Status            int
	MIMEType          string
	RequestSize       int64
	ResponseSize      int64
	Duration          time.Duration
	ErrorCategory     string
	ResponseTruncated bool
	RequestCapture    []byte
	ResponseCapture   []byte
	BodyStored        bool
	StorageStatus     string
	Similarity        int
	SimilarityPartial bool
	StatusDiff        bool
	LengthDelta       int64
	DurationDelta     int64
	MIMEDiff          bool
	CreatedAt         time.Time
}

type ResultQuery struct {
	BeforeSequence               *int64
	Status                       int
	ErrorCategory                string
	MIMEType                     string
	MinSize, MaxSize             int64
	MinDurationMS, MaxDurationMS int64
	MinSimilarity, MaxSimilarity int
	PayloadSearch                string
	Limit                        int
}

type ResultPage struct {
	Results            []Result
	NextBeforeSequence *int64
}

type Store interface {
	CreateDraft(context.Context, Draft) (Job, error)
	ReplaceDraft(context.Context, string, int64, Config) (Job, error)
	ListJobs(context.Context) ([]JobSummary, error)
	GetJob(context.Context, string) (Job, error)
	Transition(context.Context, string, int64, State, State, string) (Job, error)
	AppendResult(context.Context, string, Result) (Job, error)
	ListResults(context.Context, string, ResultQuery) (ResultPage, error)
	GetResult(context.Context, string, int64) (Result, error)
	RecoverRunning(context.Context) error
	DeleteJob(context.Context, string) error
}

type FieldError struct {
	Field string
	Code  string
}

func (e *FieldError) Error() string {
	return fmt.Sprintf("invalid %s: %s", e.Field, e.Code)
}

func invalid(field, code string) error {
	return &FieldError{Field: field, Code: code}
}
