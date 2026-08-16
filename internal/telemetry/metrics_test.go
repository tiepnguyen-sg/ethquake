package telemetry

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tiepnguyen-sg/ethquake/internal/beacon"
	"github.com/tiepnguyen-sg/ethquake/internal/execution"
	"github.com/tiepnguyen-sg/ethquake/internal/observer"
)

func TestMetricsExposeMeasurementsAndRemoveThemOnFailure(t *testing.T) {
	metrics, err := NewMetrics()
	if err != nil {
		t.Fatalf("NewMetrics() error = %v", err)
	}
	timestamp := time.Unix(100, 0).UTC()
	if err := metrics.RecordSpec(observer.SpecObservation{
		Timestamp: timestamp,
		Target:    "lighthouse",
		Spec:      beacon.Spec{SecondsPerSlot: 12, SlotsPerEpoch: 32},
	}); err != nil {
		t.Fatalf("RecordSpec() error = %v", err)
	}
	if err := metrics.RecordBeacon(observer.BeaconObservation{
		Timestamp:        timestamp,
		Target:           "lighthouse",
		Head:             beacon.Head{Slot: 168},
		Finality:         beacon.Finality{Epoch: 3},
		FinalityLagSlots: 72,
		PollDuration:     10 * time.Millisecond,
	}); err != nil {
		t.Fatalf("RecordBeacon() error = %v", err)
	}

	body := scrape(t, metrics.Handler())
	assertMetric(t, body, `ethquake_observer_beacon_head_slot{target="lighthouse"} 168`)
	assertMetric(t, body, `ethquake_observer_finality_lag_slots{target="lighthouse"} 72`)
	assertMetric(t, body, `ethquake_observer_runtime_slots_per_epoch{target="lighthouse"} 32`)
	assertMetric(t, body, `ethquake_observer_beacon_execution_optimistic{source="head",target="lighthouse"} 0`)
	assertMetric(t, body, `ethquake_observer_beacon_execution_optimistic{source="finality",target="lighthouse"} 0`)
	assertMetric(t, body, `ethquake_observer_target_up{protocol="beacon",target="lighthouse"} 1`)
	assertMetric(t, body, `ethquake_observer_beacon_polls_total{result="success",target="lighthouse"} 1`)

	if err := metrics.RecordPollFailure(observer.PollFailure{
		Timestamp:    timestamp.Add(time.Second),
		Target:       "lighthouse",
		Protocol:     "beacon",
		Operation:    "beacon_poll",
		Error:        "unavailable",
		PollDuration: 20 * time.Millisecond,
	}); err != nil {
		t.Fatalf("RecordPollFailure() error = %v", err)
	}

	body = scrape(t, metrics.Handler())
	if strings.Contains(body, `ethquake_observer_beacon_head_slot{target="lighthouse"}`) {
		t.Fatalf("failed target retained head measurement:\n%s", body)
	}
	if strings.Contains(body, `ethquake_observer_finality_lag_slots{target="lighthouse"}`) {
		t.Fatalf("failed target retained lag measurement:\n%s", body)
	}
	if strings.Contains(body, `ethquake_observer_beacon_execution_optimistic{`) {
		t.Fatalf("failed target retained optimistic-state measurement:\n%s", body)
	}
	assertMetric(t, body, `ethquake_observer_target_up{protocol="beacon",target="lighthouse"} 0`)
	assertMetric(t, body, `ethquake_observer_beacon_polls_total{result="error",target="lighthouse"} 1`)
	assertMetric(t, body, `ethquake_observer_last_success_timestamp_seconds{protocol="beacon",target="lighthouse"} 100`)
	assertMetric(t, body, `ethquake_observer_errors_total{operation="beacon_poll",protocol="beacon",target="lighthouse"} 1`)
}

