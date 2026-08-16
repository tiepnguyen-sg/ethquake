// Package observer owns Ethereum measurement polling and lifecycle.
package observer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/tiepnguyen-sg/ethquake/internal/beacon"
	"golang.org/x/sync/errgroup"
)

type BeaconClient interface {
	Spec(context.Context) (beacon.Spec, error)
	Head(context.Context) (beacon.Head, error)
	Finality(context.Context) (beacon.Finality, error)
}

type Target struct {
	Name   string
	Beacon BeaconClient
}

type SpecObservation struct {
	Timestamp time.Time   `json:"timestamp"`
	Target    string      `json:"target"`
	Spec      beacon.Spec `json:"spec"`
}

type BeaconObservation struct {
	Timestamp        time.Time       `json:"timestamp"`
	Target           string          `json:"target"`
	Head             beacon.Head     `json:"head"`
	Finality         beacon.Finality `json:"finality"`
	FinalityLagSlots uint64          `json:"finality_lag_slots"`
	PollDuration     time.Duration   `json:"-"`
}

type PollFailure struct {
	Timestamp    time.Time     `json:"timestamp"`
	Target       string        `json:"target"`
	Protocol     string        `json:"protocol"`
	Operation    string        `json:"operation"`
	Error        string        `json:"error"`
	PollDuration time.Duration `json:"-"`
}

type Recorder interface {
	RecordSpec(SpecObservation) error
	RecordBeacon(BeaconObservation) error
	RecordHeadComparison(HeadComparison) error
	RecordPollFailure(PollFailure) error
}

type Options struct {
	PollInterval     time.Duration
	HeadHistoryLimit int
	Now              func() time.Time
}

type Observer struct {
	targets        []Target
	recorder       Recorder
	pollInterval   time.Duration
	now            func() time.Time
	headComparator *HeadComparator

	initializeOnce sync.Once
	spec           beacon.Spec
	initializeErr  error
}

func New(targets []Target, recorder Recorder, options Options) (*Observer, error) {
	if len(targets) == 0 {
		return nil, errors.New("at least one Beacon target is required")
	}
	if recorder == nil {
		return nil, errors.New("recorder is required")
	}
	if options.PollInterval <= 0 {
		return nil, errors.New("poll interval must be positive")
	}
	if options.HeadHistoryLimit < 2 {
		return nil, errors.New("head history limit must be at least 2")
	}
	if options.Now == nil {
		options.Now = time.Now
	}

	seen := make(map[string]struct{}, len(targets))
	targetCopy := append([]Target(nil), targets...)
	for _, target := range targetCopy {
		if target.Name == "" {
			return nil, errors.New("Beacon target name must not be empty")
		}
		if target.Beacon == nil {
			return nil, fmt.Errorf("Beacon client for target %q is required", target.Name)
		}
		if _, exists := seen[target.Name]; exists {
			return nil, fmt.Errorf("Beacon target name %q is duplicated", target.Name)
		}
		seen[target.Name] = struct{}{}
	}
	sort.Slice(targetCopy, func(left, right int) bool {
		return targetCopy[left].Name < targetCopy[right].Name
	})

	var headComparator *HeadComparator
	if len(targetCopy) > 1 {
		targetNames := make([]string, 0, len(targetCopy))
		for _, target := range targetCopy {
			targetNames = append(targetNames, target.Name)
		}
		var comparatorErr error
		headComparator, comparatorErr = NewHeadComparator(targetNames, options.HeadHistoryLimit)
		if comparatorErr != nil {
			return nil, fmt.Errorf("create head comparator: %w", comparatorErr)
		}
	}

	return &Observer{
		targets:        targetCopy,
		recorder:       recorder,
		pollInterval:   options.PollInterval,
		now:            options.Now,
		headComparator: headComparator,
	}, nil
}

