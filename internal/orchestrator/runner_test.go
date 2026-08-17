package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tiepnguyen-sg/ethquake/internal/beacon"
	"github.com/tiepnguyen-sg/ethquake/internal/observer"
	"github.com/tiepnguyen-sg/ethquake/internal/scenario"
	"github.com/tiepnguyen-sg/ethquake/internal/topology"
)

func TestValidatePlacementsRequiresControlledNodesAndResources(t *testing.T) {
	value := readScenario(t)
	placements := fixturePlacements(value)
	if err := topology.ValidatePlacements(value, placements); err != nil {
		t.Fatalf("topology.ValidatePlacements() error = %v", err)
	}
	placements[1].Node = placements[0].Node
	if err := topology.ValidatePlacements(value, placements); err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Fatalf("topology.ValidatePlacements() error = %v", err)
	}
}

func TestHeadDivergenceToleranceUsesConsecutiveComparableSlots(t *testing.T) {
	observations := []observer.BeaconObservation{
		{Target: "a", CurrentSlot: 10, Head: beacon.Head{Slot: 8, Root: "a8"}},
		{Target: "b", CurrentSlot: 10, Head: beacon.Head{Slot: 9, Root: "b9"}},
		{Target: "a", CurrentSlot: 11, Head: beacon.Head{Slot: 11, Root: "same11"}},
		{Target: "b", CurrentSlot: 11, Head: beacon.Head{Slot: 11, Root: "same11"}},
		{Target: "a", CurrentSlot: 12, Head: beacon.Head{Slot: 11, Root: "a11"}},
		{Target: "b", CurrentSlot: 12, Head: beacon.Head{Slot: 12, Root: "b12"}},
		{Target: "a", CurrentSlot: 13, Head: beacon.Head{Slot: 13, Root: "a13"}},
		{Target: "b", CurrentSlot: 13, Head: beacon.Head{Slot: 12, Root: "b12"}},
	}
	if !headDivergenceExceeded(observations, []string{"a", "b"}, 10, 13, 1) {
		t.Fatal("headDivergenceExceeded() = false")
	}
	if headDivergenceExceeded(observations[:6], []string{"a", "b"}, 10, 12, 1) {
		t.Fatal("single-slot mismatches exceeded tolerance")
	}
}

func TestRuntimeDurationRejectsOverflow(t *testing.T) {
	_, err := runtimeDuration(beacon.Spec{SecondsPerSlot: ^uint64(0)}, 2)
	if err == nil {
		t.Fatal("runtimeDuration() error = nil")
	}
}

func TestIncompleteRecoveryWindowIsAMeasurementGap(t *testing.T) {
	removedAt := time.Unix(100, 0).UTC()
	dataset := observer.DatasetSnapshot{Beacon: []observer.BeaconObservation{{Timestamp: removedAt.Add(time.Second)}}}
	gaps := recoveryMeasurementGaps(dataset, removedAt, false)
	if len(gaps) != 1 || !strings.Contains(gaps[0], "preregistered censor slot") {
		t.Fatalf("recoveryMeasurementGaps() = %v", gaps)
	}
}

func TestFaultInjectionMustCompleteBeforeBoundary(t *testing.T) {
	snapshot := observer.DatasetSnapshot{Beacon: []observer.BeaconObservation{
		{Target: "a", CurrentSlot: 31},
		{Target: "b", CurrentSlot: 32},
	}}
	if !latestSlotExceeds(snapshot, []string{"a", "b"}, 31) {
		t.Fatal("latestSlotExceeds() = false")
	}
}

func TestPrimaryMeasurementCompletenessUsesCurrentSlotsDuringEmptyHeadSlots(t *testing.T) {
	start := time.Unix(100, 0).UTC()
	observations := []observer.BeaconObservation{
		{Target: "a", Timestamp: start, CurrentSlot: 10, Head: beacon.Head{Slot: 9, Root: "head-9"}},
		{Target: "b", Timestamp: start, CurrentSlot: 10, Head: beacon.Head{Slot: 9, Root: "head-9"}},
		{Target: "a", Timestamp: start.Add(time.Second), CurrentSlot: 11, Head: beacon.Head{Slot: 9, Root: "head-9"}},
		{Target: "b", Timestamp: start.Add(time.Second), CurrentSlot: 11, Head: beacon.Head{Slot: 9, Root: "head-9"}},
		{Target: "a", Timestamp: start.Add(2 * time.Second), CurrentSlot: 12, Head: beacon.Head{Slot: 12, Root: "head-12"}},
		{Target: "b", Timestamp: start.Add(2 * time.Second), CurrentSlot: 12, Head: beacon.Head{Slot: 12, Root: "head-12"}},
	}
	starts := observationsAtSlotMap(observations, []string{"a", "b"}, 10)
	ends := observationsAtSlotMap(observations, []string{"a", "b"}, 12)
	gaps := primaryMeasurementGaps(observer.DatasetSnapshot{Beacon: observations}, 10, 12, starts, ends, []string{"a", "b"})
	if len(gaps) != 0 {
		t.Fatalf("primaryMeasurementGaps() = %v", gaps)
	}
}

func TestFinalizedEpochRegressionIsNotConvertedToProgress(t *testing.T) {
	progress, err := finalizedEpochProgress(12, 11)
	if err == nil || progress != 0 || !strings.Contains(err.Error(), "regressed") {
		t.Fatalf("finalizedEpochProgress() = %d, %v", progress, err)
	}
}

func fixturePlacements(value scenario.Scenario) []topology.Placement {
	result := make([]topology.Placement, 0, len(value.Spec.Topology.Participants))
	for index, participant := range value.Spec.Topology.Participants {
		result = append(result, topology.Placement{
			ParticipantID:  participant.ID,
			Target:         participant.BeaconTarget,
			Pod:            "cl-pod-" + participant.ID,
			Node:           "node-" + string(rune('a'+index)),
			NodePool:       "pool-" + string(rune('a'+index)),
			MachineType:    "candidate-instance",
			ResourcePolicy: "el=1-2cpu,2-4Gi;cl=1-2cpu,2-4Gi;vc=.25-.5cpu,.5-1Gi",
		})
	}
	return result
}

func readScenario(t *testing.T) scenario.Scenario {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "scenarios", "cl-p2p-partition.yaml"))
	if err != nil {
		t.Fatalf("read scenario: %v", err)
	}
	value, err := scenario.Parse(data)
	if err != nil {
		t.Fatalf("parse scenario: %v", err)
	}
	return value
}
