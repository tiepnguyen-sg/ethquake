package observer

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/tiepnguyen-sg/ethquake/internal/execution"
	"golang.org/x/sync/errgroup"
)

type ExecutionClient interface {
	SubscribeNewHeads(context.Context, func(execution.Head) error) error
}

type ExecutionTarget struct {
	Name      string
	Execution ExecutionClient
}

type ExecutionObservation struct {
	Timestamp  time.Time      `json:"timestamp"`
	Target     string         `json:"target"`
	Head       execution.Head `json:"head"`
	Continuity bool           `json:"continuity"`
}

type ExecutionRecorder interface {
	RecordExecution(ExecutionObservation) error
	RecordReorg(ReorgObservation) error
	RecordPollFailure(PollFailure) error
}

type ExecutionOptions struct {
	HistoryLimit     int
	ReconnectInitial time.Duration
	ReconnectMaximum time.Duration
	Now              func() time.Time
}

type ExecutionObserver struct {
	targets          []ExecutionTarget
	recorder         ExecutionRecorder
	historyLimit     int
	reconnectInitial time.Duration
	reconnectMaximum time.Duration
	now              func() time.Time
}

func NewExecutionObserver(targets []ExecutionTarget, recorder ExecutionRecorder, options ExecutionOptions) (*ExecutionObserver, error) {
	if len(targets) == 0 {
		return nil, errors.New("at least one execution target is required")
	}
	if recorder == nil {
		return nil, errors.New("execution recorder is required")
	}
	if options.HistoryLimit < 2 {
		return nil, errors.New("reorg history limit must be at least 2")
	}
	if options.ReconnectInitial <= 0 || options.ReconnectMaximum < options.ReconnectInitial {
		return nil, errors.New("execution reconnect delays are invalid")
	}
	if options.Now == nil {
		options.Now = time.Now
	}

	seen := make(map[string]struct{}, len(targets))
	targetCopy := append([]ExecutionTarget(nil), targets...)
	for _, target := range targetCopy {
		if target.Name == "" {
			return nil, errors.New("execution target name must not be empty")
		}
		if target.Execution == nil {
			return nil, fmt.Errorf("execution client for target %q is required", target.Name)
		}
		if _, exists := seen[target.Name]; exists {
			return nil, fmt.Errorf("execution target name %q is duplicated", target.Name)
		}
		seen[target.Name] = struct{}{}
	}
	sort.Slice(targetCopy, func(left, right int) bool {
		return targetCopy[left].Name < targetCopy[right].Name
	})

	return &ExecutionObserver{
		targets:          targetCopy,
		recorder:         recorder,
		historyLimit:     options.HistoryLimit,
		reconnectInitial: options.ReconnectInitial,
		reconnectMaximum: options.ReconnectMaximum,
		now:              options.Now,
	}, nil
}

func (o *ExecutionObserver) Run(ctx context.Context) error {
	group, groupContext := errgroup.WithContext(ctx)
	for _, target := range o.targets {
		target := target
		group.Go(func() error {
			return o.runTarget(groupContext, target)
		})
	}
	if err := group.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func (o *ExecutionObserver) runTarget(ctx context.Context, target ExecutionTarget) error {
	reconnectDelay := o.reconnectInitial
	for {
		tracker, err := NewReorgTracker(target.Name, o.historyLimit)
		if err != nil {
			return err
		}
		receivedHead := false
		err = target.Execution.SubscribeNewHeads(ctx, func(head execution.Head) error {
			receivedHead = true
			result := tracker.Observe(head)
			observation := ExecutionObservation{
				Timestamp:  o.now().UTC(),
				Target:     target.Name,
				Head:       head,
				Continuity: result.Continuity,
			}
			if err := o.recorder.RecordExecution(observation); err != nil {
				return fmt.Errorf("record execution head: %w", err)
			}
			if result.GapOperation != "" {
				if err := o.recorder.RecordPollFailure(PollFailure{
					Timestamp: o.now().UTC(),
					Target:    target.Name,
					Protocol:  "execution",
					Operation: result.GapOperation,
					Error:     result.GapError,
				}); err != nil {
					return fmt.Errorf("record execution continuity gap: %w", err)
				}
			}
			if result.Reorg != nil {
				result.Reorg.Timestamp = o.now().UTC()
				if err := o.recorder.RecordReorg(*result.Reorg); err != nil {
					return fmt.Errorf("record execution reorg: %w", err)
				}
			}
			return nil
		})
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return nil
		}
		if err == nil {
			err = errors.New("execution subscription ended without an error")
		}
		if err := o.recorder.RecordPollFailure(PollFailure{
			Timestamp: o.now().UTC(),
			Target:    target.Name,
			Protocol:  "execution",
			Operation: "execution_subscription",
			Error:     err.Error(),
		}); err != nil {
			return fmt.Errorf("record execution subscription failure for target %q: %w", target.Name, err)
		}

		if receivedHead {
			reconnectDelay = o.reconnectInitial
		}
		timer := time.NewTimer(reconnectDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
		if reconnectDelay < o.reconnectMaximum {
			if reconnectDelay > o.reconnectMaximum/2 {
				reconnectDelay = o.reconnectMaximum
			} else {
				reconnectDelay *= 2
			}
		}
	}
}

type MultiExecutionRecorder struct {
	recorders []ExecutionRecorder
}

func NewMultiExecutionRecorder(recorders ...ExecutionRecorder) (*MultiExecutionRecorder, error) {
	if len(recorders) == 0 {
		return nil, errors.New("at least one execution recorder is required")
	}
	copyOfRecorders := append([]ExecutionRecorder(nil), recorders...)
	for index, recorder := range copyOfRecorders {
		if recorder == nil {
			return nil, fmt.Errorf("execution recorder %d is nil", index)
		}
	}
	return &MultiExecutionRecorder{recorders: copyOfRecorders}, nil
}

func (m *MultiExecutionRecorder) RecordExecution(observation ExecutionObservation) error {
	for index, recorder := range m.recorders {
		if err := recorder.RecordExecution(observation); err != nil {
			return fmt.Errorf("execution recorder %d: %w", index, err)
		}
	}
	return nil
}

func (m *MultiExecutionRecorder) RecordReorg(observation ReorgObservation) error {
	for index, recorder := range m.recorders {
		if err := recorder.RecordReorg(observation); err != nil {
			return fmt.Errorf("execution recorder %d: %w", index, err)
		}
	}
	return nil
}

func (m *MultiExecutionRecorder) RecordPollFailure(failure PollFailure) error {
	for index, recorder := range m.recorders {
		if err := recorder.RecordPollFailure(failure); err != nil {
			return fmt.Errorf("execution recorder %d: %w", index, err)
		}
	}
	return nil
}
