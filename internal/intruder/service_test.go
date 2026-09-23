package intruder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/events"
	"github.com/lutzifer/burpsuite-clone/internal/repeater"
)

type recordingPublisher struct {
	mu     sync.Mutex
	events []events.Event
}

func (p *recordingPublisher) Publish(e events.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, e)
}

func TestServicePublishesIdentifierOnlyEvents(t *testing.T) {
	store := &runnerStore{}
	scope := &serviceScope{}
	scope.allowed.Store(true)
	publisher := &recordingPublisher{}
	s, err := NewService(context.Background(), store, scope, &serviceSender{}, publisher)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	job, err := s.Create(context.Background(), serviceDraft())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Start(context.Background(), job.ID, job.Revision); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(time.Second)
	for {
		current, _ := store.snapshot()
		if current.State == StateCompleted {
			break
		}
		select {
		case <-deadline:
			t.Fatal("job did not complete")
		case <-time.After(time.Millisecond):
		}
	}
	s.Close()
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	seen := map[string]bool{}
	for _, event := range publisher.events {
		seen[event.Type] = true
		encoded, err := json.Marshal(event.Data)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(encoded, []byte("example.test")) || bytes.Contains(encoded, []byte("payload")) || bytes.Contains(encoded, []byte("headers")) {
			t.Fatalf("sensitive event: %s", encoded)
		}
	}
	for _, kind := range []string{"intruder.job.updated", "intruder.progress", "intruder.result.created"} {
		if !seen[kind] {
			t.Fatalf("missing %s: %+v", kind, publisher.events)
		}
	}
}

type serviceStore struct {
	Store
	job       Job
	recovered atomic.Bool
}

func (s *serviceStore) CreateDraft(_ context.Context, draft Draft) (Job, error) {
	s.job = Job{ID: draft.ID, Config: draft.Config, State: StateDraft, Revision: 1, ScopeVersion: draft.ScopeVersion}
	return s.job, nil
}
func (s *serviceStore) GetJob(_ context.Context, id string) (Job, error) {
	if s.job.ID != id {
		return Job{}, errors.New("missing")
	}
	return s.job, nil
}
func (s *serviceStore) ReplaceDraft(_ context.Context, id string, revision int64, cfg Config) (Job, error) {
	if s.job.ID != id || s.job.Revision != revision || s.job.State != StateDraft {
		return Job{}, ErrRevisionConflict
	}
	s.job.Config = cfg
	s.job.Revision++
	return s.job, nil
}
func (s *serviceStore) Transition(_ context.Context, id string, revision int64, from, to State, reason string) (Job, error) {
	if s.job.ID != id || s.job.Revision != revision || s.job.State != from {
		return Job{}, ErrRevisionConflict
	}
	s.job.State = to
	s.job.StateReason = reason
	s.job.Revision++
	return s.job, nil
}
func (s *serviceStore) RecoverRunning(context.Context) error { s.recovered.Store(true); return nil }

type serviceScope struct{ allowed atomic.Bool }

func (s *serviceScope) Allows(string) bool { return s.allowed.Load() }

type serviceSender struct{ calls atomic.Int64 }

func (s *serviceSender) Send(context.Context, repeater.SendRequest, repeater.SendOptions) (repeater.SendResult, error) {
	s.calls.Add(1)
	return repeater.SendResult{Status: 200}, nil
}

func serviceDraft() Draft {
	return Draft{ID: "job-one", ScopeVersion: 1, Config: Config{
		Attack: AttackSniper, Template: Template{Method: "GET", URL: "http://example.test/?q=x", Raw: []byte("GET /?q=x HTTP/1.1\r\nHost: example.test\r\n\r\n")},
		Positions:    []Position{{ID: "p1", Start: 8, End: 9, PayloadSetID: "s1"}},
		PayloadSets:  []PayloadSet{{ID: "s1", Payloads: [][]byte{[]byte("a")}}},
		RequestLimit: 1, Concurrency: 1, RatePerSecond: 1, Timeout: time.Second,
	}}
}

