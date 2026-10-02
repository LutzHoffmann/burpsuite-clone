package intruder

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func validConfig() Config {
	return Config{
		Attack: AttackSniper,
		Template: Template{
			Method: "GET",
			URL:    "https://example.test/items?id=1",
			Raw:    []byte("GET /items?id=1 HTTP/1.1\r\nHost: example.test\r\n\r\n"),
		},
		Positions:     []Position{{ID: "p1", Start: 14, End: 15, PayloadSetID: "s1"}},
		PayloadSets:   []PayloadSet{{ID: "s1", Payloads: [][]byte{[]byte("one"), []byte("two")}}},
		RequestLimit:  1000,
		Concurrency:   2,
		RatePerSecond: 5,
		Timeout:       30 * time.Second,
	}
}

func TestValidateConfigAcceptsLegalBoundaryValues(t *testing.T) {
	cfg := validConfig()
	cfg.RequestLimit = 1
	cfg.Concurrency = 1
	cfg.RatePerSecond = 0.1
	cfg.Timeout = time.Second
	cfg.PayloadSets[0].Payloads = [][]byte{make([]byte, MaxPayloadBytes)}
	cfg.Template.Raw = make([]byte, MaxTemplateBytes)
	cfg.Positions[0] = Position{ID: "p1", Start: 0, End: 1, PayloadSetID: "s1"}

	count, err := ValidateConfig(cfg)
	if err != nil || count != 1 {
		t.Fatalf("ValidateConfig() = %d, %v", count, err)
	}

	cfg.RequestLimit = MaxRequests
	cfg.Concurrency = MaxConcurrency
	cfg.RatePerSecond = MaxRatePerSecond
	cfg.Timeout = MaxTimeout
	if _, err := ValidateConfig(cfg); err != nil {
		t.Fatalf("maximum boundaries rejected: %v", err)
	}
}

func TestValidateConfigRejectsInvalidFields(t *testing.T) {
	tests := []struct {
		name  string
		field string
		code  string
		edit  func(*Config)
	}{
		{"attack", "attack", "unsupported", func(c *Config) { c.Attack = "unknown" }},
		{"no positions", "positions", "required", func(c *Config) { c.Positions = nil }},
		{"zero width", "positions[0]", "range", func(c *Config) { c.Positions[0].End = c.Positions[0].Start }},
		{"out of range", "positions[0]", "range", func(c *Config) { c.Positions[0].End = len(c.Template.Raw) + 1 }},
		{"unsorted", "positions[1]", "order", func(c *Config) {
			c.Positions = []Position{{ID: "p1", Start: 8, End: 9, PayloadSetID: "s1"}, {ID: "p2", Start: 2, End: 3, PayloadSetID: "s1"}}
		}},
		{"overlap", "positions[1]", "overlap", func(c *Config) {
			c.Positions = []Position{{ID: "p1", Start: 2, End: 5, PayloadSetID: "s1"}, {ID: "p2", Start: 4, End: 6, PayloadSetID: "s1"}}
		}},
		{"missing set", "positions[0].payloadSetId", "missing", func(c *Config) { c.Positions[0].PayloadSetID = "absent" }},
		{"empty assigned set", "payloadSets[s1]", "required", func(c *Config) { c.PayloadSets[0].Payloads = nil }},
		{"duplicate set id", "payloadSets[1].id", "duplicate", func(c *Config) { c.PayloadSets = append(c.PayloadSets, c.PayloadSets[0]) }},
		{"large template", "template.raw", "too_large", func(c *Config) { c.Template.Raw = make([]byte, MaxTemplateBytes+1) }},
		{"large payload", "payloadSets[s1].payloads[0]", "too_large", func(c *Config) { c.PayloadSets[0].Payloads[0] = make([]byte, MaxPayloadBytes+1) }},
		{"too many payloads", "payloadSets", "too_many", func(c *Config) { c.PayloadSets[0].Payloads = make([][]byte, MaxPayloadCount+1) }},
		{"nan rate", "ratePerSecond", "range", func(c *Config) { c.RatePerSecond = math.NaN() }},
		{"infinite rate", "ratePerSecond", "range", func(c *Config) { c.RatePerSecond = math.Inf(1) }},
		{"low request limit", "requestLimit", "range", func(c *Config) { c.RequestLimit = 0 }},
		{"high request limit", "requestLimit", "range", func(c *Config) { c.RequestLimit = MaxRequests + 1 }},
		{"low concurrency", "concurrency", "range", func(c *Config) { c.Concurrency = 0 }},
		{"high concurrency", "concurrency", "range", func(c *Config) { c.Concurrency = MaxConcurrency + 1 }},
		{"low rate", "ratePerSecond", "range", func(c *Config) { c.RatePerSecond = MinRatePerSecond / 2 }},
		{"high rate", "ratePerSecond", "range", func(c *Config) { c.RatePerSecond = MaxRatePerSecond + 1 }},
		{"short timeout", "timeout", "range", func(c *Config) { c.Timeout = MinTimeout - time.Millisecond }},
		{"long timeout", "timeout", "range", func(c *Config) { c.Timeout = MaxTimeout + time.Second }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			tc.edit(&cfg)
			_, err := ValidateConfig(cfg)
			var fieldErr *FieldError
			if !errors.As(err, &fieldErr) {
				t.Fatalf("expected FieldError, got %T %v", err, err)
			}
			if fieldErr.Field != tc.field || fieldErr.Code != tc.code {
				t.Fatalf("FieldError = %#v, want field=%q code=%q", fieldErr, tc.field, tc.code)
			}
			if strings.Contains(fieldErr.Error(), "one") {
				t.Fatalf("error leaks payload: %q", fieldErr.Error())
			}
		})
	}
}