func TestMetricsExposeExactHeadComparisonWithoutTolerance(t *testing.T) {
	metrics, err := NewMetrics()
	if err != nil {
		t.Fatalf("NewMetrics() error = %v", err)
	}
	before := scrape(t, metrics.Handler())
	if strings.Contains(before, "ethquake_observer_beacon_head_agreement") ||
		strings.Contains(before, "ethquake_observer_beacon_head_comparison_slot") {
		t.Fatalf("head comparison metrics exist before a measurement:\n%s", before)
	}
	if err := metrics.RecordHeadComparison(observer.HeadComparison{
		Timestamp: time.Unix(100, 0),
		Slot:      168,
		Roots:     map[string]string{"lighthouse": "0x01", "teku": "0x02"},
		Agreement: false,
	}); err != nil {
		t.Fatalf("RecordHeadComparison() error = %v", err)
	}
	body := scrape(t, metrics.Handler())
	assertMetric(t, body, `ethquake_observer_beacon_head_agreement 0`)
	assertMetric(t, body, `ethquake_observer_beacon_head_comparison_slot 168`)
	assertMetric(t, body, `ethquake_observer_beacon_head_comparisons_total{result="mismatch"} 1`)
	if err := metrics.RecordPollFailure(observer.PollFailure{
		Timestamp: time.Unix(101, 0),
		Target:    "teku",
		Protocol:  "beacon",
		Operation: "beacon_poll",
		Error:     "unavailable",
	}); err != nil {
		t.Fatalf("RecordPollFailure() error = %v", err)
	}
	body = scrape(t, metrics.Handler())
	if strings.Contains(body, "ethquake_observer_beacon_head_agreement") ||
		strings.Contains(body, "ethquake_observer_beacon_head_comparison_slot") {
		t.Fatalf("head comparison metrics remained during a gap:\n%s", body)
	}
}

func TestMetricsExposeExecutionReorgAndUnknownDepth(t *testing.T) {
	metrics, err := NewMetrics()
	if err != nil {
		t.Fatalf("NewMetrics() error = %v", err)
	}
	timestamp := time.Unix(200, 0).UTC()
	if err := metrics.RecordExecution(observer.ExecutionObservation{
		Timestamp:  timestamp,
		Target:     "geth",
		Head:       execution.Head{Number: 12},
		Continuity: true,
	}); err != nil {
		t.Fatalf("RecordExecution() error = %v", err)
	}
	depth := uint64(2)
	if err := metrics.RecordReorg(observer.ReorgObservation{Target: "geth", Depth: &depth}); err != nil {
		t.Fatalf("RecordReorg() error = %v", err)
	}
	body := scrape(t, metrics.Handler())
	assertMetric(t, body, `ethquake_observer_execution_head_number{target="geth"} 12`)
	assertMetric(t, body, `ethquake_observer_execution_continuity{target="geth"} 1`)
	assertMetric(t, body, `ethquake_observer_execution_reorgs_total{target="geth"} 1`)
	assertMetric(t, body, `ethquake_observer_execution_reorg_depth{target="geth"} 2`)
	assertMetric(t, body, `ethquake_observer_target_up{protocol="execution",target="geth"} 1`)

	if err := metrics.RecordReorg(observer.ReorgObservation{Target: "geth", Depth: nil}); err != nil {
		t.Fatalf("RecordReorg() unknown depth error = %v", err)
	}
	body = scrape(t, metrics.Handler())
	if strings.Contains(body, `ethquake_observer_execution_reorg_depth{target="geth"}`) {
		t.Fatalf("unknown depth retained stale metric:\n%s", body)
	}
	assertMetric(t, body, `ethquake_observer_execution_reorgs_total{target="geth"} 2`)
}

func scrape(t *testing.T, handler http.Handler) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("metrics status = %d", response.Code)
	}
	return response.Body.String()
}

func assertMetric(t *testing.T, body, line string) {
	t.Helper()
	if !strings.Contains(body, line) {
		t.Fatalf("metric %q not found in:\n%s", line, body)
	}
}
