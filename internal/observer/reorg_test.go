package observer

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiepnguyen-sg/ethquake/internal/execution"
)

func TestReorgTrackerCalculatesKnownDepth(t *testing.T) {
	tracker, err := NewReorgTracker("geth", 16)
	if err != nil {
		t.Fatalf("NewReorgTracker() error = %v", err)
	}
	chain := []execution.Head{
		{Number: 100, Hash: executionHash("1"), ParentHash: executionHash("0")},
		{Number: 101, Hash: executionHash("2"), ParentHash: executionHash("1")},
		{Number: 102, Hash: executionHash("3"), ParentHash: executionHash("2")},
	}
	for _, head := range chain {
		if result := tracker.Observe(head); result.Reorg != nil || !result.Continuity {
			t.Fatalf("canonical head %+v produced result %+v", head, result)
		}
	}

	replacement := execution.Head{Number: 101, Hash: executionHash("4"), ParentHash: executionHash("1")}
	result := tracker.Observe(replacement)
	if result.Reorg == nil || result.Reorg.Depth == nil {
		t.Fatalf("replacement result = %+v", result)
	}
	if *result.Reorg.Depth != 2 {
		t.Fatalf("reorg depth = %d, want 2", *result.Reorg.Depth)
	}
	if result.Reorg.PreviousHead != chain[2] || result.Reorg.NewHead != replacement {
		t.Fatalf("reorg heads = %+v", result.Reorg)
	}
	if !result.Continuity {
		t.Fatal("known reorg should preserve continuity")
	}

	extension := execution.Head{Number: 102, Hash: executionHash("5"), ParentHash: executionHash("4")}
	if result := tracker.Observe(extension); result.Reorg != nil || !result.Continuity {
		t.Fatalf("replacement extension result = %+v", result)
	}
}

func TestReorgTrackerReportsUnknownDepthWithoutInventingZero(t *testing.T) {
	tracker, err := NewReorgTracker("reth", 16)
	if err != nil {
		t.Fatalf("NewReorgTracker() error = %v", err)
	}
	tracker.Observe(execution.Head{Number: 100, Hash: executionHash("1"), ParentHash: executionHash("0")})
	result := tracker.Observe(execution.Head{Number: 100, Hash: executionHash("2"), ParentHash: executionHash("9")})
	if result.Reorg == nil {
		t.Fatal("reorg = nil")
	}
	if result.Reorg.Depth != nil {
		t.Fatalf("unknown reorg depth = %d, want nil", *result.Reorg.Depth)
	}
	if result.Continuity {
		t.Fatal("unknown reorg depth should mark continuity false")
	}
}

func TestReorgTrackerDistinguishesSequenceGapFromReorg(t *testing.T) {
	tracker, err := NewReorgTracker("geth", 16)
	if err != nil {
		t.Fatalf("NewReorgTracker() error = %v", err)
	}
	tracker.Observe(execution.Head{Number: 100, Hash: executionHash("1"), ParentHash: executionHash("0")})
	result := tracker.Observe(execution.Head{Number: 103, Hash: executionHash("4"), ParentHash: executionHash("3")})
	if result.Reorg != nil {
		t.Fatalf("sequence gap was classified as reorg: %+v", result.Reorg)
	}
	if result.Continuity || result.GapOperation != "execution_continuity" {
		t.Fatalf("sequence gap result = %+v", result)
	}
}

func TestReorgTrackerHistoryLimitMakesOldDepthUnknown(t *testing.T) {
	tracker, err := NewReorgTracker("geth", 2)
	if err != nil {
		t.Fatalf("NewReorgTracker() error = %v", err)
	}
	tracker.Observe(execution.Head{Number: 100, Hash: executionHash("1"), ParentHash: executionHash("0")})
	tracker.Observe(execution.Head{Number: 101, Hash: executionHash("2"), ParentHash: executionHash("1")})
	tracker.Observe(execution.Head{Number: 102, Hash: executionHash("3"), ParentHash: executionHash("2")})
	result := tracker.Observe(execution.Head{Number: 101, Hash: executionHash("4"), ParentHash: executionHash("1")})
	if result.Reorg == nil || result.Reorg.Depth != nil {
		t.Fatalf("trimmed ancestor result = %+v", result)
	}
}