func TestCountAttackTypes(t *testing.T) {
	positions := []Position{
		{ID: "p1", Start: 1, End: 2, PayloadSetID: "s1"},
		{ID: "p2", Start: 3, End: 4, PayloadSetID: "s2"},
	}
	sets := []PayloadSet{
		{ID: "s1", Payloads: [][]byte{[]byte("a"), []byte("b")}},
		{ID: "s2", Payloads: [][]byte{[]byte("1"), []byte("2"), []byte("3")}},
	}
	tests := []struct {
		attack AttackType
		want   int64
	}{
		{AttackSniper, 5},
		{AttackPitchfork, 2},
		{AttackClusterBomb, 6},
	}
	for _, tc := range tests {
		t.Run(string(tc.attack), func(t *testing.T) {
			cfg := validConfig()
			cfg.Attack, cfg.Positions, cfg.PayloadSets = tc.attack, positions, sets
			count, err := ValidateConfig(cfg)
			if err != nil || count != tc.want {
				t.Fatalf("count = %d, %v; want %d", count, err, tc.want)
			}
		})
	}

	cfg := validConfig()
	cfg.Attack = AttackBatteringRam
	cfg.Positions = positions
	cfg.PayloadSets = []PayloadSet{{ID: "s1", Payloads: sets[0].Payloads}}
	cfg.Positions[1].PayloadSetID = "s1"
	count, err := ValidateConfig(cfg)
	if err != nil || count != 2 {
		t.Fatalf("battering ram count = %d, %v", count, err)
	}
}

func TestCountRejectsClusterBombBeyondLimitWithoutOverflow(t *testing.T) {
	cfg := validConfig()
	cfg.Attack = AttackClusterBomb
	cfg.RequestLimit = MaxRequests
	cfg.Positions = make([]Position, 20)
	cfg.PayloadSets = make([]PayloadSet, 20)
	for i := range cfg.Positions {
		cfg.Positions[i] = Position{ID: string(rune('a' + i)), Start: i * 2, End: i*2 + 1, PayloadSetID: string(rune('A' + i))}
		cfg.PayloadSets[i] = PayloadSet{ID: string(rune('A' + i)), Payloads: make([][]byte, 10)}
	}
	_, err := ValidateConfig(cfg)
	var fieldErr *FieldError
	if !errors.As(err, &fieldErr) || fieldErr.Field != "requestLimit" || fieldErr.Code != "exceeded" {
		t.Fatalf("expected bounded request-limit error, got %v", err)
	}
}

