package activechecks

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/repeater"
	"github.com/lutzifer/burpsuite-clone/internal/scancreds"
	"github.com/lutzifer/burpsuite-clone/internal/store"
)

var (
	ErrInvalidInput = errors.New("invalid active check input")
	ErrScopeDenied  = errors.New("active check target outside scope")
	ErrBusy         = errors.New("active check already running")
)

type History interface {
	GetExchange(context.Context, int64) (*store.Exchange, error)
}
type CrawlReader interface {
	GetCrawlRun(context.Context, int64) (store.CrawlRun, error)
}
type Scope interface{ Allows(string) bool }
type Request struct {
	CrawlID     int64                 `json:"crawlId"`
	Acknowledge bool                  `json:"acknowledge"`
	Session     scancreds.Credentials `json:"session"`
}
type Report struct {
	RunID           int64  `json:"runId"`
	State           string `json:"state"`
	MaximumRequests int    `json:"maximumRequests"`
}
type candidate struct {
	url, source string
	names       []string
}
type Service struct {
	history    History
	crawls     CrawlReader
	repository store.ActiveCheckStore
	scope      Scope
	sender     repeater.Sender
	mu         sync.Mutex
	runID      int64
	cancel     context.CancelFunc
}

func New(history History, crawls CrawlReader, repository store.ActiveCheckStore, scope Scope, sender repeater.Sender) (*Service, error) {
	if history == nil || crawls == nil || repository == nil || scope == nil || sender == nil {
		return nil, errors.New("active check dependencies unavailable")
	}
	return &Service{history: history, crawls: crawls, repository: repository, scope: scope, sender: sender}, nil
}
func sameOrigin(raw string, seed *url.URL) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == seed.Scheme && strings.EqualFold(u.Host, seed.Host) && u.User == nil
}
func chooseCandidates(run store.CrawlRun, seed *url.URL, scope Scope) []candidate {
	chosen := []candidate{}
	seen := map[string]bool{}
	add := func(raw, source string, names []string) {
		if len(chosen) >= 25 || !sameOrigin(raw, seed) || !scope.Allows(raw) {
			return
		}
		filtered := []string{}
		for _, name := range names {
			if len(filtered) >= 5 {
				break
			}
			if name == "" || len(name) > 256 {
				continue
			}
			key := source + "\x00" + raw + "\x00" + name
			if seen[key] {
				continue
			}
			seen[key] = true
			filtered = append(filtered, name)
		}
		if len(filtered) > 0 {
			chosen = append(chosen, candidate{url: raw, source: source, names: filtered})
		}
	}
	for _, page := range run.Pages {
		add(page.URL, "query", page.QueryNames)
	}
	for _, form := range run.Forms {
		if !strings.EqualFold(form.Method, "GET") {
			continue
		}
		names := []string{}
		for _, field := range form.Fields {
			typ := strings.ToLower(field.Type)
			if typ == "hidden" || typ == "password" || typ == "file" {
				continue
			}
			names = append(names, field.Name)
		}
		add(form.ActionURL, "get_form", names)
	}
	return chosen
}
func (s *Service) Start(ctx context.Context, input Request) (Report, error) {
	if input.CrawlID < 1 || !input.Acknowledge || input.Session.Validate() != nil {
		return Report{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return Report{}, ErrBusy
	}
	run, err := s.crawls.GetCrawlRun(ctx, input.CrawlID)
	if err != nil {
		return Report{}, err
	}
	if run.State != "completed" {
		return Report{}, ErrInvalidInput
	}
	seed, err := s.history.GetExchange(ctx, run.HistoryID)
	if err != nil {
		return Report{}, err
	}
	if seed.Method != "GET" || !seed.InScope || seed.Error || (seed.Scheme != "http" && seed.Scheme != "https") {
		return Report{}, ErrInvalidInput
	}
	seedURL := &url.URL{Scheme: seed.Scheme, Host: seed.Host, Path: seed.Path}
	if seedURL.Host == "" || !s.scope.Allows(seedURL.String()) {
		return Report{}, ErrScopeDenied
	}
	candidates := chooseCandidates(run, seedURL, s.scope)
	if len(candidates) == 0 {
		return Report{}, ErrInvalidInput
	}
	id, err := s.repository.CreateActiveCheckRun(ctx, input.CrawlID)
	if err != nil {
		return Report{}, err
	}
	runCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	s.runID = id
	s.cancel = cancel
	maximum := 0
	for _, item := range candidates {
		maximum += len(item.names)
	}
	if maximum > 100 {
		maximum = 100
	}
	go s.run(runCtx, id, candidates, input.Session)
	return Report{RunID: id, State: "running", MaximumRequests: maximum}, nil
}
func (s *Service) Cancel(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id < 1 || s.runID != id || s.cancel == nil {
		return sql.ErrNoRows
	}
	s.cancel()
	return nil
}
func (s *Service) run(ctx context.Context, id int64, candidates []candidate, session scancreds.Credentials) {
	state, reason := "completed", ""
	defer func() {
		persistCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.repository.FinishActiveCheckRun(persistCtx, id, state, reason)
		s.mu.Lock()
		s.cancel()
		s.cancel = nil
		s.runID = 0
		s.mu.Unlock()
	}()
	lastSend := time.Time{}
	sent := 0
	for _, item := range candidates {
		for _, name := range item.names {
			if sent >= 100 {
				return
			}
			if delay := time.Until(lastSend.Add(500 * time.Millisecond)); delay > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					state = "cancelled"
					reason = "cancelled"
					return
				case <-timer.C:
				}
			}
			if ctx.Err() != nil {
				state = "cancelled"
				reason = "cancelled"
				return
			}
			markerBytes := make([]byte, 12)
			if _, err := rand.Read(markerBytes); err != nil {
				state = "failed"
				reason = "marker_failed"
				return
			}
			marker := "scan-" + hex.EncodeToString(markerBytes)
			target, err := url.Parse(item.url)
			if err != nil {
				state = "failed"
				reason = "invalid_target"
				return
			}
			target.RawQuery = url.Values{name: {marker}}.Encode()
			if len(target.String()) > 4096 {
				state = "failed"
				reason = "target_too_long"
				return
			}
			if !s.scope.Allows(target.String()) {
				state = "scope_revoked"
				reason = "scope_revoked"
				return
			}
			lastSend = time.Now()
			result, sendErr := s.sender.Send(ctx, repeater.SendRequest{Method: "GET", URL: target.String(), Headers: session.Headers("BurpSuiteClone-ActiveChecks/1")}, repeater.SendOptions{Timeout: 5 * time.Second, BodyLimitBytes: 64 << 10})
			observation := store.ActiveCheckObservation{URL: item.url, Source: item.source, Parameter: name, Context: "unknown"}
			if sendErr != nil {
				if ctx.Err() != nil {
					state = "cancelled"
					reason = "cancelled"
					return
				}
				observation.Error = "request_failed"
			} else {
				classified := Classify(result.Body, result.ContentType, marker, result.Truncated)
				observation.Status = result.Status
				observation.Found = classified.Found
				observation.Context = classified.Context
				observation.Partial = classified.Partial
			}
			persistCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			err = s.repository.AppendActiveCheck(persistCtx, id, observation)
			cancel()
			if err != nil {
				state = "failed"
				reason = "storage_failed"
				return
			}
			sent++
		}
	}
}
