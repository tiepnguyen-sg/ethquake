package experiment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tiepnguyen-sg/ethquake/internal/scenario"
	"github.com/tiepnguyen-sg/ethquake/internal/topology"
)

func TestAnalyzeGateCClientDifferentiation(t *testing.T) {
	value := readScenario(t)
	runs := validRuns(value, []uint64{5, 5, 5}, []uint64{8, 8, 8})
	analysis, err := Analyze(value, runs, time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if analysis.GateA.Outcome != "yes" || analysis.GateB.Outcome != "yes" || analysis.GateC.Outcome != "yes" {
		t.Fatalf("gates = A:%+v B:%+v C:%+v", analysis.GateA, analysis.GateB, analysis.GateC)
	}
	if analysis.Conclusion != "client_specific_differentiation" || !strings.Contains(analysis.GateC.Reason, "teku recovered") {
		t.Fatalf("analysis = %+v", analysis)
	}
	if got := analysis.RecoveryByClient["lighthouse"].Values; len(got) != 3 || got[0] != 5 || got[2] != 5 {
		t.Fatalf("lighthouse recovery repetitions = %v", got)
	}
}

func TestAnalyzeGateANoIsInvalidExperiment(t *testing.T) {
	value := readScenario(t)
	runs := validRuns(value, []uint64{5, 5, 5}, []uint64{8, 8, 8})
	for index := range runs {
		if runs[index].RunID == "fault-2" {
			for targetIndex := range runs[index].Targets {
				runs[index].Targets[targetIndex].EndFinalizedEpoch = 13
				runs[index].Targets[targetIndex].FinalizedEpochProgress = 3
			}
		}
	}
	analysis, err := Analyze(value, runs, time.Now())
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if analysis.GateA.Outcome != "no" || analysis.GateA.Classification != "invalid_experiment" || analysis.GateB.Outcome != "not_evaluated" {
		t.Fatalf("analysis = %+v", analysis)
	}
}

func TestAnalyzeGateARejectsPairWhenOneControlTargetDoesNotProgress(t *testing.T) {
	value := readScenario(t)
	runs := validRuns(value, []uint64{5, 5, 5}, []uint64{8, 8, 8})
	for index := range runs {
		if runs[index].RunID == "control-2" {
			runs[index].Targets[0].EndFinalizedEpoch = 10
			runs[index].Targets[0].FinalizedEpochProgress = 0
		}
	}
	analysis, err := Analyze(value, runs, time.Now())
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if analysis.GateA.Outcome != "no" || analysis.GateA.Classification != "invalid_experiment" {
		t.Fatalf("Gate A = %+v", analysis.GateA)
	}
	if analysis.Pairs[1].ControlMinimumProgress != 0 || analysis.Pairs[1].FaultMaximumProgress != 0 {
		t.Fatalf("pair 2 = %+v", analysis.Pairs[1])
	}
}

func TestAnalyzeGateBNoIsNetworkFinding(t *testing.T) {
	value := readScenario(t)
	runs := validRuns(value, []uint64{5, 5, 5}, []uint64{8, 8, 8})
	for index := range runs {
		if runs[index].RunID == "fault-1" {
			runs[index].Targets[0].EndFinalizedEpoch = 11
			runs[index].Targets[0].FinalizedEpochProgress = 1
		}
	}
	analysis, err := Analyze(value, runs, time.Now())
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if analysis.GateA.Outcome != "yes" || analysis.GateB.Outcome != "no" || analysis.GateB.Classification != "network_level_finding" || analysis.GateC.Outcome != "not_evaluated" {
		t.Fatalf("analysis = %+v", analysis)
	}
}

func TestAnalyzeRejectsMutableDependencyMetadata(t *testing.T) {
	value := readScenario(t)
	runs := validRuns(value, []uint64{5, 5, 5}, []uint64{8, 8, 8})
	runs[0].Dependencies.RuntimeImages["lighthouse"] = "sigp/lighthouse:latest"
	_, err := Analyze(value, runs, time.Now())
	if err == nil || !strings.Contains(err.Error(), "not digest-pinned") {
		t.Fatalf("Analyze() error = %v", err)
	}
}

func TestAnalyzeTreatsMeasurementGapAsInvalid(t *testing.T) {
	value := readScenario(t)
	runs := validRuns(value, []uint64{5, 5, 5}, []uint64{8, 8, 8})
	runs[0].MeasurementGaps = []string{"lighthouse-a beacon_poll"}
	runs[0].PrimaryMeasurementComplete = false
	analysis, err := Analyze(value, runs, time.Now())
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if analysis.GateA.Classification != "invalid_experiment" {
		t.Fatalf("analysis = %+v", analysis)
	}
}

func TestAnalyzeDoesNotClassifyRecoveryGapAsNetworkFinding(t *testing.T) {
	value := readScenario(t)
	runs := validRuns(value, []uint64{5, 5, 5}, []uint64{8, 8, 8})
	for index := range runs {
		if runs[index].RunID == "fault-1" {
			runs[index].RecoveryMeasurementComplete = false
			runs[index].MeasurementGaps = []string{"lighthouse-a beacon_poll"}
		}
	}
	analysis, err := Analyze(value, runs, time.Now())
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if analysis.GateA.Outcome != "yes" || analysis.GateB.Outcome != "not_evaluated" || analysis.GateB.Classification != "invalid_measurement" {
		t.Fatalf("analysis = %+v", analysis)
	}
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

func validRuns(value scenario.Scenario, lighthouseRecovery, tekuRecovery []uint64) []RunSummary {
	runs := make([]RunSummary, 0, 6)
	for _, runID := range value.Spec.Methodology.RunOrder {
		condition, repetition, _ := parseRunID(runID)
		progress := uint64(3)
		if condition == ConditionFault {
			progress = 0
		}
		targets := make([]TargetResult, 0, 4)
		for _, participant := range value.Spec.Topology.Participants {
			group := ""
			for _, faultGroup := range value.Spec.Fault.Groups {
				for _, participantID := range faultGroup.Participants {
					if participantID == participant.ID {
						group = faultGroup.Name
					}
				}
			}
			var recovery *uint64
			if condition == ConditionFault {
				value := lighthouseRecovery[repetition-1]
				if participant.CLClient == "teku" {
					value = tekuRecovery[repetition-1]
				}
				recovery = &value
			}
			targets = append(targets, TargetResult{
				Target:                 participant.BeaconTarget,
				CLClient:               participant.CLClient,
				Group:                  group,
				StartFinalizedEpoch:    10,
				EndFinalizedEpoch:      10 + progress,
				FinalizedEpochProgress: progress,
				RecoverySlots:          recovery,
			})
		}
		runs = append(runs, RunSummary{
			SchemaVersion:               RunSchemaVersion,
			RunID:                       runID,
			Condition:                   condition,
			Repetition:                  repetition,
			Window:                      Window{StartSlot: 100, EndSlot: 196},
			PrimaryMeasurementComplete:  true,
			RecoveryMeasurementComplete: true,
			Targets:                     targets,
			RealizedSplit:               validRealizedSplit(value),
			Dependencies:                validDependencies(),
			Confounders: Confounders{
				ResourcesEqual:          true,
				NodePlacementControlled: true,
				DependencyClosurePinned: true,
			},
		})
	}
	return runs
}

func validRealizedSplit(value scenario.Scenario) topology.RealizedSplit {
	result := topology.RealizedSplit{
		SplitBy:            value.Spec.Fault.SplitBy,
		TotalActiveBalance: 128,
		WithinTolerance:    true,
		ToleranceFraction:  value.Spec.Thresholds.RealizedSplitTolerance,
	}
	for _, participant := range value.Spec.Topology.Participants {
		result.Participants = append(result.Participants, topology.ParticipantWeight{
			ParticipantID:    participant.ID,
			ValidatorCount:   participant.ValidatorCount,
			EffectiveBalance: 32,
		})
	}
	for _, group := range value.Spec.Fault.Groups {
		result.Groups = append(result.Groups, topology.GroupWeight{
			Name:             group.Name,
			RequestedShare:   group.Share,
			RealizedShare:    0.5,
			EffectiveBalance: 64,
		})
	}
	return result
}

func validDependencies() DependencyMetadata {
	return DependencyMetadata{
		EthereumPackageRevision: strings.Repeat("a", 40),
		ImportedPackages: map[string]string{
			"github.com/kurtosis-tech/prometheus-package": strings.Repeat("b", 40),
		},
		RuntimeImages: map[string]string{
			"lighthouse": "sigp/lighthouse:v1@sha256:" + strings.Repeat("c", 64),
		},
	}
}