type fakeExecutionClient struct {
	head execution.Head
	err  error
}

type closedExecutionClient struct{}

func (*closedExecutionClient) SubscribeNewHeads(context.Context, func(execution.Head) error) error {
	return nil
}

func (f *fakeExecutionClient) SubscribeNewHeads(ctx context.Context, receive func(execution.Head) error) error {
	if f.err != nil {
		return f.err
	}
	if err := receive(f.head); err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}

type executionMemoryRecorder struct {
	mu            sync.Mutex
	executions    []ExecutionObservation
	reorgs        []ReorgObservation
	failures      []PollFailure
	onExecution   func()
	onPollFailure func()
}

func (r *executionMemoryRecorder) RecordExecution(observation ExecutionObservation) error {
	r.mu.Lock()
	r.executions = append(r.executions, observation)
	callback := r.onExecution
	r.mu.Unlock()
	if callback != nil {
		callback()
	}
	return nil
}

func (r *executionMemoryRecorder) RecordReorg(observation ReorgObservation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reorgs = append(r.reorgs, observation)
	return nil
}

func (r *executionMemoryRecorder) RecordPollFailure(failure PollFailure) error {
	r.mu.Lock()
	r.failures = append(r.failures, failure)
	callback := r.onPollFailure
	r.mu.Unlock()
	if callback != nil {
		callback()
	}
	return nil
}

func TestExecutionObserverStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	recorder := &executionMemoryRecorder{onExecution: cancel}
	executionObserver, err := NewExecutionObserver(
		[]ExecutionTarget{{
			Name:      "geth",
			Execution: &fakeExecutionClient{head: execution.Head{Number: 1, Hash: executionHash("1"), ParentHash: executionHash("0")}},
		}},
		recorder,
		ExecutionOptions{
			HistoryLimit:     16,
			ReconnectInitial: time.Millisecond,
			ReconnectMaximum: time.Second,
		},
	)
	if err != nil {
		t.Fatalf("NewExecutionObserver() error = %v", err)
	}
	if err := executionObserver.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(recorder.executions) != 1 {
		t.Fatalf("execution observations = %d, want 1", len(recorder.executions))
	}
	if len(recorder.failures) != 0 {
		t.Fatalf("failures = %d, want 0", len(recorder.failures))
	}
}

func TestExecutionObserverRecordsSubscriptionFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	recorder := &executionMemoryRecorder{onPollFailure: cancel}
	executionObserver, err := NewExecutionObserver(
		[]ExecutionTarget{{Name: "reth", Execution: &fakeExecutionClient{err: errors.New("socket unavailable")}}},
		recorder,
		ExecutionOptions{
			HistoryLimit:     16,
			ReconnectInitial: time.Hour,
			ReconnectMaximum: time.Hour,
		},
	)
	if err != nil {
		t.Fatalf("NewExecutionObserver() error = %v", err)
	}
	if err := executionObserver.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(recorder.failures) != 1 {
		t.Fatalf("failures = %d, want 1", len(recorder.failures))
	}
	failure := recorder.failures[0]
	if failure.Protocol != "execution" || failure.Operation != "execution_subscription" || !strings.Contains(failure.Error, "socket unavailable") {
		t.Fatalf("failure = %+v", failure)
	}
}

func TestExecutionObserverRecordsUnexpectedCleanSubscriptionEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	recorder := &executionMemoryRecorder{onPollFailure: cancel}
	executionObserver, err := NewExecutionObserver(
		[]ExecutionTarget{{Name: "geth", Execution: &closedExecutionClient{}}},
		recorder,
		ExecutionOptions{
			HistoryLimit:     16,
			ReconnectInitial: time.Hour,
			ReconnectMaximum: time.Hour,
		},
	)
	if err != nil {
		t.Fatalf("NewExecutionObserver() error = %v", err)
	}
	if err := executionObserver.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(recorder.failures) != 1 || !strings.Contains(recorder.failures[0].Error, "ended without an error") {
		t.Fatalf("failures = %+v", recorder.failures)
	}
}

func executionHash(character string) string {
	return "0x" + strings.Repeat(character, 64)
}
