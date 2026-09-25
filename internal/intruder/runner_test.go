package intruder

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/repeater"
)

type failingResultStore struct{ *runnerStore }

func (s *failingResultStore) AppendResult(context.Context, string, Result) (Job, error) {
	return Job{}, errors.New("disk full")
}

func TestRunnerPausesBeforeNextRequestOnPersistenceFailure(t *testing.T) {
	store := &failingResultStore{runnerStore: &runnerStore{}}
	scope := &serviceScope{}
	scope.allowed.Store(true)
	sender := &serviceSender{}
	s, err := NewService(context.Background(), store, scope, sender)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	draft := serviceDraft()
	draft.Config.PayloadSets[0].Payloads = [][]byte{[]byte("a"), []byte("b")}
	draft.Config.RequestLimit = 2
	draft.Config.RatePerSecond = 100
	job, err := s.Create(context.Background(), draft)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Start(context.Background(), job.ID, job.Revision); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(time.Second)
	for {
		current, results := store.snapshot()
		if current.State == StatePaused {
			if current.StateReason != "persistence_error" || current.NextSequence != 0 || len(results) != 0 || sender.calls.Load() != 1 {
				t.Fatalf("job=%+v results=%+v sends=%d", current, results, sender.calls.Load())
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("job did not pause: %+v", current)
		case <-time.After(time.Millisecond):
		}
	}
}

func TestRunnerClosePausesActiveJob(t *testing.T) {
	store := &runnerStore{}
	scope := &serviceScope{}
	scope.allowed.Store(true)
	sender := &blockingRunnerSender{entered: make(chan struct{}), release: make(chan struct{})}
	s, err := NewService(context.Background(), store, scope, sender)
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.Create(context.Background(), serviceDraft())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Start(context.Background(), job.ID, job.Revision); err != nil {
		t.Fatal(err)
	}
	select {
	case <-sender.entered:
	case <-time.After(time.Second):
		t.Fatal("send did not start")
	}
	s.Close()
	current, results := store.snapshot()
	if current.State != StatePaused || current.NextSequence != 1 || len(results) != 1 || results[0].ErrorCategory != "cancelled" {
		t.Fatalf("job=%+v results=%+v", current, results)
	}
	if _, err = s.Resume(context.Background(), current.ID, current.Revision); err == nil {
		t.Fatal("closed service accepted resume")
	}
}

type revokingScope struct{ checks atomic.Int64 }

func (s *revokingScope) Allows(string) bool { return s.checks.Add(1) < 5 }

func TestRunnerRechecksScopeImmediatelyBeforeSend(t *testing.T) {
	store := &runnerStore{}
	scope := &revokingScope{}
	sender := &serviceSender{}
	s, err := NewService(context.Background(), store, scope, sender)
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
		current, results := store.snapshot()
		if current.State == StatePaused {
			if current.StateReason != "scope_revoked" || sender.calls.Load() != 0 || len(results) != 1 || results[0].ErrorCategory != "scope_revoked" {
				t.Fatalf("job=%+v results=%+v sends=%d", current, results, sender.calls.Load())
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("job did not pause: %+v", current)
		case <-time.After(time.Millisecond):
		}
	}
}

func TestRunnerAbortRecordsCancelledRequestOnce(t *testing.T) {
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
	job, err = s.Abort(context.Background(), job.ID, job.Revision)
	if err != nil || job.State != StateAborting {
		t.Fatalf("abort = %+v, %v", job, err)
	}
	deadline := time.After(time.Second)
	for {
		current, results := store.snapshot()
		if current.State == StateAborted {
			if len(results) != 1 || results[0].Sequence != 0 || results[0].ErrorCategory != "cancelled" || current.NextSequence != 1 {
				t.Fatalf("job=%+v results=%+v", current, results)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("job did not abort: %+v", current)
		case <-time.After(time.Millisecond):
		}
	}
}

type runnerStore struct {
	Store
	mu      sync.Mutex
	job     Job
	results []Result
}

func (s *runnerStore) RecoverRunning(context.Context) error { return nil }
func (s *runnerStore) CreateDraft(_ context.Context, d Draft) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.job = Job{ID: d.ID, Config: d.Config, State: StateDraft, Revision: 1, TotalRequests: int64(len(d.Config.PayloadSets[0].Payloads))}
	return s.job, nil
}
func (s *runnerStore) GetJob(_ context.Context, id string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.job, nil
}
func (s *runnerStore) Transition(_ context.Context, id string, revision int64, from, to State, reason string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job.Revision != revision || s.job.State != from {
		return Job{}, ErrRevisionConflict
	}
	s.job.Revision++
	s.job.State = to
	s.job.StateReason = reason
	return s.job, nil
}
func (s *runnerStore) AppendResult(_ context.Context, id string, r Result) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Sequence != s.job.NextSequence {
		return Job{}, ErrSequenceConflict
	}
	s.results = append(s.results, r)
	s.job.NextSequence++
	s.job.CompletedCount++
	s.job.Revision++
	return s.job, nil
}