func (o *Observer) Run(ctx context.Context) error {
	if err := o.initialize(ctx); err != nil {
		return err
	}

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

func (o *Observer) initialize(ctx context.Context) error {
	o.initializeOnce.Do(func() {
		specs := make([]beacon.Spec, len(o.targets))
		group, groupContext := errgroup.WithContext(ctx)
		for index, target := range o.targets {
			index, target := index, target
			group.Go(func() error {
				spec, err := target.Beacon.Spec(groupContext)
				if err != nil {
					return fmt.Errorf("fetch runtime spec from target %q: %w", target.Name, err)
				}
				specs[index] = spec
				return nil
			})
		}
		if err := group.Wait(); err != nil {
			o.initializeErr = err
			return
		}

		reference := specs[0]
		for index := 1; index < len(specs); index++ {
			if !specs[index].Compatible(reference) {
				o.initializeErr = fmt.Errorf(
					"runtime spec mismatch: target %q has %+v; target %q has %+v",
					o.targets[index].Name,
					specs[index],
					o.targets[0].Name,
					reference,
				)
				return
			}
		}

		o.spec = reference
		for index, target := range o.targets {
			if err := o.recorder.RecordSpec(SpecObservation{
				Timestamp: o.now().UTC(),
				Target:    target.Name,
				Spec:      specs[index],
			}); err != nil {
				o.initializeErr = fmt.Errorf("record runtime spec for target %q: %w", target.Name, err)
				return
			}
		}
	})
	return o.initializeErr
}

func (o *Observer) runTarget(ctx context.Context, target Target) error {
	if err := o.pollAndRecord(ctx, target); err != nil {
		return err
	}

	ticker := time.NewTicker(o.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := o.pollAndRecord(ctx, target); err != nil {
				return err
			}
		}
	}
}

func (o *Observer) pollAndRecord(ctx context.Context, target Target) error {
	started := o.now()
	head, finality, err := pollBeacon(ctx, target.Beacon)
	duration := o.now().Sub(started)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		failure := PollFailure{
			Timestamp:    o.now().UTC(),
			Target:       target.Name,
			Protocol:     "beacon",
			Operation:    "beacon_poll",
			Error:        err.Error(),
			PollDuration: duration,
		}
		if recordErr := o.recorder.RecordPollFailure(failure); recordErr != nil {
			return fmt.Errorf("record poll failure for target %q: %w", target.Name, recordErr)
		}
		return nil
	}

	lag, err := FinalityLag(head.Slot, finality.Epoch, o.spec.SlotsPerEpoch)
	if err != nil {
		failure := PollFailure{
			Timestamp:    o.now().UTC(),
			Target:       target.Name,
			Protocol:     "beacon",
			Operation:    "calculate_finality_lag",
			Error:        err.Error(),
			PollDuration: duration,
		}
		if recordErr := o.recorder.RecordPollFailure(failure); recordErr != nil {
			return fmt.Errorf("record calculation failure for target %q: %w", target.Name, recordErr)
		}
		return nil
	}

	observation := BeaconObservation{
		Timestamp:        o.now().UTC(),
		Target:           target.Name,
		Head:             head,
		Finality:         finality,
		FinalityLagSlots: lag,
		PollDuration:     duration,
	}
	if err := o.recorder.RecordBeacon(observation); err != nil {
		return fmt.Errorf("record Beacon observation for target %q: %w", target.Name, err)
	}
	if o.headComparator != nil {
		comparison := o.headComparator.Observe(observation.Timestamp, target.Name, head)
		if comparison != nil {
			if err := o.recorder.RecordHeadComparison(*comparison); err != nil {
				return fmt.Errorf("record head comparison at slot %d: %w", comparison.Slot, err)
			}
		}
	}
	return nil
}

func pollBeacon(ctx context.Context, client BeaconClient) (beacon.Head, beacon.Finality, error) {
	var head beacon.Head
	var finality beacon.Finality
	group, groupContext := errgroup.WithContext(ctx)
	group.Go(func() error {
		value, err := client.Head(groupContext)
		if err != nil {
			return fmt.Errorf("fetch head: %w", err)
		}
		head = value
		return nil
	})
	group.Go(func() error {
		value, err := client.Finality(groupContext)
		if err != nil {
			return fmt.Errorf("fetch finality: %w", err)
		}
		finality = value
		return nil
	})
	if err := group.Wait(); err != nil {
		return beacon.Head{}, beacon.Finality{}, err
	}
	return head, finality, nil
}

func FinalityLag(headSlot, finalizedEpoch, slotsPerEpoch uint64) (uint64, error) {
	if slotsPerEpoch == 0 {
		return 0, errors.New("SLOTS_PER_EPOCH must be positive")
	}
	if finalizedEpoch > math.MaxUint64/slotsPerEpoch {
		return 0, errors.New("finalized epoch to slot conversion overflows uint64")
	}
	finalizedSlot := finalizedEpoch * slotsPerEpoch
	if headSlot < finalizedSlot {
		return 0, fmt.Errorf("head slot %d precedes finalized epoch start slot %d", headSlot, finalizedSlot)
	}
	return headSlot - finalizedSlot, nil
}
