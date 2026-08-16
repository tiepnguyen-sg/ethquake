package assertion

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/tiepnguyen-sg/ethquake/internal/observer"
)

func TestEvaluateRecordedFixtureWithoutCluster(t *testing.T) {
	contents, err := os.ReadFile("testdata/measurement-window.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var dataset Dataset
	if err := json.Unmarshal(contents, &dataset); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	result, err := Evaluate(Plan{
		FinalityMustAdvanceTargets: []string{"lighthouse", "teku"},
		RequireHeadComparison:      true,
		RequireNoMeasurementGaps:   true,
	}, dataset)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !result.Passed {
		t.Fatalf("Evaluate() result = %+v", result)
	}
	if len(result.Checks) != 4 {
		t.Fatalf("checks = %d, want 4", len(result.Checks))
	}
}

func TestEvaluateDoesNotTreatMissingMeasurementsAsZero(t *testing.T) {
	result, err := Evaluate(Plan{
		FinalityMustAdvanceTargets: []string{"teku"},
	}, Dataset{})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if result.Passed || len(result.Checks) != 1 || !strings.Contains(result.Checks[0].Detail, "need at least 2") {
		t.Fatalf("Evaluate() result = %+v", result)
	}
}

func TestEvaluateReportsMeasurementGap(t *testing.T) {
	result, err := Evaluate(Plan{RequireNoMeasurementGaps: true}, Dataset{
		MeasurementGaps: []observer.PollFailure{{Protocol: "beacon", Target: "teku"}},
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if result.Passed || result.Checks[0].Detail != "measurement gaps: 1" {
		t.Fatalf("Evaluate() result = %+v", result)
	}
}

func TestEvaluateRejectsInvalidPlan(t *testing.T) {
	if _, err := Evaluate(Plan{}, Dataset{}); err == nil {
		t.Fatal("empty plan error = nil")
	}
	if _, err := Evaluate(Plan{FinalityMustAdvanceTargets: []string{"same", "same"}}, Dataset{}); err == nil {
		t.Fatal("duplicate target error = nil")
	}
}
