package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tiepnguyen-sg/ethquake/internal/experiment"
)

func TestMarkdownGolden(t *testing.T) {
	analysis := fixtureAnalysis()
	actual := Markdown(analysis)
	goldenPath := filepath.Join("testdata", "report.md.golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(goldenPath, actual, 0o600); err != nil {
			t.Fatalf("update golden: %v", err)
		}
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if string(actual) != string(want) {
		t.Fatalf("Markdown() mismatch\n--- actual ---\n%s\n--- want ---\n%s", actual, want)
	}
}

func TestSVGIsStandaloneAndEscapesTitle(t *testing.T) {
	analysis := fixtureAnalysis()
	analysis.ScenarioName = `<script>alert("x")</script>`
	value := string(SVG(analysis))
	if !strings.HasPrefix(value, "<svg") || !strings.HasSuffix(value, "</svg>") {
		t.Fatalf("SVG() = %q", value)
	}
	if strings.Contains(value, "<script>") || !strings.Contains(value, "&lt;script&gt;") {
		t.Fatalf("SVG title was not escaped: %s", value)
	}
}

func fixtureAnalysis() experiment.Analysis {
	return experiment.Analysis{
		SchemaVersion: "ethquake.report/v1alpha1",
		ScenarioName:  "cl-p2p-partition",
		GeneratedAt:   time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC),
		Pairs: []experiment.PairEvidence{
			{Repetition: 1, ControlMinimumProgress: 3, FaultMaximumProgress: 0, ConservativeDifference: 3},
			{Repetition: 2, ControlMinimumProgress: 3, FaultMaximumProgress: 0, ConservativeDifference: 3},
			{Repetition: 3, ControlMinimumProgress: 3, FaultMaximumProgress: 0, ConservativeDifference: 3},
		},
		ControlMinimumProgress: experiment.Statistic{Values: []uint64{3, 3, 3}, Median: 3, Min: 3, Max: 3},
		FaultMaximumProgress:   experiment.Statistic{Values: []uint64{0, 0, 0}, Median: 0, Min: 0, Max: 0},
		RecoveryByClient: map[string]experiment.Statistic{
			"lighthouse": {Values: []uint64{5, 5, 5}, Median: 5, Min: 5, Max: 5},
			"teku":       {Values: []uint64{8, 8, 8}, Median: 8, Min: 8, Max: 8},
		},
		GateA:      experiment.Gate{Outcome: "yes", Reason: "Every fault run was lower than control."},
		GateB:      experiment.Gate{Outcome: "yes", Reason: "The prediction matched."},
		GateC:      experiment.Gate{Outcome: "yes", Reason: "Teku recovered later in every repetition."},
		Conclusion: "client_specific_differentiation",
	}
}
