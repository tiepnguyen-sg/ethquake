package observer

import (
	"testing"
	"time"

	"github.com/tiepnguyen-sg/ethquake/internal/beacon"
)

func TestDatasetRecorderReturnsIndependentSnapshot(t *testing.T) {
	recorder := NewDatasetRecorder()
	if err := recorder.RecordSpec(SpecObservation{
		Target:  "a",
		Spec:    beacon.Spec{ForkEpochs: map[string]string{"DENEB_FORK_EPOCH": "0"}},
		Genesis: beacon.Genesis{Time: 1000, ValidatorsRoot: "root", ForkVersion: "fork"},
	}); err != nil {
		t.Fatalf("RecordSpec() error = %v", err)
	}
	if err := recorder.RecordBeacon(BeaconObservation{Target: "a", Timestamp: time.Now()}); err != nil {
		t.Fatalf("RecordBeacon() error = %v", err)
	}
	if err := recorder.RecordHeadComparison(HeadComparison{Roots: map[string]string{"a": "root"}}); err != nil {
		t.Fatalf("RecordHeadComparison() error = %v", err)
	}
	first := recorder.Snapshot()
	first.Specs["a"].ForkEpochs["DENEB_FORK_EPOCH"] = "changed"
	first.HeadComparisons[0].Roots["a"] = "changed"
	second := recorder.Snapshot()
	if second.Specs["a"].ForkEpochs["DENEB_FORK_EPOCH"] != "0" || second.Genesis["a"].Time != 1000 || second.HeadComparisons[0].Roots["a"] != "root" {
		t.Fatalf("Snapshot() leaked mutable state: %+v", second)
	}
}