func TestIteratorSequences(t *testing.T) {
	positions := []Position{
		{ID: "p1", Start: 1, End: 2, PayloadSetID: "s1"},
		{ID: "p2", Start: 3, End: 4, PayloadSetID: "s2"},
	}
	sets := []PayloadSet{
		{ID: "s1", Payloads: [][]byte{[]byte("a"), []byte("a"), {}}},
		{ID: "s2", Payloads: [][]byte{[]byte("1"), []byte("2")}},
	}
	tests := []struct {
		name   string
		attack AttackType
		want   []Combination
	}{
		{"sniper", AttackSniper, []Combination{
			{Sequence: 0, Selections: []Selection{{PositionID: "p1", PayloadSetID: "s1", PayloadIndex: 0, Payload: []byte("a")}}},
			{Sequence: 1, Selections: []Selection{{PositionID: "p1", PayloadSetID: "s1", PayloadIndex: 1, Payload: []byte("a")}}},
			{Sequence: 2, Selections: []Selection{{PositionID: "p1", PayloadSetID: "s1", PayloadIndex: 2, Payload: []byte{}}}},
			{Sequence: 3, Selections: []Selection{{PositionID: "p2", PayloadSetID: "s2", PayloadIndex: 0, Payload: []byte("1")}}},
			{Sequence: 4, Selections: []Selection{{PositionID: "p2", PayloadSetID: "s2", PayloadIndex: 1, Payload: []byte("2")}}},
		}},
		{"pitchfork", AttackPitchfork, []Combination{
			{Sequence: 0, Selections: []Selection{{PositionID: "p1", PayloadSetID: "s1", PayloadIndex: 0, Payload: []byte("a")}, {PositionID: "p2", PayloadSetID: "s2", PayloadIndex: 0, Payload: []byte("1")}}},
			{Sequence: 1, Selections: []Selection{{PositionID: "p1", PayloadSetID: "s1", PayloadIndex: 1, Payload: []byte("a")}, {PositionID: "p2", PayloadSetID: "s2", PayloadIndex: 1, Payload: []byte("2")}}},
		}},
		{"cluster", AttackClusterBomb, []Combination{
			{Sequence: 0, Selections: []Selection{{PositionID: "p1", PayloadSetID: "s1", PayloadIndex: 0, Payload: []byte("a")}, {PositionID: "p2", PayloadSetID: "s2", PayloadIndex: 0, Payload: []byte("1")}}},
			{Sequence: 1, Selections: []Selection{{PositionID: "p1", PayloadSetID: "s1", PayloadIndex: 0, Payload: []byte("a")}, {PositionID: "p2", PayloadSetID: "s2", PayloadIndex: 1, Payload: []byte("2")}}},
			{Sequence: 2, Selections: []Selection{{PositionID: "p1", PayloadSetID: "s1", PayloadIndex: 1, Payload: []byte("a")}, {PositionID: "p2", PayloadSetID: "s2", PayloadIndex: 0, Payload: []byte("1")}}},
			{Sequence: 3, Selections: []Selection{{PositionID: "p1", PayloadSetID: "s1", PayloadIndex: 1, Payload: []byte("a")}, {PositionID: "p2", PayloadSetID: "s2", PayloadIndex: 1, Payload: []byte("2")}}},
			{Sequence: 4, Selections: []Selection{{PositionID: "p1", PayloadSetID: "s1", PayloadIndex: 2, Payload: []byte{}}, {PositionID: "p2", PayloadSetID: "s2", PayloadIndex: 0, Payload: []byte("1")}}},
			{Sequence: 5, Selections: []Selection{{PositionID: "p1", PayloadSetID: "s1", PayloadIndex: 2, Payload: []byte{}}, {PositionID: "p2", PayloadSetID: "s2", PayloadIndex: 1, Payload: []byte("2")}}},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Attack, cfg.Positions, cfg.PayloadSets = tc.attack, positions, sets
			got := collect(t, cfg, 0)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("combinations = %#v\nwant %#v", got, tc.want)
			}
		})
	}
}

func TestIteratorBatteringRamAndResume(t *testing.T) {
	cfg := validConfig()
	cfg.Attack = AttackBatteringRam
	cfg.Positions = []Position{{ID: "p1", Start: 1, End: 2, PayloadSetID: "shared"}, {ID: "p2", Start: 3, End: 4, PayloadSetID: "shared"}}
	cfg.PayloadSets = []PayloadSet{{ID: "shared", Payloads: [][]byte{[]byte("x"), []byte("y"), []byte("z")}}}

	want := []Combination{{Sequence: 1, Selections: []Selection{
		{PositionID: "p1", PayloadSetID: "shared", PayloadIndex: 1, Payload: []byte("y")},
		{PositionID: "p2", PayloadSetID: "shared", PayloadIndex: 1, Payload: []byte("y")},
	}}, {Sequence: 2, Selections: []Selection{
		{PositionID: "p1", PayloadSetID: "shared", PayloadIndex: 2, Payload: []byte("z")},
		{PositionID: "p2", PayloadSetID: "shared", PayloadIndex: 2, Payload: []byte("z")},
	}}}
	if got := collect(t, cfg, 1); !reflect.DeepEqual(got, want) {
		t.Fatalf("resume combinations = %#v, want %#v", got, want)
	}
	if got := collect(t, cfg, 3); len(got) != 0 {
		t.Fatalf("resume at total produced %#v", got)
	}
}

func TestIteratorCopiesExposedPayloads(t *testing.T) {
	cfg := validConfig()
	it, err := NewIterator(cfg, 0)
	if err != nil {
		t.Fatal(err)
	}
	first, ok := it.Next()
	if !ok {
		t.Fatal("missing first combination")
	}
	first.Selections[0].Payload[0] = 'X'
	secondIt, err := NewIterator(cfg, 0)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := secondIt.Next()
	if string(again.Selections[0].Payload) != "one" {
		t.Fatalf("iterator exposed source payload: %q", again.Selections[0].Payload)
	}
}

func collect(t *testing.T, cfg Config, start int64) []Combination {
	t.Helper()
	it, err := NewIterator(cfg, start)
	if err != nil {
		t.Fatal(err)
	}
	var out []Combination
	for {
		combination, ok := it.Next()
		if !ok {
			return out
		}
		out = append(out, combination)
	}
}
