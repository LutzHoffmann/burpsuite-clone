package activescan

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"sort"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/repeater"
	"github.com/lutzifer/burpsuite-clone/internal/store"
)

var (
	ErrInvalidInput = errors.New("invalid active scan input")
	ErrScopeDenied  = errors.New("active scan target outside scope")
	ErrBusy         = errors.New("active scan already running")
)

type ExchangeReader interface {
	GetExchange(context.Context, int64) (*store.Exchange, error)
	CreateActiveScanRun(context.Context, int64) (int64, error)
	AppendActiveScanProbe(context.Context, int64, int, store.ActiveScanProbe) error
	FinishActiveScanRun(context.Context, int64, string, string) error
}
type Scope interface{ Allows(string) bool }

type Request struct {
	HistoryID   int64 `json:"historyId"`
	MaxProbes   int   `json:"maxProbes"`
	Acknowledge bool  `json:"acknowledge"`
}

type Probe struct {
	Parameter string `json:"parameter"`
	Status    int    `json:"status"`
	Reflected bool   `json:"reflected"`
	Partial   bool   `json:"partial"`
	Error     string `json:"error,omitempty"`
}

type Report struct {
	RunID         int64   `json:"runId"`
	State         string  `json:"state"`
	HistoryID     int64   `json:"historyId"`
	ProbeCount    int     `json:"probeCount"`
	StoppedReason string  `json:"stoppedReason,omitempty"`
	Probes        []Probe `json:"probes"`
}

type Scanner struct {
	history ExchangeReader
	scope   Scope
	sender  repeater.Sender
	busy    chan struct{}
}

func New(history ExchangeReader, scope Scope, sender repeater.Sender) (*Scanner, error) {
	if history == nil || scope == nil || sender == nil {
		return nil, errors.New("active scanner dependencies unavailable")
	}
	return &Scanner{history: history, scope: scope, sender: sender, busy: make(chan struct{}, 1)}, nil
}

func (s *Scanner) Scan(ctx context.Context, input Request) (report Report, err error) {
	if input.HistoryID < 1 || input.MaxProbes < 1 || input.MaxProbes > 5 || !input.Acknowledge {
		return Report{}, ErrInvalidInput
	}
	select {
	case s.busy <- struct{}{}:
		defer func() { <-s.busy }()
	default:
		return Report{}, ErrBusy
	}
	exchange, err := s.history.GetExchange(ctx, input.HistoryID)
	if err != nil {
		return Report{}, err
	}
	if exchange.Method != "GET" || !exchange.InScope || exchange.Error || (exchange.Scheme != "http" && exchange.Scheme != "https") || len(exchange.Path) > 4096 || len(exchange.Query) > 4096 {
		return Report{}, ErrInvalidInput
	}
	base := &url.URL{Scheme: exchange.Scheme, Host: exchange.Host, Path: exchange.Path}
	if base.Host == "" || base.User != nil || len(base.String()) > 8192 {
		return Report{}, ErrInvalidInput
	}
	if !s.scope.Allows(base.String()) {
		return Report{}, ErrScopeDenied
	}
	values, err := url.ParseQuery(exchange.Query)
	if err != nil || len(values) == 0 {
		return Report{}, ErrInvalidInput
	}
	names := make([]string, 0, len(values))
	for name := range values {
		if name != "" && len(name) <= 256 {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return Report{}, ErrInvalidInput
	}
	if len(names) > input.MaxProbes {
		names = names[:input.MaxProbes]
	}
	runID, err := s.history.CreateActiveScanRun(ctx, input.HistoryID)
	if err != nil {
		return Report{}, err
	}
	report = Report{RunID: runID, HistoryID: input.HistoryID, Probes: make([]Probe, 0, len(names))}
	defer func() {
		state := "completed"
		if report.StoppedReason == "scope_revoked" {
			state = "scope_revoked"
		}
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				state = "cancelled"
			} else {
				state = "failed"
			}
			report.StoppedReason = state
		}
		persistCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if finishErr := s.history.FinishActiveScanRun(persistCtx, runID, state, report.StoppedReason); finishErr != nil && err == nil {
			err = finishErr
		}
		report.State = state
	}()
	for index, name := range names {
		if index > 0 {
			timer := time.NewTimer(500 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return report, ctx.Err()
			case <-timer.C:
			}
		}
		markerBytes := make([]byte, 12)
		if _, err := rand.Read(markerBytes); err != nil {
			return report, err
		}
		marker := "scan-" + hex.EncodeToString(markerBytes)
		target := *base
		target.RawQuery = url.Values{name: []string{marker}}.Encode()
		if len(target.String()) > 8192 {
			return report, ErrInvalidInput
		}
		if !s.scope.Allows(target.String()) {
			report.StoppedReason = "scope_revoked"
			return report, nil
		}
		probe := Probe{Parameter: name}
		result, sendErr := s.sender.Send(ctx, repeater.SendRequest{Method: "GET", URL: target.String(), Headers: map[string][]string{"User-Agent": {"BurpSuiteClone-ActiveScanner/1"}}}, repeater.SendOptions{Timeout: 5 * time.Second, BodyLimitBytes: 64 << 10})
		if sendErr != nil {
			if ctx.Err() != nil {
				return report, ctx.Err()
			}
			probe.Error = "request_failed"
		} else {
			probe.Status = result.Status
			probe.Reflected = bytes.Contains(result.Body, []byte(marker))
			probe.Partial = result.Truncated
		}
		persistCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		persistErr := s.history.AppendActiveScanProbe(persistCtx, runID, report.ProbeCount, store.ActiveScanProbe{Parameter: probe.Parameter, Status: probe.Status, Reflected: probe.Reflected, Partial: probe.Partial, Error: probe.Error})
		cancel()
		if persistErr != nil {
			return report, persistErr
		}
		report.Probes = append(report.Probes, probe)
		report.ProbeCount++
	}
	return report, nil
}