type reorderedSender struct {
	entered chan string
	release map[string]chan struct{}
}

type resumeSender struct {
	firstStarted chan struct{}
	releaseFirst chan struct{}
	calls        atomic.Int64
}

func (s *resumeSender) Send(ctx context.Context, _ repeater.SendRequest, _ repeater.SendOptions) (repeater.SendResult, error) {
	if s.calls.Add(1) == 1 {
		close(s.firstStarted)
		select {
		case <-s.releaseFirst:
		case <-ctx.Done():
			return repeater.SendResult{}, ctx.Err()
		}
	}
	return repeater.SendResult{Status: 200}, nil
}

func TestRunnerResumeStartsAtPersistedSequence(t *testing.T) {
	store := &runnerStore{}
	scope := &serviceScope{}
	scope.allowed.Store(true)
	sender := &resumeSender{firstStarted: make(chan struct{}), releaseFirst: make(chan struct{})}
	s, err := NewService(context.Background(), store, scope, sender)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	draft := serviceDraft()
	draft.Config.PayloadSets[0].Payloads = [][]byte{[]byte("a"), []byte("b")}
	draft.Config.RequestLimit = 2
	draft.Config.RatePerSecond = 100
	job, err := s.Create(context.Background(), draft)
	if err != nil {
		t.Fatal(err)
	}
	job, err = s.Start(context.Background(), job.ID, job.Revision)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-sender.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first send did not start")
	}
	if _, err = s.Pause(context.Background(), job.ID, job.Revision); err != nil {
		t.Fatal(err)
	}
	close(sender.releaseFirst)
	deadline := time.After(time.Second)
	for {
		current, _ := store.snapshot()
		if current.State == StatePaused {
			job = current
			break
		}
		select {
		case <-deadline:
			t.Fatal("job did not pause")
		case <-time.After(time.Millisecond):
		}
	}
	for {
		_, err = s.Resume(context.Background(), job.ID, job.Revision)
		if err == nil {
			break
		}
		if !errors.Is(err, ErrStateConflict) {
			t.Fatal(err)
		}
		select {
		case <-deadline:
			t.Fatal("previous worker did not drain")
		case <-time.After(time.Millisecond):
		}
	}
	for {
		current, results := store.snapshot()
		if current.State == StateCompleted {
			if sender.calls.Load() != 2 || len(results) != 2 || results[0].Sequence != 0 || results[1].Sequence != 1 {
				t.Fatalf("job=%+v results=%+v sends=%d", current, results, sender.calls.Load())
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("resumed job did not finish")
		case <-time.After(time.Millisecond):
		}
	}
}

func (s *reorderedSender) Send(ctx context.Context, request repeater.SendRequest, _ repeater.SendOptions) (repeater.SendResult, error) {
	key := request.URL[len(request.URL)-1:]
	s.entered <- key
	select {
	case <-s.release[key]:
		return repeater.SendResult{Status: 200, Body: []byte(key), Size: 1}, nil
	case <-ctx.Done():
		return repeater.SendResult{}, ctx.Err()
	}
}

