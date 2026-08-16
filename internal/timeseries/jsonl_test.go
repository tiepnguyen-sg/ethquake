package timeseries

import (
	"bytes"
	"testing"
	"time"

	"github.com/tiepnguyen-sg/ethquake/internal/beacon"
	"github.com/tiepnguyen-sg/ethquake/internal/execution"
	"github.com/tiepnguyen-sg/ethquake/internal/observer"
)

func TestJSONLRecorderWritesVersionedEvents(t *testing.T) {
	var output bytes.Buffer
	recorder, err := NewJSONLRecorder(&output)
	if err != nil {
		t.Fatalf("NewJSONLRecorder() error = %v", err)
	}
	timestamp := time.Date(2026, time.August, 17, 12, 30, 0, 123, time.UTC)

	if err := recorder.RecordSpec(observer.SpecObservation{
		Timestamp: timestamp,
		Target:    "lighthouse",
		Spec: beacon.Spec{
			SecondsPerSlot: 12,
			SlotsPerEpoch:  32,
			ForkEpochs:     map[string]string{"DENEB_FORK_EPOCH": "0"},
		},
	}); err != nil {
		t.Fatalf("RecordSpec() error = %v", err)
	}
	if err := recorder.RecordBeacon(observer.BeaconObservation{
		Timestamp:        timestamp,
		Target:           "lighthouse",
		Head:             beacon.Head{Slot: 168, Root: fixtureRoot('1'), ParentRoot: fixtureRoot('2'), Canonical: true},
		Finality:         beacon.Finality{Epoch: 3, Root: fixtureRoot('3')},
		FinalityLagSlots: 72,
		PollDuration:     1500 * time.Microsecond,
	}); err != nil {
		t.Fatalf("RecordBeacon() error = %v", err)
	}
	if err := recorder.RecordPollFailure(observer.PollFailure{
		Timestamp:    timestamp,
		Target:       "lighthouse",
		Protocol:     "beacon",
		Operation:    "beacon_poll",
		Error:        "upstream unavailable",
		PollDuration: 2 * time.Millisecond,
	}); err != nil {
		t.Fatalf("RecordPollFailure() error = %v", err)
	}

	want := "" +
		`{"schema_version":"ethquake.observer/v1alpha1","type":"runtime_spec","timestamp":"2026-08-17T12:30:00.000000123Z","target":"lighthouse","spec":{"seconds_per_slot":12,"slots_per_epoch":32,"fork_epochs":{"DENEB_FORK_EPOCH":"0"}}}` + "\n" +
		`{"schema_version":"ethquake.observer/v1alpha1","type":"beacon_observation","timestamp":"2026-08-17T12:30:00.000000123Z","target":"lighthouse","head":{"slot":168,"root":"` + fixtureRoot('1') + `","parent_root":"` + fixtureRoot('2') + `","canonical":true,"execution_optimistic":false},"finality":{"epoch":3,"root":"` + fixtureRoot('3') + `","execution_optimistic":false},"finality_lag_slots":72,"poll_duration_ms":1.5}` + "\n" +
		`{"schema_version":"ethquake.observer/v1alpha1","type":"measurement_gap","timestamp":"2026-08-17T12:30:00.000000123Z","target":"lighthouse","protocol":"beacon","operation":"beacon_poll","error":"upstream unavailable","poll_duration_ms":2}` + "\n"
	if output.String() != want {
		t.Fatalf("output mismatch\n--- got ---\n%s--- want ---\n%s", output.String(), want)
	}
}

func TestJSONLRecorderPreservesUnknownReorgDepth(t *testing.T) {
	var output bytes.Buffer
	recorder, err := NewJSONLRecorder(&output)
	if err != nil {
		t.Fatalf("NewJSONLRecorder() error = %v", err)
	}
	timestamp := time.Date(2026, time.August, 17, 12, 30, 0, 0, time.UTC)
	previous := execution.Head{Number: 10, Hash: fixtureRoot('1'), ParentHash: fixtureRoot('0')}
	next := execution.Head{Number: 10, Hash: fixtureRoot('2'), ParentHash: fixtureRoot('9')}
	if err := recorder.RecordReorg(observer.ReorgObservation{
		Timestamp:    timestamp,
		Target:       "geth",
		PreviousHead: previous,
		NewHead:      next,
		Depth:        nil,
	}); err != nil {
		t.Fatalf("RecordReorg() error = %v", err)
	}
	if !bytes.Contains(output.Bytes(), []byte(`"reorg_depth":null`)) {
		t.Fatalf("unknown depth was not explicit: %s", output.String())
	}
}

func TestJSONLRecorderOmitsUnavailableExecutionPollDuration(t *testing.T) {
	var output bytes.Buffer
	recorder, err := NewJSONLRecorder(&output)
	if err != nil {
		t.Fatalf("NewJSONLRecorder() error = %v", err)
	}
	if err := recorder.RecordPollFailure(observer.PollFailure{
		Timestamp: time.Date(2026, time.August, 17, 12, 30, 0, 0, time.UTC),
		Target:    "geth",
		Protocol:  "execution",
		Operation: "execution_subscription",
		Error:     "connection closed",
	}); err != nil {
		t.Fatalf("RecordPollFailure() error = %v", err)
	}
	if bytes.Contains(output.Bytes(), []byte(`"poll_duration_ms"`)) {
		t.Fatalf("execution gap invented a poll duration: %s", output.String())
	}
}

func TestJSONLRecorderWritesHeadMismatchWithoutThreshold(t *testing.T) {
	var output bytes.Buffer
	recorder, err := NewJSONLRecorder(&output)
	if err != nil {
		t.Fatalf("NewJSONLRecorder() error = %v", err)
	}
	if err := recorder.RecordHeadComparison(observer.HeadComparison{
		Timestamp: time.Date(2026, time.August, 17, 12, 30, 0, 0, time.UTC),
		Slot:      168,
		Roots:     map[string]string{"lighthouse": fixtureRoot('1'), "teku": fixtureRoot('2')},
		Agreement: false,
	}); err != nil {
		t.Fatalf("RecordHeadComparison() error = %v", err)
	}
	if !bytes.Contains(output.Bytes(), []byte(`"type":"head_comparison"`)) ||
		!bytes.Contains(output.Bytes(), []byte(`"agreement":false`)) {
		t.Fatalf("head mismatch was not explicit: %s", output.String())
	}
}

func TestNewJSONLRecorderRejectsNilWriter(t *testing.T) {
	if _, err := NewJSONLRecorder(nil); err == nil {
		t.Fatal("NewJSONLRecorder() error = nil")
	}
}

func fixtureRoot(character byte) string {
	value := make([]byte, 66)
	value[0], value[1] = '0', 'x'
	for index := 2; index < len(value); index++ {
		value[index] = character
	}
	return string(value)
}
