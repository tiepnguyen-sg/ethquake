package observer

import (
	"sync"

	"github.com/tiepnguyen-sg/ethquake/internal/beacon"
)

// DatasetSnapshot is a consistent copy of observations recorded so far.
type DatasetSnapshot struct {
	Specs           map[string]beacon.Spec
	Genesis         map[string]beacon.Genesis
	Beacon          []BeaconObservation
	HeadComparisons []HeadComparison
	Gaps            []PollFailure
}

// DatasetRecorder retains one run's bounded in-memory measurement dataset.
// The orchestrator owns its lifetime; the standalone Observer does not depend
// on experiment concepts.
type DatasetRecorder struct {
	mu              sync.RWMutex
	specs           map[string]beacon.Spec
	genesis         map[string]beacon.Genesis
	beacon          []BeaconObservation
	headComparisons []HeadComparison
	gaps            []PollFailure
	changed         chan struct{}
}

func NewDatasetRecorder() *DatasetRecorder {
	return &DatasetRecorder{
		specs:   make(map[string]beacon.Spec),
		genesis: make(map[string]beacon.Genesis),
		changed: make(chan struct{}, 1),
	}
}

func (recorder *DatasetRecorder) Changed() <-chan struct{} {
	return recorder.changed
}

func (recorder *DatasetRecorder) RecordSpec(observation SpecObservation) error {
	recorder.mu.Lock()
	recorder.specs[observation.Target] = cloneSpec(observation.Spec)
	recorder.genesis[observation.Target] = observation.Genesis
	recorder.mu.Unlock()
	recorder.notify()
	return nil
}

func (recorder *DatasetRecorder) RecordBeacon(observation BeaconObservation) error {
	recorder.mu.Lock()
	recorder.beacon = append(recorder.beacon, observation)
	recorder.mu.Unlock()
	recorder.notify()
	return nil
}

func (recorder *DatasetRecorder) RecordHeadComparison(comparison HeadComparison) error {
	recorder.mu.Lock()
	comparison.Roots = cloneMap(comparison.Roots)
	recorder.headComparisons = append(recorder.headComparisons, comparison)
	recorder.mu.Unlock()
	recorder.notify()
	return nil
}

func (recorder *DatasetRecorder) RecordPollFailure(failure PollFailure) error {
	recorder.mu.Lock()
	recorder.gaps = append(recorder.gaps, failure)
	recorder.mu.Unlock()
	recorder.notify()
	return nil
}

func (recorder *DatasetRecorder) Snapshot() DatasetSnapshot {
	recorder.mu.RLock()
	defer recorder.mu.RUnlock()
	specs := make(map[string]beacon.Spec, len(recorder.specs))
	for target, spec := range recorder.specs {
		specs[target] = cloneSpec(spec)
	}
	genesis := make(map[string]beacon.Genesis, len(recorder.genesis))
	for target, value := range recorder.genesis {
		genesis[target] = value
	}
	comparisons := make([]HeadComparison, len(recorder.headComparisons))
	for index, comparison := range recorder.headComparisons {
		comparison.Roots = cloneMap(comparison.Roots)
		comparisons[index] = comparison
	}
	return DatasetSnapshot{
		Specs:           specs,
		Genesis:         genesis,
		Beacon:          append([]BeaconObservation(nil), recorder.beacon...),
		HeadComparisons: comparisons,
		Gaps:            append([]PollFailure(nil), recorder.gaps...),
	}
}

func (recorder *DatasetRecorder) notify() {
	select {
	case recorder.changed <- struct{}{}:
	default:
	}
}

func cloneSpec(value beacon.Spec) beacon.Spec {
	value.ForkEpochs = cloneMap(value.ForkEpochs)
	return value
}

func cloneMap(input map[string]string) map[string]string {
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
