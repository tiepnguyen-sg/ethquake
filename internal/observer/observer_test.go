package observer

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiepnguyen-sg/ethquake/internal/beacon"
)

type fakeBeaconClient struct {
	spec        beacon.Spec
	head        beacon.Head
	finality    beacon.Finality
	specErr     error
	headErr     error
	finalityErr error
}

func (f *fakeBeaconClient) Spec(context.Context) (beacon.Spec, error) {
	return f.spec, f.specErr
}

func (f *fakeBeaconClient) Head(context.Context) (beacon.Head, error) {
	return f.head, f.headErr
}

func (f *fakeBeaconClient) Finality(context.Context) (beacon.Finality, error) {
	return f.finality, f.finalityErr
}

type memoryRecorder struct {
	mu          sync.Mutex
	specs       []SpecObservation
	beacons     []BeaconObservation
	failures    []PollFailure
	comparisons []HeadComparison
	onBeacon    func()
}

func (r *memoryRecorder) RecordSpec(observation SpecObservation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.specs = append(r.specs, observation)
	return nil
}

func (r *memoryRecorder) RecordBeacon(observation BeaconObservation) error {
	r.mu.Lock()
	r.beacons = append(r.beacons, observation)
	onBeacon := r.onBeacon
	r.mu.Unlock()
	if onBeacon != nil {
		onBeacon()
	}
	return nil
}

func (r *memoryRecorder) RecordHeadComparison(comparison HeadComparison) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.comparisons = append(r.comparisons, comparison)
	return nil
}

func (r *memoryRecorder) RecordPollFailure(failure PollFailure) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures = append(r.failures, failure)
	return nil
}