func TestRunnerConcurrentSendsPersistInSequence(t *testing.T) {
	store := &runnerStore{}
	scope := &serviceScope{}
	scope.allowed.Store(true)
	sender := &reorderedSender{entered: make(chan string, 2), release: map[string]chan struct{}{"a": make(chan struct{}), "b": make(chan struct{})}}
	s, err := NewService(context.Background(), store, scope, sender)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	draft := serviceDraft()
	draft.Config.PayloadSets[0].Payloads = [][]byte{[]byte("a"), []byte("b")}
	draft.Config.RequestLimit = 2
	draft.Config.Concurrency = 2
	draft.Config.RatePerSecond = 100
	job, err := s.Create(context.Background(), draft)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Start(context.Background(), job.ID, job.Revision); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-sender.entered:
		case <-time.After(time.Second):
			t.Fatal("second send did not start concurrently")
		}
	}
	close(sender.release["b"])
	time.Sleep(10 * time.Millisecond)
	close(sender.release["a"])
	deadline := time.After(time.Second)
	for {
		current, results := store.snapshot()
		if current.State == StateCompleted {
			if len(results) != 2 || results[0].Sequence != 0 || results[1].Sequence != 1 {
				t.Fatalf("results = %+v", results)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("job did not complete: %+v", current)
		case <-time.After(time.Millisecond):
		}
	}
}

func TestRunnerDrainsStartedRequestsBeforeInvalidRequestFailure(t *testing.T) {
	store := &runnerStore{}
	scope := &serviceScope{}
	scope.allowed.Store(true)
	sender := &serviceSender{}
	s, err := NewService(context.Background(), store, scope, sender)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	draft := serviceDraft()
	draft.Config.PayloadSets[0].Payloads = [][]byte{[]byte("a"), []byte("\r\n")}
	draft.Config.RequestLimit = 2
	draft.Config.Concurrency = 2
	draft.Config.RatePerSecond = 100
	job, err := s.Create(context.Background(), draft)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Start(context.Background(), job.ID, job.Revision); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(time.Second)
	for {
		current, results := store.snapshot()
		if current.State == StateFailed {
			if current.StateReason != "invalid_request" || len(results) != 1 || results[0].Sequence != 0 || sender.calls.Load() != 1 {
				t.Fatalf("job=%+v results=%+v sends=%d", current, results, sender.calls.Load())
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("job did not fail: %+v", current)
		case <-time.After(time.Millisecond):
		}
	}
}
func (s *runnerStore) snapshot() (Job, []Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.job, append([]Result(nil), s.results...)
}

type runnerSender struct {
	scope *serviceScope
	calls int
}

type blockingRunnerSender struct{ entered, release chan struct{} }

func (s *blockingRunnerSender) Send(ctx context.Context, _ repeater.SendRequest, _ repeater.SendOptions) (repeater.SendResult, error) {
	close(s.entered)
	select {
	case <-s.release:
		return repeater.SendResult{Status: 200}, nil
	case <-ctx.Done():
		return repeater.SendResult{}, ctx.Err()
	}
}

func (s *runnerSender) Send(_ context.Context, _ repeater.SendRequest, _ repeater.SendOptions) (repeater.SendResult, error) {
	s.calls++
	s.scope.allowed.Store(false)
	return repeater.SendResult{Status: 200, Body: []byte("ok"), Size: 2}, nil
}

func TestRunnerStopsAfterScopeRevocation(t *testing.T) {
	store := &runnerStore{}
	scope := &serviceScope{}
	scope.allowed.Store(true)
	sender := &runnerSender{scope: scope}
	s, err := NewService(context.Background(), store, scope, sender)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	draft := serviceDraft()
	draft.Config.PayloadSets[0].Payloads = [][]byte{[]byte("a"), []byte("b")}
	draft.Config.RequestLimit = 2
	draft.Config.RatePerSecond = 100
	job, err := s.Create(context.Background(), draft)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Start(context.Background(), job.ID, job.Revision)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		got, results := store.snapshot()
		if got.State == StatePaused {
			if got.StateReason != "scope_revoked" || len(results) != 1 || sender.calls != 1 {
				t.Fatalf("job=%+v results=%d calls=%d", got, len(results), sender.calls)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("job did not pause: %+v", got)
		case <-time.After(time.Millisecond):
		}
	}
}