func TestServiceRejectsOutOfScopeCreationAndStart(t *testing.T) {
	store := &serviceStore{}
	scope := &serviceScope{}
	sender := &serviceSender{}
	service, err := NewService(context.Background(), store, scope, sender)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if _, err := service.Create(context.Background(), serviceDraft()); !errors.Is(err, ErrScopeDenied) {
		t.Fatalf("out-of-scope create = %v", err)
	}
	scope.allowed.Store(true)
	job, err := service.Create(context.Background(), serviceDraft())
	if err != nil {
		t.Fatal(err)
	}
	scope.allowed.Store(false)
	if _, err := service.Start(context.Background(), job.ID, job.Revision); !errors.Is(err, ErrScopeDenied) {
		t.Fatalf("out-of-scope start = %v", err)
	}
	if sender.calls.Load() != 0 {
		t.Fatalf("sent %d requests", sender.calls.Load())
	}
}

func TestServiceRecoversWithoutSending(t *testing.T) {
	store := &serviceStore{}
	scope := &serviceScope{}
	sender := &serviceSender{}
	service, err := NewService(context.Background(), store, scope, sender)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if !store.recovered.Load() || sender.calls.Load() != 0 {
		t.Fatalf("recovery=%t sends=%d", store.recovered.Load(), sender.calls.Load())
	}
}

func TestServiceUpdateChecksScopeAndDraftState(t *testing.T) {
	store := &serviceStore{}
	scope := &serviceScope{}
	scope.allowed.Store(true)
	s, err := NewService(context.Background(), store, scope, &serviceSender{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	job, err := s.Create(context.Background(), serviceDraft())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Update(context.Background(), job.ID, job.Revision+1, job.Config); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale revision: %v", err)
	}
	scope.allowed.Store(false)
	if _, err = s.Update(context.Background(), job.ID, job.Revision, job.Config); !errors.Is(err, ErrScopeDenied) {
		t.Fatalf("out of scope: %v", err)
	}
	scope.allowed.Store(true)
	updated, err := s.Update(context.Background(), job.ID, job.Revision, job.Config)
	if err != nil || updated.Revision != job.Revision+1 {
		t.Fatalf("update = %+v, %v", updated, err)
	}
	store.job.State = StateCompleted
	if _, err = s.Update(context.Background(), job.ID, updated.Revision, job.Config); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("completed update: %v", err)
	}
}

func TestServiceDoesNotResumeWhilePreviousWorkerIsDraining(t *testing.T) {
	store := &runnerStore{}
	scope := &serviceScope{}
	scope.allowed.Store(true)
	s, err := NewService(context.Background(), store, scope, &serviceSender{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	job, err := s.Create(context.Background(), serviceDraft())
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.running[job.ID] = &runHandle{done: make(chan struct{})}
	s.mu.Unlock()
	store.mu.Lock()
	store.job.State = StatePaused
	store.job.Revision++
	job = store.job
	store.mu.Unlock()
	if _, err = s.Resume(context.Background(), job.ID, job.Revision); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("resume with draining worker = %v", err)
	}
	s.mu.Lock()
	delete(s.running, job.ID)
	s.mu.Unlock()
}

func TestServicePauseAndAbortControlRunningJob(t *testing.T) {
	store := &runnerStore{}
	scope := &serviceScope{}
	scope.allowed.Store(true)
	sender := &blockingRunnerSender{entered: make(chan struct{}), release: make(chan struct{})}
	s, err := NewService(context.Background(), store, scope, sender)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	job, err := s.Create(context.Background(), serviceDraft())
	if err != nil {
		t.Fatal(err)
	}
	job, err = s.Start(context.Background(), job.ID, job.Revision)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-sender.entered:
	case <-time.After(time.Second):
		t.Fatal("send did not start")
	}
	job, err = s.Pause(context.Background(), job.ID, job.Revision)
	if err != nil || job.State != StatePausing {
		t.Fatalf("pause = %+v, %v", job, err)
	}
	close(sender.release)
	deadline := time.After(time.Second)
	for {
		job, _ = store.snapshot()
		if job.State == StatePaused {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("not paused: %+v", job)
		case <-time.After(time.Millisecond):
		}
	}
	job, err = s.Abort(context.Background(), job.ID, job.Revision)
	if err != nil || job.State != StateAborted {
		t.Fatalf("abort paused = %+v, %v", job, err)
	}
}
