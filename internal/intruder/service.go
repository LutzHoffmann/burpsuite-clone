package intruder

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"

	"github.com/lutzifer/burpsuite-clone/internal/events"
	"github.com/lutzifer/burpsuite-clone/internal/repeater"
)

var ErrScopeDenied = errors.New("intruder target is outside current scope")
var ErrJobLimit = errors.New("intruder running job limit reached")

type ScopeClassifier interface{ Allows(string) bool }
type EventPublisher interface{ Publish(events.Event) }

type Service struct {
	store   Store
	scope   ScopeClassifier
	sender  repeater.Sender
	events  EventPublisher
	mu      sync.Mutex
	running map[string]*runHandle
	closed  bool
	work    sync.WaitGroup
}

type runHandle struct {
	cancel context.CancelFunc
	done   chan struct{}
	intent State
}

func NewService(ctx context.Context, store Store, scope ScopeClassifier, sender repeater.Sender, publishers ...EventPublisher) (*Service, error) {
	if store == nil || scope == nil || sender == nil {
		return nil, errors.New("intruder dependencies unavailable")
	}
	if err := store.RecoverRunning(ctx); err != nil {
		return nil, err
	}
	service := &Service{store: store, scope: scope, sender: sender, running: make(map[string]*runHandle)}
	if len(publishers) > 0 {
		service.events = publishers[0]
	}
	return service, nil
}

func (s *Service) publish(kind string, data map[string]interface{}) {
	if s.events != nil {
		s.events.Publish(events.Event{Type: kind, Data: data})
	}
}

func (s *Service) publishJob(job Job) {
	s.publish("intruder.job.updated", map[string]interface{}{"jobId": job.ID, "state": job.State, "revision": job.Revision})
}

func (s *Service) Create(ctx context.Context, draft Draft) (Job, error) {
	if _, err := ValidateConfig(draft.Config); err != nil {
		return Job{}, err
	}
	if !s.scope.Allows(draft.Config.Template.URL) {
		return Job{}, ErrScopeDenied
	}
	if draft.ID == "" {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return Job{}, err
		}
		draft.ID = hex.EncodeToString(b)
	}
	job, err := s.store.CreateDraft(ctx, draft)
	if err == nil {
		s.publishJob(job)
	}
	return job, err
}

func (s *Service) Update(ctx context.Context, id string, revision int64, cfg Config) (Job, error) {
	if _, err := ValidateConfig(cfg); err != nil {
		return Job{}, err
	}
	if !s.scope.Allows(cfg.Template.URL) {
		return Job{}, ErrScopeDenied
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Job{}, errors.New("intruder service closed")
	}
	job, err := s.store.GetJob(ctx, id)
	if err != nil {
		return Job{}, err
	}
	if job.Revision != revision {
		return Job{}, ErrRevisionConflict
	}
	if job.State != StateDraft {
		return Job{}, ErrStateConflict
	}
	updated, err := s.store.ReplaceDraft(ctx, id, revision, cfg)
	if err == nil {
		s.publishJob(updated)
	}
	return updated, err
}

func (s *Service) List(ctx context.Context) ([]JobSummary, error) {
	return s.store.ListJobs(ctx)
}

func (s *Service) Get(ctx context.Context, id string) (Job, error) {
	return s.store.GetJob(ctx, id)
}

func (s *Service) ListResults(ctx context.Context, id string, query ResultQuery) (ResultPage, error) {
	return s.store.ListResults(ctx, id, query)
}

func (s *Service) GetResult(ctx context.Context, id string, sequence int64) (Result, error) {
	return s.store.GetResult(ctx, id, sequence)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("intruder service closed")
	}
	if _, running := s.running[id]; running {
		return ErrStateConflict
	}
	err := s.store.DeleteJob(ctx, id)
	if err == nil {
		s.publish("intruder.job.updated", map[string]interface{}{"jobId": id, "state": "deleted"})
	}
	return err
}

func (s *Service) Start(ctx context.Context, id string, revision int64) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Job{}, errors.New("intruder service closed")
	}
	if _, exists := s.running[id]; exists {
		return Job{}, ErrStateConflict
	}
	if len(s.running) >= 2 {
		return Job{}, ErrJobLimit
	}
	job, err := s.store.GetJob(ctx, id)
	if err != nil {
		return Job{}, err
	}
	if job.Revision != revision {
		return Job{}, ErrRevisionConflict
	}
	if job.State != StateDraft && job.State != StatePaused {
		return Job{}, ErrStateConflict
	}
	if !s.scope.Allows(job.Config.Template.URL) {
		return Job{}, ErrScopeDenied
	}
	if _, err := ValidateConfig(job.Config); err != nil {
		return Job{}, err
	}
	started, err := s.store.Transition(ctx, id, revision, job.State, StateRunning, "")
	if err != nil {
		return Job{}, err
	}
	s.publishJob(started)
	runCtx, cancel := context.WithCancel(context.Background())
	h := &runHandle{cancel: cancel, done: make(chan struct{})}
	s.running[id] = h
	s.work.Add(1)
	go s.run(runCtx, started, h)
	return started, nil
}

func (s *Service) Resume(ctx context.Context, id string, revision int64) (Job, error) {
	return s.Start(ctx, id, revision)
}

func (s *Service) Pause(ctx context.Context, id string, revision int64) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.running[id]
	if !ok {
		return Job{}, ErrStateConflict
	}
	job, err := s.store.Transition(ctx, id, revision, StateRunning, StatePausing, "operator")
	if err != nil {
		return Job{}, err
	}
	h.intent = StatePaused
	s.publishJob(job)
	return job, nil
}

func (s *Service) Abort(ctx context.Context, id string, revision int64) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, err := s.store.GetJob(ctx, id)
	if err != nil {
		return Job{}, err
	}
	if job.Revision != revision {
		return Job{}, ErrRevisionConflict
	}
	if job.State == StatePaused {
		aborted, err := s.store.Transition(ctx, id, revision, StatePaused, StateAborted, "operator")
		if err == nil {
			s.publishJob(aborted)
		}
		return aborted, err
	}
	h, ok := s.running[id]
	if !ok || (job.State != StateRunning && job.State != StatePausing) {
		return Job{}, ErrStateConflict
	}
	job, err = s.store.Transition(ctx, id, revision, job.State, StateAborting, "operator")
	if err != nil {
		return Job{}, err
	}
	h.intent = StateAborted
	h.cancel()
	s.publishJob(job)
	return job, nil
}

func (s *Service) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	for _, h := range s.running {
		h.intent = StatePaused
		h.cancel()
	}
	s.mu.Unlock()
	s.work.Wait()
}
