package target

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/events"
	"github.com/lutzifer/burpsuite-clone/internal/scope"
	"github.com/lutzifer/burpsuite-clone/internal/store"
)

const (
	rebuildPageSize         = 200
	rebuildProgressRecords  = 100
	rebuildProgressInterval = 250 * time.Millisecond
)

var (
	errServiceClosed    = errors.New("target service is closed")
	errProjectionFailed = errors.New("target projection failed")
)

type Repository interface {
	store.Store
	store.ScopeStore
	store.RebuildHistoryStore
	store.TargetStore
}

type Service struct {
	repository Repository
	scope      *scope.Manager
	events     *events.Hub
	limits     Limits

	operationMu sync.Mutex

	mu                  sync.Mutex
	pendingObservers    sync.WaitGroup
	activeGenerationID  int64
	activeScopeVersion  int64
	rebuildCancel       context.CancelFunc
	rebuildDone         chan struct{}
	pendingGenerationID int64
	pendingRules        *scope.RuleSet
	rebuildGenerationID int64
	rebuildRunning      bool
	closed              bool
}

func NewService(repository Repository, manager *scope.Manager, hub *events.Hub, limits Limits) *Service {
	return &Service{repository: repository, scope: manager, events: hub, limits: limits}
}

func (s *Service) Recover(ctx context.Context) error {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	if s.isClosed() {
		return errServiceClosed
	}

	state, err := s.repository.LoadScopeState(ctx)
	if err != nil {
		return err
	}
	rules, err := scope.Compile(state.Version, state.Rules)
	if err != nil {
		return err
	}
	s.scope.Replace(rules)

	active, activeErr := s.repository.ActiveTargetGeneration(ctx)
	if activeErr != nil && !errors.Is(activeErr, sql.ErrNoRows) {
		return activeErr
	}
	s.mu.Lock()
	if activeErr == nil {
		s.activeGenerationID = active.ID
		s.activeScopeVersion = active.ScopeVersion
	} else {
		s.activeGenerationID = 0
		s.activeScopeVersion = 0
	}
	s.mu.Unlock()

	latest, latestErr := s.repository.LatestTargetGeneration(ctx)
	if latestErr != nil && !errors.Is(latestErr, sql.ErrNoRows) {
		return latestErr
	}
	if latestErr == nil && latest.Status == "failed" {
		return nil
	}
	if latestErr == nil && latest.Status == "building" {
		if err := s.repository.CancelTargetGeneration(ctx, latest.ID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if activeErr == nil && active.ScopeVersion == state.Version && (latestErr != nil || latest.Status != "building") {
		return nil
	}
	return s.startRebuildLocked(ctx, state.Version, rules)
}

func (s *Service) Scope() *scope.Manager {
	return s.scope
}

func (s *Service) Observe(ctx context.Context, exchange *store.Exchange) error {
	if exchange == nil {
		return errProjectionFailed
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	generationID := s.activeGenerationID
	rules := (*scope.RuleSet)(nil)
	pending := false
	if s.pendingGenerationID != 0 {
		generationID = s.pendingGenerationID
		rules = s.pendingRules
		pending = true
	} else if generationID == 0 || !exchange.InScope || exchange.ScopeVersion != s.activeScopeVersion {
		s.mu.Unlock()
		return nil
	}
	s.pendingObservers.Add(1)
	s.mu.Unlock()
	defer s.pendingObservers.Done()

	if pending && !rules.Classify(scope.Target{Scheme: exchange.Scheme, Host: exchange.Host, Path: exchange.Path}).InScope {
		return nil
	}
	observation, err := Analyze(exchange, s.limits)
	if err != nil {
		return errProjectionFailed
	}
	if err := s.repository.UpsertTargetObservation(ctx, generationID, observation); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return errProjectionFailed
	}
	if pending {
		return nil
	}

	tree, err := s.repository.ListTargetTree(ctx)
	if err != nil {
		return errProjectionFailed
	}
	endpointID := endpointIDForKey(tree, observation.Key)
	if endpointID != 0 {
		s.publish("target.endpoint.updated", endpointUpdatedPayload{EndpointID: endpointID, GenerationID: generationID})
	}
	return nil
}

func (s *Service) State(ctx context.Context) (scope.State, error) {
	return s.repository.LoadScopeState(ctx)
}

func (s *Service) ReplaceRules(ctx context.Context, expectedVersion int64, rules []scope.Rule) (scope.State, error) {
	if _, err := scope.Compile(expectedVersion+1, rules); err != nil {
		return scope.State{}, err
	}

	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	if s.isClosed() {
		return scope.State{}, errServiceClosed
	}
	current, err := s.repository.LoadScopeState(ctx)
	if err != nil {
		return scope.State{}, err
	}
	if current.Version != expectedVersion {
		return scope.State{}, store.ErrScopeVersionConflict
	}
	if err := s.stopRebuildLocked(); err != nil {
		return scope.State{}, err
	}

	state, err := s.repository.ReplaceScopeRules(ctx, expectedVersion, rules)
	if err != nil {
		return scope.State{}, err
	}
	compiled, err := scope.Compile(state.Version, state.Rules)
	if err != nil {
		return scope.State{}, err
	}
	s.scope.Replace(compiled)
	s.publish("scope.changed", scopeChangedPayload{Version: state.Version})
	if err := s.startRebuildLocked(ctx, state.Version, compiled); err != nil {
		return state, err
	}
	return state, nil
}

func (s *Service) RetryRebuild(ctx context.Context) error {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	if s.isClosed() {
		return errServiceClosed
	}
	state, err := s.repository.LoadScopeState(ctx)
	if err != nil {
		return err
	}
	rules, err := scope.Compile(state.Version, state.Rules)
	if err != nil {
		return err
	}
	if err := s.stopRebuildLocked(); err != nil {
		return err
	}
	s.scope.Replace(rules)
	return s.startRebuildLocked(ctx, state.Version, rules)
}

func (s *Service) RebuildStatus(ctx context.Context) (store.RebuildStatus, error) {
	generation, err := s.latestTargetGeneration(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return store.RebuildStatus{Status: "idle"}, nil
	}
	if err != nil {
		return store.RebuildStatus{}, err
	}
	s.mu.Lock()
	activeScopeVersion := s.activeScopeVersion
	s.mu.Unlock()
	return rebuildStatus(generation, activeScopeVersion), nil
}

func (s *Service) latestTargetGeneration(ctx context.Context) (store.TargetGeneration, error) {
	for {
		generation, err := s.repository.LatestTargetGeneration(ctx)
		if !isSQLiteBusy(err) {
			return generation, err
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return store.TargetGeneration{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func isSQLiteBusy(err error) bool {
	type errorCoder interface{ Code() int }
	var coded errorCoder
	return errors.As(err, &coded) && coded.Code() == 5
}

func (s *Service) Tree(ctx context.Context) ([]store.TargetTreeNode, error) {
	tree, err := s.repository.ListTargetTree(ctx)
	if err != nil {
		return nil, err
	}
	rules := s.scope.Current()
	for index := range tree {
		enrichTreeScope(&tree[index], rules, tree[index].Scheme, tree[index].Host, tree[index].Port, nil)
	}
	return tree, nil
}

func (s *Service) Endpoint(ctx context.Context, endpointID int64) (store.TargetEndpoint, error) {
	endpoint, err := s.repository.GetTargetEndpoint(ctx, endpointID)
	if err != nil {
		return store.TargetEndpoint{}, err
	}
	endpoint.InScope = classifyEndpointKey(s.scope.Current(), endpoint.Key)
	return endpoint, nil
}

func (s *Service) Requests(ctx context.Context, endpointID int64) ([]store.TargetRequestRef, error) {
	return s.repository.ListTargetRequests(ctx, endpointID)
}

func (s *Service) Parameters(ctx context.Context, endpointID int64) ([]store.TargetParameter, error) {
	return s.repository.ListTargetParameters(ctx, endpointID)
}

func (s *Service) Close() {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	cancel := s.rebuildCancel
	done := s.rebuildDone
	generationID := s.rebuildGenerationID
	running := s.rebuildRunning
	if cancel != nil {
		cancel()
	}
	s.mu.Unlock()

	if running && done != nil {
		<-done
		if err := s.repository.CancelTargetGeneration(context.Background(), generationID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return
		}
	}
	s.mu.Lock()
	s.pendingObservers.Wait()
	s.mu.Unlock()
}

func (s *Service) startRebuildLocked(ctx context.Context, scopeVersion int64, rules *scope.RuleSet) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errServiceClosed
	}
	// Add and Wait are serialized by mu, so the active map is quiescent before
	// the pending generation is registered and no observer can join mid-drain.
	s.pendingObservers.Wait()
	throughID, err := s.repository.LatestExchangeID(ctx)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	total, err := s.repository.CountExchangesThrough(ctx, throughID)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	generationID, err := s.repository.CreateTargetGeneration(ctx, scopeVersion, total)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	workerContext, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.pendingGenerationID = generationID
	s.pendingRules = rules
	s.rebuildGenerationID = generationID
	s.rebuildCancel = cancel
	s.rebuildDone = done
	s.rebuildRunning = true
	activeScopeVersion := s.activeScopeVersion
	s.mu.Unlock()

	status := store.RebuildStatus{
		ID: generationID, ScopeVersion: scopeVersion, ActiveScopeVersion: activeScopeVersion,
		Processed: 0, Total: total, Status: "building",
	}
	s.publish("target.rebuild.started", status)
	go s.runRebuild(workerContext, done, generationID, scopeVersion, throughID, total, rules)
	return nil
}

func (s *Service) stopRebuildLocked() error {
	s.mu.Lock()
	if !s.rebuildRunning {
		s.mu.Unlock()
		return nil
	}
	cancel := s.rebuildCancel
	done := s.rebuildDone
	generationID := s.rebuildGenerationID
	if cancel != nil {
		cancel()
	}
	s.mu.Unlock()

	<-done
	if err := s.repository.CancelTargetGeneration(context.Background(), generationID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return nil
}

func (s *Service) runRebuild(ctx context.Context, done chan struct{}, generationID, scopeVersion, throughID, total int64, rules *scope.RuleSet) {
	defer func() {
		s.mu.Lock()
		if s.rebuildGenerationID == generationID {
			s.rebuildRunning = false
			s.rebuildCancel = nil
		}
		s.mu.Unlock()
		close(done)
	}()

	var processed, afterID, lastReported int64
	lastProgress := time.Now()
	for {
		if ctx.Err() != nil {
			s.drainObservers(generationID)
			return
		}
		page, err := s.repository.ListExchangesPage(ctx, afterID, throughID, rebuildPageSize)
		if err != nil {
			s.failRebuild(ctx, generationID, scopeVersion, processed, total, err)
			return
		}
		if len(page) == 0 {
			break
		}
		for index := range page {
			if ctx.Err() != nil {
				s.drainObservers(generationID)
				return
			}
			exchange := &page[index]
			if rules.Classify(scope.Target{Scheme: exchange.Scheme, Host: exchange.Host, Path: exchange.Path}).InScope {
				observation, err := Analyze(exchange, s.limits)
				if err != nil {
					s.failRebuild(ctx, generationID, scopeVersion, processed, total, err)
					return
				}
				if err := s.repository.UpsertTargetObservation(ctx, generationID, observation); err != nil {
					s.failRebuild(ctx, generationID, scopeVersion, processed, total, err)
					return
				}
			}
			processed++
			afterID = exchange.ID
			if processed-lastReported >= rebuildProgressRecords || time.Since(lastProgress) >= rebuildProgressInterval {
				if err := s.repository.SetTargetGenerationProgress(ctx, generationID, processed); err != nil {
					s.failRebuild(ctx, generationID, scopeVersion, processed, total, err)
					return
				}
				lastReported = processed
				lastProgress = time.Now()
				s.publish("target.rebuild.progress", s.statusFor(generationID, scopeVersion, processed, total, "building", ""))
			}
		}
		if len(page) < rebuildPageSize {
			break
		}
	}

	if processed != lastReported {
		if err := s.repository.SetTargetGenerationProgress(ctx, generationID, processed); err != nil {
			s.failRebuild(ctx, generationID, scopeVersion, processed, total, err)
			return
		}
	}
	if !s.activateRebuild(ctx, generationID, scopeVersion, processed, total) {
		return
	}
}

func (s *Service) activateRebuild(ctx context.Context, generationID, scopeVersion, processed, total int64) bool {
	s.mu.Lock()
	if s.pendingGenerationID != generationID {
		s.mu.Unlock()
		return false
	}
	s.pendingGenerationID = 0
	s.pendingRules = nil
	s.pendingObservers.Wait()
	if ctx.Err() != nil || s.closed {
		s.mu.Unlock()
		return false
	}
	if err := s.repository.ActivateTargetGeneration(ctx, generationID); err != nil {
		s.mu.Unlock()
		s.markRebuildFailed(generationID, scopeVersion, processed, total)
		return false
	}
	s.activeGenerationID = generationID
	s.activeScopeVersion = scopeVersion
	s.mu.Unlock()

	_ = s.repository.PruneRetiredTargetGenerations(context.Background())
	s.publish("target.rebuild.completed", s.statusFor(generationID, scopeVersion, processed, total, "active", ""))
	return true
}

func (s *Service) failRebuild(ctx context.Context, generationID, scopeVersion, processed, total int64, _ error) {
	s.drainObservers(generationID)
	if ctx.Err() != nil {
		return
	}
	s.markRebuildFailed(generationID, scopeVersion, processed, total)
}

func (s *Service) markRebuildFailed(generationID, scopeVersion, processed, total int64) {
	background := context.Background()
	_ = s.repository.SetTargetGenerationProgress(background, generationID, processed)
	if err := s.repository.FailTargetGeneration(background, generationID, errProjectionFailed.Error()); err != nil {
		return
	}
	s.publish("target.rebuild.failed", s.statusFor(generationID, scopeVersion, processed, total, "failed", errProjectionFailed.Error()))
}

func (s *Service) drainObservers(generationID int64) {
	s.mu.Lock()
	if s.pendingGenerationID == generationID {
		s.pendingGenerationID = 0
		s.pendingRules = nil
	}
	s.pendingObservers.Wait()
	s.mu.Unlock()
}

func (s *Service) statusFor(generationID, scopeVersion, processed, total int64, status, generationError string) store.RebuildStatus {
	s.mu.Lock()
	activeScopeVersion := s.activeScopeVersion
	s.mu.Unlock()
	return store.RebuildStatus{
		ID: generationID, ScopeVersion: scopeVersion, ActiveScopeVersion: activeScopeVersion,
		Processed: processed, Total: total, Status: status, Error: generationError,
	}
}

func (s *Service) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *Service) publish(eventType string, data any) {
	if s.events != nil {
		s.events.Publish(events.Event{Type: eventType, Data: data})
	}
}

func rebuildStatus(generation store.TargetGeneration, activeScopeVersion int64) store.RebuildStatus {
	return store.RebuildStatus{
		ID: generation.ID, ScopeVersion: generation.ScopeVersion, ActiveScopeVersion: activeScopeVersion,
		Processed: generation.Processed, Total: generation.Total, Status: generation.Status, Error: generation.Error,
	}
}

type scopeChangedPayload struct {
	Version int64 `json:"version"`
}

type endpointUpdatedPayload struct {
	EndpointID   int64 `json:"endpointId"`
	GenerationID int64 `json:"generationId"`
}

func classifyEndpointKey(rules *scope.RuleSet, key store.TargetEndpointKey) bool {
	host := key.Host
	defaultPort := key.Scheme == "http" && key.Port == 80 || key.Scheme == "https" && key.Port == 443
	if !defaultPort {
		host = net.JoinHostPort(key.Host, strconv.Itoa(key.Port))
	}
	return rules.Classify(scope.Target{Scheme: key.Scheme, Host: host, Path: key.Path}).InScope
}

func enrichTreeScope(node *store.TargetTreeNode, rules *scope.RuleSet, scheme, host string, port int, path []string) bool {
	if node.Scheme != "" {
		scheme = node.Scheme
	}
	if node.Host != "" {
		host = node.Host
	}
	if node.Port != 0 {
		port = node.Port
	}
	if node.Path != "" {
		path = append(append([]string(nil), path...), node.Path)
	}
	if node.ID != 0 {
		endpointPath := "/"
		if len(path) > 0 {
			endpointPath += strings.Join(path, "/")
		}
		node.InScope = classifyEndpointKey(rules, store.TargetEndpointKey{Scheme: scheme, Host: host, Port: port, Path: endpointPath, Method: node.Method})
		return node.InScope
	}
	node.InScope = false
	for index := range node.Children {
		if enrichTreeScope(&node.Children[index], rules, scheme, host, port, path) {
			node.InScope = true
		}
	}
	return node.InScope
}

func endpointIDForKey(tree []store.TargetTreeNode, key store.TargetEndpointKey) int64 {
	var visit func([]store.TargetTreeNode, string, string, int, []string) int64
	visit = func(nodes []store.TargetTreeNode, scheme, host string, port int, path []string) int64 {
		for _, node := range nodes {
			nextScheme, nextHost, nextPort := scheme, host, port
			nextPath := path
			if node.Scheme != "" {
				nextScheme = node.Scheme
			}
			if node.Host != "" {
				nextHost = node.Host
			}
			if node.Port != 0 {
				nextPort = node.Port
			}
			if node.Path != "" {
				nextPath = append(append([]string(nil), path...), node.Path)
			}
			if node.ID != 0 {
				endpointPath := "/"
				if len(nextPath) > 0 {
					endpointPath += strings.Join(nextPath, "/")
				}
				if nextScheme == key.Scheme && nextHost == key.Host && nextPort == key.Port && endpointPath == key.Path && node.Method == key.Method {
					return node.ID
				}
			}
			if id := visit(node.Children, nextScheme, nextHost, nextPort, nextPath); id != 0 {
				return id
			}
		}
		return 0
	}
	return visit(tree, "", "", 0, nil)
}
