package intruder

import (
	"context"
	"errors"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/repeater"
)

func (s *Service) run(ctx context.Context, job Job, h *runHandle) {
	defer func() {
		if recover() != nil {
			s.finishRun(job, StateFailed, "worker_panic")
		}
		s.mu.Lock()
		delete(s.running, job.ID)
		close(h.done)
		s.mu.Unlock()
		s.work.Done()
	}()
	iterator, err := NewIterator(job.Config, job.NextSequence)
	if err != nil {
		s.finishRun(job, StateFailed, "invalid_configuration")
		return
	}
	interval := time.Duration(float64(time.Second) / job.Config.RatePerSecond)
	nextStart := time.Now()
	var lastProgress time.Time
	for {
		if intent := s.runIntent(h); intent != "" {
			s.finishCancelled(job, h)
			return
		}
		outcomes := make(chan Result, job.Config.Concurrency)
		launched := 0
		finished := false
		scopeRevoked := false
		invalidRequest := false
		for launched < job.Config.Concurrency {
			if s.runIntent(h) != "" || ctx.Err() != nil {
				break
			}
			combination, ok := iterator.Next()
			if !ok {
				finished = true
				break
			}
			if !waitForStart(ctx, nextStart) {
				break
			}
			if !s.scope.Allows(job.Config.Template.URL) {
				scopeRevoked = true
				break
			}
			request, buildErr := BuildRequest(job.Config.Template, job.Config.Positions, combination)
			if buildErr != nil {
				invalidRequest = true
				break
			}
			if !s.scope.Allows(request.URL) {
				scopeRevoked = true
				break
			}
			nextStart = time.Now().Add(interval)
			launched++
			go s.sendOne(ctx, job, combination, request, outcomes)
		}
		pending := make(map[int64]Result, launched)
		for range launched {
			result := <-outcomes
			pending[result.Sequence] = result
		}
		batchEnd := job.NextSequence + int64(launched)
		for sequence := job.NextSequence; sequence < batchEnd; sequence++ {
			result := pending[sequence]
			if result.ErrorCategory == "scope_revoked" {
				scopeRevoked = true
			}
			job, err = s.store.AppendResult(context.Background(), job.ID, result)
			if err != nil {
				s.finishRun(job, StatePaused, "persistence_error")
				return
			}
			s.publish("intruder.result.created", map[string]interface{}{"jobId": job.ID, "sequence": result.Sequence})
			if time.Since(lastProgress) >= 200*time.Millisecond {
				s.publish("intruder.progress", map[string]interface{}{"jobId": job.ID, "completed": job.CompletedCount, "total": job.TotalRequests, "errors": job.ErrorCount})
				lastProgress = time.Now()
			}
		}
		if invalidRequest {
			s.finishRun(job, StateFailed, "invalid_request")
			return
		}
		if scopeRevoked {
			s.finishRun(job, StatePaused, "scope_revoked")
			return
		}
		if s.runIntent(h) != "" || ctx.Err() != nil {
			s.finishCancelled(job, h)
			return
		}
		if finished {
			s.finishRun(job, StateCompleted, "")
			return
		}
	}
}

func (s *Service) runIntent(h *runHandle) State { s.mu.Lock(); defer s.mu.Unlock(); return h.intent }

func (s *Service) sendOne(ctx context.Context, job Job, combination Combination, request repeater.SendRequest, out chan<- Result) {
	result := Result{Sequence: combination.Sequence, Selections: combination.Selections, Method: request.Method, URL: request.URL, RequestSize: int64(len(request.Body)), RequestCapture: request.Body, CreatedAt: time.Now().UTC()}
	defer func() {
		if recover() != nil {
			result.ErrorCategory = "worker_panic"
		}
		out <- result
	}()
	if !s.scope.Allows(request.URL) {
		result.ErrorCategory = "scope_revoked"
		return
	}
	response, err := s.sender.Send(ctx, request, repeater.SendOptions{Timeout: job.Config.Timeout, BodyLimitBytes: analysisBodyLimit})
	if err != nil {
		result.ErrorCategory = sendErrorCategory(err)
		return
	}
	result.Status = response.Status
	result.MIMEType = response.ContentType
	result.ResponseSize = response.Size
	result.Duration = time.Duration(response.DurationMS) * time.Millisecond
	result.ResponseTruncated = response.Truncated
	result.ResponseCapture = response.Body
	result.BodyStored = true
}

func (s *Service) finishCancelled(job Job, h *runHandle) {
	s.mu.Lock()
	intent := h.intent
	s.mu.Unlock()
	if intent == StateAborted {
		s.finishRun(job, StateAborted, "operator")
		return
	}
	s.finishRun(job, StatePaused, "operator")
}

func waitForStart(ctx context.Context, at time.Time) bool {
	remaining := time.Until(at)
	if remaining <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return ctx.Err() == nil
	}
}

func sendErrorCategory(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	return "network"
}

func (s *Service) finishRun(job Job, state State, reason string) {
	current, err := s.store.GetJob(context.Background(), job.ID)
	if err != nil {
		return
	}
	if state == StatePaused {
		if current.State == StateRunning {
			current, err = s.store.Transition(context.Background(), current.ID, current.Revision, StateRunning, StatePausing, reason)
			if err != nil {
				return
			}
		}
		if current.State == StatePausing {
			if updated, err := s.store.Transition(context.Background(), current.ID, current.Revision, StatePausing, StatePaused, reason); err == nil {
				s.publishJob(updated)
			}
		}
		return
	}
	if state == StateAborted && current.State == StateAborting {
		if updated, err := s.store.Transition(context.Background(), current.ID, current.Revision, StateAborting, StateAborted, reason); err == nil {
			s.publishJob(updated)
		}
		return
	}
	if current.State == StateRunning {
		if updated, err := s.store.Transition(context.Background(), current.ID, current.Revision, StateRunning, state, reason); err == nil {
			s.publishJob(updated)
		}
	}
}
