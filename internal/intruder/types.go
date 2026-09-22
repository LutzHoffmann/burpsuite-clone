package intruder

import (
	"fmt"
	"time"
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
