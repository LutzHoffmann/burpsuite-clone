package target

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/events"
	"github.com/lutzifer/burpsuite-clone/internal/scope"
	"github.com/lutzifer/burpsuite-clone/internal/store"
)

var testLimits = Limits{MaxJSONDepth: 16, MaxFields: 1000, MaxMultipartFields: 100}

func TestServiceReplaceRulesRebuildsAndActivates(t *testing.T) {
	repository := openTargetRepository(t)
	saveExchange(t, repository, "https", "example.test", "/api", "q=1")
	initial, _ := scope.Compile(0, nil)
	service := NewService(repository, scope.NewManager(initial), events.NewHub(), testLimits)
	t.Cleanup(service.Close)

	state, err := service.ReplaceRules(context.Background(), 0, []scope.Rule{{Enabled: true, Action: scope.ActionInclude, Scheme: "https", HostPattern: "example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	status := waitForRebuildStatus(t, service, "active")
	tree, err := service.Tree(context.Background())
	if err != nil || len(tree) != 1 {
		t.Fatalf("tree = %#v, err = %v", tree, err)
	}
	if state.Version != 1 || status.ScopeVersion != 1 || status.ActiveScopeVersion != 1 || !service.Scope().Current().Classify(scope.Target{Scheme: "https", Host: "example.test", Path: "/api"}).InScope {
		t.Fatalf("state = %#v, status = %#v", state, status)
	}
	endpointID := firstEndpointID(t, tree)
	endpoint, err := service.Endpoint(context.Background(), endpointID)
	if err != nil || !endpoint.InScope {
		t.Fatalf("endpoint = %#v, err = %v", endpoint, err)
	}
	requests, err := service.Requests(context.Background(), endpointID)
	if err != nil || len(requests) != 1 {
		t.Fatalf("requests = %#v, err = %v", requests, err)
	}
	parameters, err := service.Parameters(context.Background(), endpointID)
	if err != nil || len(parameters) != 1 || parameters[0].Name != "q" {
		t.Fatalf("parameters = %#v, err = %v", parameters, err)
	}
}

func TestServiceReplaceRulesConflictPreservesStateAndGeneration(t *testing.T) {
	repository := openTargetRepository(t)
	service := newRecoveredService(t, repository, repository, events.NewHub())
	state := replaceRulesAndWait(t, service, 0, includeRule("example.test", "/"))
	before := waitForRebuildStatus(t, service, "active")

	if _, err := service.ReplaceRules(context.Background(), 0, includeRule("other.test", "/")); !errors.Is(err, store.ErrScopeVersionConflict) {
		t.Fatalf("replace conflict error = %v", err)
	}
	afterState, err := service.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	after := waitForRebuildStatus(t, service, "active")
	if afterState.Version != state.Version || after.ID != before.ID || service.Scope().Current().Classify(scope.Target{Scheme: "https", Host: "other.test", Path: "/"}).InScope {
		t.Fatalf("state = %#v, before = %#v, after = %#v", afterState, before, after)
	}
}

func TestServiceReplaceRulesMalformedPreservesPriorState(t *testing.T) {
	repository := openTargetRepository(t)
	service := newRecoveredService(t, repository, repository, events.NewHub())
	replaceRulesAndWait(t, service, 0, includeRule("example.test", "/"))
	before := waitForRebuildStatus(t, service, "active")

	if _, err := service.ReplaceRules(context.Background(), 1, []scope.Rule{{Enabled: true, Action: scope.ActionInclude, Scheme: "ftp", HostPattern: "other.test"}}); err == nil {
		t.Fatal("malformed replacement succeeded")
	}
	state, err := service.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	after := waitForRebuildStatus(t, service, "active")
	if state.Version != 1 || after.ID != before.ID || !service.Scope().Current().Classify(scope.Target{Scheme: "https", Host: "example.test", Path: "/"}).InScope {
		t.Fatalf("state = %#v, before = %#v, after = %#v", state, before, after)
	}
}

func TestServiceRebuildFailurePreservesActiveGenerationAndRetry(t *testing.T) {
	sqlite := openTargetRepository(t)
	saveExchange(t, sqlite, "https", "example.test", "/old", "")
	repository := newControlledRepository(sqlite)
	service := newRecoveredService(t, repository, sqlite, events.NewHub())
	replaceRulesAndWait(t, service, 0, includeRule("example.test", "/old"))
	oldTree, err := service.Tree(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	oldEndpointID := firstEndpointID(t, oldTree)

	repository.setPageFailure(errors.New("history page unavailable"))
	if _, err := service.ReplaceRules(context.Background(), 1, includeRule("example.test", "/new")); err != nil {
		t.Fatal(err)
	}
	failed := waitForRebuildStatus(t, service, "failed")
	stale, err := service.Endpoint(context.Background(), oldEndpointID)
	if err != nil || stale.InScope || failed.ActiveScopeVersion != 1 {
		t.Fatalf("stale endpoint = %#v, status = %#v, err = %v", stale, failed, err)
	}

	repository.setPageFailure(nil)
	saveExchange(t, sqlite, "https", "example.test", "/new", "")
	if err := service.RetryRebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	retried := waitForRebuildStatus(t, service, "active")
	tree, err := service.Tree(context.Background())
	if err != nil || len(tree) != 1 || retried.ScopeVersion != 2 || retried.ActiveScopeVersion != 2 || retried.ID == failed.ID {
		t.Fatalf("tree = %#v, status = %#v, err = %v", tree, retried, err)
	}
}

func TestServiceConcurrentRetriesRejectSecondWithoutCancellingFirst(t *testing.T) {
	sqlite := openTargetRepository(t)
	saveExchange(t, sqlite, "https", "example.test", "/api", "")
	repository := newControlledRepository(sqlite)
	service := newRecoveredService(t, repository, sqlite, events.NewHub())
	replaceRulesAndWait(t, service, 0, includeRule("example.test", "/"))
	repository.blockPages()

	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			results <- service.RetryRebuild(context.Background())
		}()
	}
	close(start)
	firstErr := <-results
	secondErr := <-results
	cancellations := repository.cancelCount()
	repository.releaseBlockedPages()
	waitForRebuildStatus(t, service, "active")

	accepted := 0
	rejected := 0
	unexpected := 0
	for _, err := range []error{firstErr, secondErr} {
		if err == nil {
			accepted++
		} else if errors.Is(err, ErrRebuildInProgress) {
			rejected++
		} else {
			unexpected++
		}
	}
	if accepted != 1 || rejected != 1 || unexpected != 0 || cancellations != 0 {
		t.Fatalf("retry errors = [%v, %v], accepted = %d, rejected = %d, unexpected = %d, cancellations = %d", firstErr, secondErr, accepted, rejected, unexpected, cancellations)
	}
}

func TestServiceFailedRebuildRejectsWritesToStaleActiveGeneration(t *testing.T) {
	sqlite := openTargetRepository(t)
	saveExchange(t, sqlite, "https", "example.test", "/old", "")
	repository := newControlledRepository(sqlite)
	service := newRecoveredService(t, repository, sqlite, events.NewHub())
	replaceRulesAndWait(t, service, 0, includeRule("example.test", "/old"))

	repository.setPageFailure(errors.New("history page unavailable"))
	if _, err := service.ReplaceRules(context.Background(), 1, includeRule("example.test", "/new")); err != nil {
		t.Fatal(err)
	}
	waitForRebuildStatus(t, service, "failed")
	late := saveScopedExchange(t, sqlite, "example.test", "/old/late", true, 1)
	if err := service.Observe(context.Background(), late); err != nil {
		t.Fatal(err)
	}
	tree, err := service.Tree(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if treeHasPath(tree, "late") {
		t.Fatalf("stale active generation accepted observation after failed rebuild: %#v", tree)
	}
}

func TestServiceProgressPersistenceErrorFailsRebuild(t *testing.T) {
	sqlite := openTargetRepository(t)
	saveExchange(t, sqlite, "https", "example.test", "/api", "")
	repository := newControlledRepository(sqlite)
	repository.setProgressFailures(errors.New("progress unavailable"))
	hub := events.NewHub()
	subscriber, unsubscribe := hub.Subscribe()
	defer unsubscribe()
	service := newRecoveredService(t, repository, sqlite, hub)

	if _, err := service.ReplaceRules(context.Background(), 0, includeRule("example.test", "/")); err != nil {
		t.Fatal(err)
	}
	waitForRebuildWorker(t, service)
	status, err := service.RebuildStatus(context.Background())
	if err != nil || status.Status != "failed" {
		t.Fatalf("status = %#v, err = %v", status, err)
	}
	received := collectUntilEvent(t, subscriber, "target.rebuild.failed")
	if received[len(received)-1].Data != status {
		t.Fatalf("failed event = %#v, status = %#v", received[len(received)-1].Data, status)
	}
}

func TestServiceReplaceRulesOwnsRebuildStartContext(t *testing.T) {
	sqlite := openTargetRepository(t)
	repository := newControlledRepository(sqlite)
	ctx, cancel := context.WithCancel(context.Background())
	repository.cancelAfterScopeReplace = cancel
	service := newRecoveredService(t, repository, sqlite, events.NewHub())

	state, err := service.ReplaceRules(ctx, 0, includeRule("example.test", "/"))
	if err != nil {
		t.Fatalf("ReplaceRules after committed request cancellation: %v", err)
	}
	waitForRebuildWorker(t, service)
	status, err := service.RebuildStatus(context.Background())
	if err != nil || status.ScopeVersion != state.Version || status.Status != "active" {
		t.Fatalf("status = %#v, err = %v", status, err)
	}
}

func TestServiceReplaceRulesExposesRebuildStartFailure(t *testing.T) {
	sqlite := openTargetRepository(t)
	repository := newControlledRepository(sqlite)
	repository.generationFailure = errors.New("generation unavailable")
	hub := events.NewHub()
	subscriber, unsubscribe := hub.Subscribe()
	defer unsubscribe()
	service := newRecoveredService(t, repository, sqlite, hub)

	state, err := service.ReplaceRules(context.Background(), 0, includeRule("example.test", "/"))
	if err == nil {
		t.Fatal("ReplaceRules succeeded despite generation failure")
	}
	status, statusErr := service.RebuildStatus(context.Background())
	if statusErr != nil || status.ScopeVersion != state.Version || status.Status != "failed" {
		t.Fatalf("status = %#v, err = %v", status, statusErr)
	}
	received := collectUntilEvent(t, subscriber, "target.rebuild.failed")
	if received[len(received)-1].Data != status {
		t.Fatalf("failed event = %#v, status = %#v", received[len(received)-1].Data, status)
	}
}

func TestServiceFailureMarkRetriesTransientContention(t *testing.T) {
	sqlite := openTargetRepository(t)
	saveExchange(t, sqlite, "https", "example.test", "/api", "")
	repository := newControlledRepository(sqlite)
	repository.setPageFailure(errors.New("history page unavailable"))
	repository.setFailureMarkFailures(sqliteBusyTestError{})
	hub := events.NewHub()
	subscriber, unsubscribe := hub.Subscribe()
	defer unsubscribe()
	service := newRecoveredService(t, repository, sqlite, hub)

	if _, err := service.ReplaceRules(context.Background(), 0, includeRule("example.test", "/")); err != nil {
		t.Fatal(err)
	}
	waitForRebuildWorker(t, service)
	status, err := service.RebuildStatus(context.Background())
	if err != nil || status.Status != "failed" || repository.failureMarkAttemptCount() < 2 {
		t.Fatalf("status = %#v, attempts = %d, err = %v", status, repository.failureMarkAttemptCount(), err)
	}
	received := collectUntilEvent(t, subscriber, "target.rebuild.failed")
	if received[len(received)-1].Data != status {
		t.Fatalf("failed event = %#v, status = %#v", received[len(received)-1].Data, status)
	}
}

func TestServiceFailureMarkPermanentErrorRemainsObservable(t *testing.T) {
	sqlite := openTargetRepository(t)
	saveExchange(t, sqlite, "https", "example.test", "/api", "")
	repository := newControlledRepository(sqlite)
	repository.setPageFailure(errors.New("history page unavailable"))
	repository.setPermanentFailureMarkError(errors.New("repository rejected secret-body"))
	hub := events.NewHub()
	subscriber, unsubscribe := hub.Subscribe()
	defer unsubscribe()
	service := newRecoveredService(t, repository, sqlite, hub)

	if _, err := service.ReplaceRules(context.Background(), 0, includeRule("example.test", "/")); err != nil {
		t.Fatal(err)
	}
	waitForRebuildWorker(t, service)
	status, err := service.RebuildStatus(context.Background())
	if err != nil || status.Status != "failed" || strings.Contains(status.Error, "secret-body") {
		t.Fatalf("status = %#v, err = %v", status, err)
	}
	received := collectUntilEvent(t, subscriber, "target.rebuild.failed")
	if received[len(received)-1].Data != status {
		t.Fatalf("failed event = %#v, status = %#v", received[len(received)-1].Data, status)
	}
}

func TestServiceNewerRebuildCancelsAndWaitsForOlderWorker(t *testing.T) {
	sqlite := openTargetRepository(t)
	saveExchange(t, sqlite, "https", "example.test", "/api", "")
	repository := newControlledRepository(sqlite)
	service := newRecoveredService(t, repository, sqlite, events.NewHub())
	replaceRulesAndWait(t, service, 0, includeRule("example.test", "/"))

	pageStarted := repository.blockPages()
	if _, err := service.ReplaceRules(context.Background(), 1, includeRule("example.test", "/v2")); err != nil {
		t.Fatal(err)
	}
	waitForSignal(t, pageStarted, "older rebuild page")
	repository.unblockFuturePages()
	if _, err := service.ReplaceRules(context.Background(), 2, includeRule("example.test", "/api")); err != nil {
		t.Fatal(err)
	}
	status := waitForRebuildStatus(t, service, "active")
	if status.ScopeVersion != 3 || status.ActiveScopeVersion != 3 || repository.cancelCount() == 0 {
		t.Fatalf("status = %#v, cancelled generations = %d", status, repository.cancelCount())
	}
}

func TestServiceObserveCapturesPriorScopeExchangeCompletedDuringRebuild(t *testing.T) {
	sqlite := openTargetRepository(t)
	saveExchange(t, sqlite, "https", "seed.test", "/seed", "")
	repository := newControlledRepository(sqlite)
	service := newRecoveredService(t, repository, sqlite, events.NewHub())

	pageStarted := repository.blockPages()
	if _, err := service.ReplaceRules(context.Background(), 0, includeRule("example.test", "/")); err != nil {
		t.Fatal(err)
	}
	waitForSignal(t, pageStarted, "rebuild page")
	late := &store.Exchange{
		Method: "GET", Scheme: "https", Host: "example.test", Path: "/late", Status: 200,
		StartedAt: time.Now().UTC(), InScope: false, ScopeVersion: 0,
		Request: store.RequestData{Headers: map[string][]string{}}, Response: store.ResponseData{Headers: map[string][]string{}},
	}
	if err := sqlite.SaveExchange(context.Background(), late); err != nil {
		t.Fatal(err)
	}
	if err := service.Observe(context.Background(), late); err != nil {
		t.Fatal(err)
	}
	repository.releaseBlockedPages()
	waitForRebuildStatus(t, service, "active")
	tree, err := service.Tree(context.Background())
	if err != nil || !treeHasPath(tree, "late") {
		t.Fatalf("tree = %#v, err = %v", tree, err)
	}
}

func TestServiceObserveReclassifiesPostCutoffExchangeAfterLosingActivationRace(t *testing.T) {
	sqlite := openTargetRepository(t)
	saveExchange(t, sqlite, "https", "seed.test", "/seed", "")
	repository := newControlledRepository(sqlite)
	service := newRecoveredService(t, repository, sqlite, events.NewHub())

	pageStarted := repository.blockPages()
	activationStarted := repository.blockActivations()
	if _, err := service.ReplaceRules(context.Background(), 0, includeRule("example.test", "/")); err != nil {
		t.Fatal(err)
	}
	waitForSignal(t, pageStarted, "rebuild page after cutoff")
	late := saveScopedExchange(t, sqlite, "example.test", "/post-cutoff", false, 0)
	repository.releaseBlockedPages()
	waitForSignal(t, activationStarted, "generation activation")

	observeStarted := make(chan struct{})
	observeDone := make(chan error, 1)
	go func() {
		close(observeStarted)
		observeDone <- service.Observe(context.Background(), late)
	}()
	waitForSignal(t, observeStarted, "post-cutoff observation")
	select {
	case err := <-observeDone:
		repository.releaseBlockedActivations()
		t.Fatalf("Observe returned before activation released the service mutex: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	repository.releaseBlockedActivations()
	if err := <-observeDone; err != nil {
		t.Fatal(err)
	}
	waitForRebuildStatus(t, service, "active")
	tree, err := service.Tree(context.Background())
	if err != nil || !treeHasPath(tree, "post-cutoff") {
		t.Fatalf("tree = %#v, err = %v", tree, err)
	}
}

func TestServiceObserveVersionMismatchDoesNotContaminateActiveGeneration(t *testing.T) {
	repository := openTargetRepository(t)
	service := newRecoveredService(t, repository, repository, events.NewHub())
	replaceRulesAndWait(t, service, 0, includeRule("example.test", "/"))

	exchange := saveScopedExchange(t, repository, "example.test", "/mismatch", true, 2)
	if err := service.Observe(context.Background(), exchange); err != nil {
		t.Fatal(err)
	}
	tree, err := service.Tree(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if treeHasPath(tree, "mismatch") {
		t.Fatalf("version-mismatched exchange contaminated active tree: %#v", tree)
	}
}

func TestServiceObserveSameVersionOutOfScopeDoesNotWriteTargetObservation(t *testing.T) {
	sqlite := openTargetRepository(t)
	repository := newControlledRepository(sqlite)
	service := newRecoveredService(t, repository, sqlite, events.NewHub())
	replaceRulesAndWait(t, service, 0, includeRule("example.test", "/"))
	before := repository.upsertAttemptCount()
	exchange := saveScopedExchange(t, sqlite, "example.test", "/outside", false, 1)

	if err := service.Observe(context.Background(), exchange); err != nil {
		t.Fatal(err)
	}

	if after := repository.upsertAttemptCount(); after != before {
		t.Fatalf("target observation writes = %d, want %d", after, before)
	}
	tree, err := service.Tree(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if treeHasPath(tree, "/outside") {
		t.Fatalf("out-of-scope endpoint was projected: %#v", tree)
	}
}

func TestServiceReplacementDrainsActiveObservationBeforeStartingPendingGeneration(t *testing.T) {
	sqlite := openTargetRepository(t)
	repository := newControlledRepository(sqlite)
	service := newRecoveredService(t, repository, sqlite, events.NewHub())
	replaceRulesAndWait(t, service, 0, includeRule("example.test", "/"))

	upsertStarted := repository.blockUpserts()
	exchange := saveScopedExchange(t, sqlite, "example.test", "/active", true, 1)
	observeDone := make(chan error, 1)
	go func() { observeDone <- service.Observe(context.Background(), exchange) }()
	waitForSignal(t, upsertStarted, "active observation")

	replaceDone := make(chan error, 1)
	go func() {
		_, err := service.ReplaceRules(context.Background(), 1, includeRule("example.test", "/pending"))
		replaceDone <- err
	}()
	select {
	case err := <-replaceDone:
		repository.releaseBlockedUpserts()
		<-observeDone
		t.Fatalf("replacement returned before active observation drained: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	repository.releaseBlockedUpserts()
	if err := <-observeDone; err != nil {
		t.Fatal(err)
	}
	if err := <-replaceDone; err != nil {
		t.Fatal(err)
	}
	waitForRebuildStatus(t, service, "active")
}

func TestServiceRecoverCancelsInterruptedBuildingGeneration(t *testing.T) {
	sqlite := openTargetRepository(t)
	state, err := sqlite.ReplaceScopeRules(context.Background(), 0, includeRule("example.test", "/"))
	if err != nil {
		t.Fatal(err)
	}
	saveExchange(t, sqlite, "https", "example.test", "/recovered", "")
	interruptedID, err := sqlite.CreateTargetGeneration(context.Background(), state.Version, 1)
	if err != nil {
		t.Fatal(err)
	}
	repository := newControlledRepository(sqlite)
	initial, _ := scope.Compile(0, nil)
	service := NewService(repository, scope.NewManager(initial), events.NewHub(), testLimits)
	t.Cleanup(service.Close)

	if err := service.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	status := waitForRebuildStatus(t, service, "active")
	if status.ID == interruptedID || status.ScopeVersion != state.Version || repository.cancelCount() == 0 {
		t.Fatalf("status = %#v, cancel count = %d", status, repository.cancelCount())
	}
}

func TestServiceRecoverLeavesExplicitFailureForRetry(t *testing.T) {
	sqlite := openTargetRepository(t)
	state, err := sqlite.ReplaceScopeRules(context.Background(), 0, includeRule("example.test", "/"))
	if err != nil {
		t.Fatal(err)
	}
	generationID, err := sqlite.CreateTargetGeneration(context.Background(), state.Version, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlite.FailTargetGeneration(context.Background(), generationID, "projection failed"); err != nil {
		t.Fatal(err)
	}
	compiled, _ := scope.Compile(state.Version, state.Rules)
	service := NewService(sqlite, scope.NewManager(compiled), events.NewHub(), testLimits)
	t.Cleanup(service.Close)

	if err := service.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	status := waitForRebuildStatus(t, service, "failed")
	if status.ID != generationID {
		t.Fatalf("status = %#v", status)
	}
}

func TestServiceEventsAreOrderedAndValueFree(t *testing.T) {
	repository := openTargetRepository(t)
	for index := 0; index < 120; index++ {
		saveExchange(t, repository, "https", "example.test", fmt.Sprintf("/api/%d", index), "secret=value")
	}
	hub := events.NewHub()
	subscriber, unsubscribe := hub.Subscribe()
	defer unsubscribe()
	service := newRecoveredService(t, repository, repository, hub)

	if _, err := service.ReplaceRules(context.Background(), 0, includeRule("example.test", "/")); err != nil {
		t.Fatal(err)
	}
	waitForRebuildStatus(t, service, "active")
	received := collectUntilEvent(t, subscriber, "target.rebuild.completed")
	if received[0].Type != "scope.changed" || received[1].Type != "target.rebuild.started" || received[len(received)-1].Type != "target.rebuild.completed" {
		t.Fatalf("event sequence = %v", eventTypes(received))
	}
	for _, event := range received {
		payload, err := json.Marshal(event.Data)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(payload), "secret") || strings.Contains(string(payload), "example.test") {
			t.Fatalf("event %q leaked exchange values: %s", event.Type, payload)
		}
	}

	drainEvents(subscriber)
	exchange := saveScopedExchange(t, repository, "example.test", "/incremental", true, 1)
	if err := service.Observe(context.Background(), exchange); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-subscriber:
		if event.Type != "target.endpoint.updated" {
			t.Fatalf("event = %#v", event)
		}
		payload, _ := json.Marshal(event.Data)
		if strings.Contains(string(payload), "example.test") || strings.Contains(string(payload), "incremental") {
			t.Fatalf("endpoint event leaked values: %s", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("endpoint update event not published")
	}
}

func TestServiceProgressEventsAreCoalesced(t *testing.T) {
	repository := openTargetRepository(t)
	for index := 0; index < 240; index++ {
		saveExchange(t, repository, "https", "example.test", fmt.Sprintf("/items/%d", index), "")
	}
	hub := events.NewHub()
	subscriber, unsubscribe := hub.Subscribe()
	defer unsubscribe()
	service := newRecoveredService(t, repository, repository, hub)

	if _, err := service.ReplaceRules(context.Background(), 0, includeRule("example.test", "/")); err != nil {
		t.Fatal(err)
	}
	waitForRebuildStatus(t, service, "active")
	eventsSeen := collectUntilEvent(t, subscriber, "target.rebuild.completed")
	progressCount := 0
	lastProcessed := int64(0)
	for _, event := range eventsSeen {
		if event.Type != "target.rebuild.progress" {
			continue
		}
		progressCount++
		status, ok := event.Data.(store.RebuildStatus)
		if !ok || status.Processed <= lastProcessed || status.Total != 240 {
			t.Fatalf("progress payload = %#v", event.Data)
		}
		lastProcessed = status.Processed
	}
	if progressCount == 0 || progressCount >= 20 {
		t.Fatalf("progress event count = %d", progressCount)
	}
}

func TestServiceActivationPrunesRetiredGenerations(t *testing.T) {
	sqlite := openTargetRepository(t)
	saveExchange(t, sqlite, "https", "example.test", "/old", "")
	repository := newControlledRepository(sqlite)
	service := newRecoveredService(t, repository, sqlite, events.NewHub())
	replaceRulesAndWait(t, service, 0, includeRule("example.test", "/old"))
	oldTree, err := service.Tree(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	oldEndpointID := firstEndpointID(t, oldTree)

	replaceRulesAndWait(t, service, 1, includeRule("example.test", "/new"))
	if repository.pruneCount() == 0 {
		t.Fatal("service did not prune retired generations")
	}
	if _, err := service.Requests(context.Background(), oldEndpointID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("retired endpoint request error = %v", err)
	}
}

func TestServiceCloseCancelsWorkerAndPreservesActiveGeneration(t *testing.T) {
	sqlite := openTargetRepository(t)
	saveExchange(t, sqlite, "https", "example.test", "/api", "")
	repository := newControlledRepository(sqlite)
	service := newRecoveredService(t, repository, sqlite, events.NewHub())
	replaceRulesAndWait(t, service, 0, includeRule("example.test", "/"))
	activeBefore, err := sqlite.ActiveTargetGeneration(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	pageStarted := repository.blockPages()
	if _, err := service.ReplaceRules(context.Background(), 1, includeRule("example.test", "/api")); err != nil {
		t.Fatal(err)
	}
	waitForSignal(t, pageStarted, "rebuild page")
	service.Close()

	latest, err := sqlite.LatestTargetGeneration(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	activeAfter, err := sqlite.ActiveTargetGeneration(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if latest.Status != "cancelled" || activeAfter.ID != activeBefore.ID {
		t.Fatalf("latest = %#v, active before = %#v, active after = %#v", latest, activeBefore, activeAfter)
	}
}

func TestServiceCloseWaitsForActiveObservation(t *testing.T) {
	sqlite := openTargetRepository(t)
	repository := newControlledRepository(sqlite)
	service := newRecoveredService(t, repository, sqlite, events.NewHub())
	replaceRulesAndWait(t, service, 0, includeRule("example.test", "/"))

	upsertStarted := repository.blockUpserts()
	exchange := saveScopedExchange(t, sqlite, "example.test", "/active", true, 1)
	observeDone := make(chan error, 1)
	go func() { observeDone <- service.Observe(context.Background(), exchange) }()
	waitForSignal(t, upsertStarted, "active observation")
	closeDone := make(chan struct{})
	go func() {
		service.Close()
		close(closeDone)
	}()
	select {
	case <-closeDone:
		repository.releaseBlockedUpserts()
		<-observeDone
		t.Fatal("Close returned before active observation completed")
	case <-time.After(50 * time.Millisecond):
	}
	repository.releaseBlockedUpserts()
	if err := <-observeDone; err != nil {
		t.Fatal(err)
	}
	waitForSignal(t, closeDone, "service close")
}

func TestServiceProjectionFailureDoesNotExposeBodyData(t *testing.T) {
	sqlite := openTargetRepository(t)
	secret := "raw-secret-body"
	exchange := &store.Exchange{
		Method: "POST", Scheme: "https", Host: "example.test", Path: "/api", Status: 200, StartedAt: time.Now().UTC(),
		Request:  store.RequestData{Headers: map[string][]string{"Content-Type": {"application/json"}}, Body: []byte(secret)},
		Response: store.ResponseData{Headers: map[string][]string{}},
	}
	if err := sqlite.SaveExchange(context.Background(), exchange); err != nil {
		t.Fatal(err)
	}
	repository := newControlledRepository(sqlite)
	repository.setUpsertFailure(errors.New("write rejected: " + secret))
	service := newRecoveredService(t, repository, sqlite, events.NewHub())

	if _, err := service.ReplaceRules(context.Background(), 0, includeRule("example.test", "/")); err != nil {
		t.Fatal(err)
	}
	status := waitForRebuildStatus(t, service, "failed")
	if strings.Contains(status.Error, secret) {
		t.Fatalf("rebuild status leaked body data: %#v", status)
	}
}

func TestServiceRebuildStatusIdleWithoutGeneration(t *testing.T) {
	repository := openTargetRepository(t)
	initial, _ := scope.Compile(0, nil)
	service := NewService(repository, scope.NewManager(initial), events.NewHub(), testLimits)
	t.Cleanup(service.Close)
	status, err := service.RebuildStatus(context.Background())
	if err != nil || status != (store.RebuildStatus{Status: "idle"}) {
		t.Fatalf("status = %#v, err = %v", status, err)
	}
}

func openTargetRepository(t *testing.T) *store.SQLiteStore {
	t.Helper()
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "project.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := repository.Close(); err != nil {
			t.Error(err)
		}
	})
	return repository
}

func saveExchange(t *testing.T, repository *store.SQLiteStore, scheme, host, path, query string) int64 {
	t.Helper()
	exchange := &store.Exchange{
		Method: "GET", Scheme: scheme, Host: host, Path: path, Query: query, Status: 200,
		StartedAt: time.Now().UTC(), Request: store.RequestData{Headers: map[string][]string{}}, Response: store.ResponseData{Headers: map[string][]string{}},
	}
	if err := repository.SaveExchange(context.Background(), exchange); err != nil {
		t.Fatal(err)
	}
	return exchange.ID
}

func waitForRebuildStatus(t *testing.T, service *Service, wanted string) store.RebuildStatus {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last store.RebuildStatus
	for time.Now().Before(deadline) {
		status, err := service.RebuildStatus(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		last = status
		if status.Status == wanted {
			return status
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("rebuild status = %#v, want %q", last, wanted)
	return store.RebuildStatus{}
}

func newRecoveredService(t *testing.T, repository Repository, sqlite *store.SQLiteStore, hub *events.Hub) *Service {
	t.Helper()
	state, err := sqlite.LoadScopeState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := scope.Compile(state.Version, state.Rules)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(repository, scope.NewManager(compiled), hub, testLimits)
	t.Cleanup(service.Close)
	return service
}

func replaceRulesAndWait(t *testing.T, service *Service, version int64, rules []scope.Rule) scope.State {
	t.Helper()
	state, err := service.ReplaceRules(context.Background(), version, rules)
	if err != nil {
		t.Fatal(err)
	}
	waitForRebuildStatus(t, service, "active")
	return state
}

func includeRule(host, path string) []scope.Rule {
	return []scope.Rule{{Enabled: true, Action: scope.ActionInclude, Scheme: "https", HostPattern: host, PathPrefix: path}}
}

func saveScopedExchange(t *testing.T, repository *store.SQLiteStore, host, path string, inScope bool, version int64) *store.Exchange {
	t.Helper()
	exchange := &store.Exchange{
		Method: "GET", Scheme: "https", Host: host, Path: path, Status: 200, StartedAt: time.Now().UTC(),
		InScope: inScope, ScopeVersion: version, Request: store.RequestData{Headers: map[string][]string{}}, Response: store.ResponseData{Headers: map[string][]string{}},
	}
	if err := repository.SaveExchange(context.Background(), exchange); err != nil {
		t.Fatal(err)
	}
	return exchange
}

func firstEndpointID(t *testing.T, tree []store.TargetTreeNode) int64 {
	t.Helper()
	var visit func([]store.TargetTreeNode) int64
	visit = func(nodes []store.TargetTreeNode) int64 {
		for _, node := range nodes {
			if node.ID != 0 {
				return node.ID
			}
			if id := visit(node.Children); id != 0 {
				return id
			}
		}
		return 0
	}
	if id := visit(tree); id != 0 {
		return id
	}
	t.Fatalf("tree has no endpoint: %#v", tree)
	return 0
}

func treeHasPath(tree []store.TargetTreeNode, path string) bool {
	for _, node := range tree {
		if node.Path == path || treeHasPath(node.Children, path) {
			return true
		}
	}
	return false
}

func waitForSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func waitForRebuildWorker(t *testing.T, service *Service) {
	t.Helper()
	service.mu.Lock()
	done := service.rebuildDone
	service.mu.Unlock()
	if done == nil {
		t.Fatal("service has no rebuild worker")
	}
	waitForSignal(t, done, "rebuild worker")
}

func collectUntilEvent(t *testing.T, subscriber <-chan events.Event, eventType string) []events.Event {
	t.Helper()
	deadline := time.After(2 * time.Second)
	received := make([]events.Event, 0)
	for {
		select {
		case event := <-subscriber:
			received = append(received, event)
			if event.Type == eventType {
				return received
			}
		case <-deadline:
			t.Fatalf("events = %v, missing %q", eventTypes(received), eventType)
		}
	}
}

func drainEvents(subscriber <-chan events.Event) {
	for {
		select {
		case <-subscriber:
		default:
			return
		}
	}
}

func eventTypes(received []events.Event) []string {
	types := make([]string, len(received))
	for index := range received {
		types[index] = received[index].Type
	}
	return types
}

type controlledRepository struct {
	Repository

	mu                      sync.Mutex
	pageFailure             error
	upsertFailure           error
	blockPage               bool
	pageStarted             chan struct{}
	releasePage             chan struct{}
	pageSignalOnce          sync.Once
	blockUpsert             bool
	upsertStarted           chan struct{}
	releaseUpsert           chan struct{}
	upsertOnce              sync.Once
	blockActivation         bool
	activationStarted       chan struct{}
	releaseActivation       chan struct{}
	activationOnce          sync.Once
	progressFailures        []error
	markFailures            []error
	permanentMarkError      error
	markAttempts            int
	upsertAttempts          int
	cancelled               []int64
	prunes                  int
	cancelAfterScopeReplace context.CancelFunc
	generationFailure       error
}

func newControlledRepository(repository Repository) *controlledRepository {
	return &controlledRepository{Repository: repository}
}

func (r *controlledRepository) ReplaceScopeRules(ctx context.Context, expectedVersion int64, rules []scope.Rule) (scope.State, error) {
	state, err := r.Repository.ReplaceScopeRules(ctx, expectedVersion, rules)
	if err == nil && r.cancelAfterScopeReplace != nil {
		r.cancelAfterScopeReplace()
	}
	return state, err
}

func (r *controlledRepository) CreateTargetGeneration(ctx context.Context, scopeVersion, total int64) (int64, error) {
	if r.generationFailure != nil {
		return 0, r.generationFailure
	}
	return r.Repository.CreateTargetGeneration(ctx, scopeVersion, total)
}

func (r *controlledRepository) ListExchangesPage(ctx context.Context, afterID, throughID int64, limit int) ([]store.Exchange, error) {
	r.mu.Lock()
	failure := r.pageFailure
	block := r.blockPage
	started := r.pageStarted
	release := r.releasePage
	r.mu.Unlock()
	if failure != nil {
		return nil, failure
	}
	if block {
		r.pageSignalOnce.Do(func() { close(started) })
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return r.Repository.ListExchangesPage(ctx, afterID, throughID, limit)
}

func (r *controlledRepository) UpsertTargetObservation(ctx context.Context, generationID int64, observation store.TargetObservation) error {
	r.mu.Lock()
	r.upsertAttempts++
	failure := r.upsertFailure
	block := r.blockUpsert
	started := r.upsertStarted
	release := r.releaseUpsert
	r.mu.Unlock()
	if failure != nil {
		return failure
	}
	if block {
		r.upsertOnce.Do(func() { close(started) })
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return r.Repository.UpsertTargetObservation(ctx, generationID, observation)
}

func (r *controlledRepository) CancelTargetGeneration(ctx context.Context, generationID int64) error {
	err := r.Repository.CancelTargetGeneration(ctx, generationID)
	if err == nil {
		r.mu.Lock()
		r.cancelled = append(r.cancelled, generationID)
		r.mu.Unlock()
	}
	return err
}

func (r *controlledRepository) ActivateTargetGeneration(ctx context.Context, generationID int64) error {
	r.mu.Lock()
	block := r.blockActivation
	started := r.activationStarted
	release := r.releaseActivation
	r.mu.Unlock()
	if block {
		r.activationOnce.Do(func() { close(started) })
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return r.Repository.ActivateTargetGeneration(ctx, generationID)
}

func (r *controlledRepository) SetTargetGenerationProgress(ctx context.Context, generationID, processed int64) error {
	r.mu.Lock()
	var failure error
	if len(r.progressFailures) > 0 {
		failure = r.progressFailures[0]
		r.progressFailures = r.progressFailures[1:]
	}
	r.mu.Unlock()
	if failure != nil {
		return failure
	}
	return r.Repository.SetTargetGenerationProgress(ctx, generationID, processed)
}

func (r *controlledRepository) FailTargetGeneration(ctx context.Context, generationID int64, generationError string) error {
	r.mu.Lock()
	r.markAttempts++
	failure := r.permanentMarkError
	if len(r.markFailures) > 0 {
		failure = r.markFailures[0]
		r.markFailures = r.markFailures[1:]
	}
	r.mu.Unlock()
	if failure != nil {
		return failure
	}
	return r.Repository.FailTargetGeneration(ctx, generationID, generationError)
}

func (r *controlledRepository) PruneRetiredTargetGenerations(ctx context.Context) error {
	err := r.Repository.PruneRetiredTargetGenerations(ctx)
	if err == nil {
		r.mu.Lock()
		r.prunes++
		r.mu.Unlock()
	}
	return err
}

func (r *controlledRepository) setPageFailure(err error) {
	r.mu.Lock()
	r.pageFailure = err
	r.mu.Unlock()
}

func (r *controlledRepository) setUpsertFailure(err error) {
	r.mu.Lock()
	r.upsertFailure = err
	r.mu.Unlock()
}

func (r *controlledRepository) setProgressFailures(failures ...error) {
	r.mu.Lock()
	r.progressFailures = append([]error(nil), failures...)
	r.mu.Unlock()
}

func (r *controlledRepository) setFailureMarkFailures(failures ...error) {
	r.mu.Lock()
	r.markFailures = append([]error(nil), failures...)
	r.mu.Unlock()
}

func (r *controlledRepository) setPermanentFailureMarkError(err error) {
	r.mu.Lock()
	r.permanentMarkError = err
	r.mu.Unlock()
}

func (r *controlledRepository) blockPages() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.blockPage = true
	r.pageStarted = make(chan struct{})
	r.releasePage = make(chan struct{})
	r.pageSignalOnce = sync.Once{}
	return r.pageStarted
}

func (r *controlledRepository) unblockFuturePages() {
	r.mu.Lock()
	r.blockPage = false
	r.mu.Unlock()
}

func (r *controlledRepository) releaseBlockedPages() {
	r.mu.Lock()
	release := r.releasePage
	r.blockPage = false
	r.mu.Unlock()
	close(release)
}

func (r *controlledRepository) blockUpserts() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.blockUpsert = true
	r.upsertStarted = make(chan struct{})
	r.releaseUpsert = make(chan struct{})
	r.upsertOnce = sync.Once{}
	return r.upsertStarted
}

func (r *controlledRepository) releaseBlockedUpserts() {
	r.mu.Lock()
	release := r.releaseUpsert
	r.blockUpsert = false
	r.mu.Unlock()
	close(release)
}

func (r *controlledRepository) blockActivations() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.blockActivation = true
	r.activationStarted = make(chan struct{})
	r.releaseActivation = make(chan struct{})
	r.activationOnce = sync.Once{}
	return r.activationStarted
}

func (r *controlledRepository) releaseBlockedActivations() {
	r.mu.Lock()
	release := r.releaseActivation
	r.blockActivation = false
	r.mu.Unlock()
	close(release)
}

func (r *controlledRepository) cancelCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.cancelled)
}

func (r *controlledRepository) pruneCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.prunes
}

func (r *controlledRepository) failureMarkAttemptCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.markAttempts
}

func (r *controlledRepository) upsertAttemptCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.upsertAttempts
}

type sqliteBusyTestError struct{}

func (sqliteBusyTestError) Error() string { return "database is locked" }
func (sqliteBusyTestError) Code() int     { return 5 }