func TestObserverRecordsRuntimeSpecAndBeaconMeasurement(t *testing.T) {
	client := &fakeBeaconClient{
		spec:     beacon.Spec{SecondsPerSlot: 12, SlotsPerEpoch: 32},
		head:     beacon.Head{Slot: 168, Root: root("1"), ParentRoot: root("2")},
		finality: beacon.Finality{Epoch: 3, Root: root("3")},
	}
	recorder := &memoryRecorder{}
	observer, err := New(
		[]Target{{Name: "lighthouse", Beacon: client}},
		recorder,
		Options{PollInterval: time.Second, HeadHistoryLimit: 16, Now: func() time.Time { return time.Unix(100, 0) }},
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := observer.initialize(context.Background()); err != nil {
		t.Fatalf("initialize() error = %v", err)
	}
	if err := observer.pollAndRecord(context.Background(), observer.targets[0]); err != nil {
		t.Fatalf("pollAndRecord() error = %v", err)
	}

	if len(recorder.specs) != 1 {
		t.Fatalf("recorded specs = %d, want 1", len(recorder.specs))
	}
	if len(recorder.beacons) != 1 {
		t.Fatalf("recorded Beacon observations = %d, want 1", len(recorder.beacons))
	}
	if recorder.beacons[0].FinalityLagSlots != 72 {
		t.Fatalf("finality lag = %d, want 72", recorder.beacons[0].FinalityLagSlots)
	}
	if len(recorder.failures) != 0 {
		t.Fatalf("recorded failures = %d, want 0", len(recorder.failures))
	}
}

func TestObserverRecordsGapInsteadOfZeroOnPollFailure(t *testing.T) {
	client := &fakeBeaconClient{
		spec:        beacon.Spec{SecondsPerSlot: 12, SlotsPerEpoch: 32},
		headErr:     errors.New("head unavailable"),
		finality:    beacon.Finality{Epoch: 3, Root: root("3")},
		finalityErr: nil,
	}
	recorder := &memoryRecorder{}
	observer, err := New(
		[]Target{{Name: "teku", Beacon: client}},
		recorder,
		Options{PollInterval: time.Second, HeadHistoryLimit: 16},
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := observer.initialize(context.Background()); err != nil {
		t.Fatalf("initialize() error = %v", err)
	}
	if err := observer.pollAndRecord(context.Background(), observer.targets[0]); err != nil {
		t.Fatalf("pollAndRecord() error = %v", err)
	}

	if len(recorder.beacons) != 0 {
		t.Fatalf("recorded Beacon observations = %d, want 0", len(recorder.beacons))
	}
	if len(recorder.failures) != 1 {
		t.Fatalf("recorded failures = %d, want 1", len(recorder.failures))
	}
	if !strings.Contains(recorder.failures[0].Error, "head unavailable") {
		t.Fatalf("failure = %q", recorder.failures[0].Error)
	}
}

func TestObserverRejectsRuntimeSpecMismatch(t *testing.T) {
	recorder := &memoryRecorder{}
	observer, err := New(
		[]Target{
			{Name: "lighthouse", Beacon: &fakeBeaconClient{spec: beacon.Spec{
				SecondsPerSlot: 12,
				SlotsPerEpoch:  32,
				ForkEpochs:     map[string]string{"GLOAS_FORK_EPOCH": "18446744073709551615"},
			}}},
			{Name: "teku", Beacon: &fakeBeaconClient{spec: beacon.Spec{
				SecondsPerSlot: 12,
				SlotsPerEpoch:  32,
				ForkEpochs:     map[string]string{"GLOAS_FORK_EPOCH": "10"},
			}}},
		},
		recorder,
		Options{PollInterval: time.Second, HeadHistoryLimit: 16},
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := observer.initialize(context.Background()); err == nil || !strings.Contains(err.Error(), "runtime spec mismatch") {
		t.Fatalf("initialize() error = %v", err)
	}
	if len(recorder.specs) != 0 {
		t.Fatalf("recorded specs = %d, want 0", len(recorder.specs))
	}
}

func TestObserverRunStopsWhenContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	recorder := &memoryRecorder{onBeacon: cancel}
	observer, err := New(
		[]Target{{
			Name: "lighthouse",
			Beacon: &fakeBeaconClient{
				spec:     beacon.Spec{SecondsPerSlot: 12, SlotsPerEpoch: 32},
				head:     beacon.Head{Slot: 168, Root: root("1"), ParentRoot: root("2")},
				finality: beacon.Finality{Epoch: 3, Root: root("3")},
			},
		}},
		recorder,
		Options{PollInterval: time.Hour, HeadHistoryLimit: 16},
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := observer.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestFinalityLag(t *testing.T) {
	tests := []struct {
		name           string
		headSlot       uint64
		finalizedEpoch uint64
		slotsPerEpoch  uint64
		want           uint64
		wantError      bool
	}{
		{name: "healthy value", headSlot: 168, finalizedEpoch: 3, slotsPerEpoch: 32, want: 72},
		{name: "valid zero", headSlot: 96, finalizedEpoch: 3, slotsPerEpoch: 32, want: 0},
		{name: "zero slots per epoch", headSlot: 1, finalizedEpoch: 0, slotsPerEpoch: 0, wantError: true},
		{name: "underflow", headSlot: 95, finalizedEpoch: 3, slotsPerEpoch: 32, wantError: true},
		{name: "overflow", headSlot: math.MaxUint64, finalizedEpoch: math.MaxUint64, slotsPerEpoch: 2, wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := FinalityLag(test.headSlot, test.finalizedEpoch, test.slotsPerEpoch)
			if (err != nil) != test.wantError {
				t.Fatalf("FinalityLag() error = %v, wantError %t", err, test.wantError)
			}
			if got != test.want {
				t.Fatalf("FinalityLag() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestNewRejectsInvalidConfiguration(t *testing.T) {
	recorder := &memoryRecorder{}
	client := &fakeBeaconClient{}
	tests := []struct {
		name     string
		targets  []Target
		recorder Recorder
		interval time.Duration
	}{
		{name: "no targets", recorder: recorder, interval: time.Second},
		{name: "nil recorder", targets: []Target{{Name: "node", Beacon: client}}, interval: time.Second},
		{name: "empty name", targets: []Target{{Beacon: client}}, recorder: recorder, interval: time.Second},
		{name: "nil client", targets: []Target{{Name: "node"}}, recorder: recorder, interval: time.Second},
		{name: "duplicate name", targets: []Target{{Name: "node", Beacon: client}, {Name: "node", Beacon: client}}, recorder: recorder, interval: time.Second},
		{name: "invalid interval", targets: []Target{{Name: "node", Beacon: client}}, recorder: recorder},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := New(test.targets, test.recorder, Options{PollInterval: test.interval, HeadHistoryLimit: 16}); err == nil {
				t.Fatal("New() error = nil")
			}
		})
	}
}

func root(character string) string {
	return "0x" + strings.Repeat(character, 64)
}
