// Package experiment coordinates and evaluates preregistered experiment runs.
package experiment

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tiepnguyen-sg/ethquake/internal/scenario"
	"github.com/tiepnguyen-sg/ethquake/internal/topology"
)

const (
	RunSchemaVersion    = "ethquake.run/v1alpha1"
	ReportSchemaVersion = "ethquake.report/v1alpha1"
)

type Condition string

const (
	ConditionControl Condition = "control"
	ConditionFault   Condition = "fault"
)

type DependencyMetadata struct {
	EthereumPackageRevision string            `json:"ethereum_package_revision"`
	ImportedPackages        map[string]string `json:"imported_packages"`
	RuntimeImages           map[string]string `json:"runtime_images"`
}

type Confounders struct {
	ResourcesEqual          bool `json:"resources_equal"`
	NodePlacementControlled bool `json:"node_placement_controlled"`
	DependencyClosurePinned bool `json:"dependency_closure_pinned"`
}

type Window struct {
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	StartSlot uint64    `json:"start_slot"`
	EndSlot   uint64    `json:"end_slot"`
}

type TargetResult struct {
	Target                 string  `json:"target"`
	CLClient               string  `json:"cl_client"`
	Group                  string  `json:"group"`
	StartFinalizedEpoch    uint64  `json:"start_finalized_epoch"`
	EndFinalizedEpoch      uint64  `json:"end_finalized_epoch"`
	FinalizedEpochProgress uint64  `json:"finalized_epoch_progress"`
	RecoverySlots          *uint64 `json:"recovery_slots"`
	DivergenceExceeded     bool    `json:"head_divergence_exceeded"`
}

type RunSummary struct {
	SchemaVersion               string                 `json:"schema_version"`
	RunID                       string                 `json:"run_id"`
	Condition                   Condition              `json:"condition"`
	Repetition                  uint64                 `json:"repetition"`
	Window                      Window                 `json:"window"`
	PrimaryMeasurementComplete  bool                   `json:"primary_measurement_complete"`
	RecoveryMeasurementComplete bool                   `json:"recovery_measurement_complete"`
	MeasurementGaps             []string               `json:"measurement_gaps"`
	Targets                     []TargetResult         `json:"targets"`
	RealizedSplit               topology.RealizedSplit `json:"realized_split"`
	Dependencies                DependencyMetadata     `json:"dependencies"`
	Confounders                 Confounders            `json:"confounders"`
}

type Statistic struct {
	Values []uint64 `json:"values"`
	Median float64  `json:"median"`
	Min    uint64   `json:"min"`
	Max    uint64   `json:"max"`
}

type PairEvidence struct {
	Repetition             uint64 `json:"repetition"`
	ControlMinimumProgress uint64 `json:"control_minimum_progress"`
	FaultMaximumProgress   uint64 `json:"fault_maximum_progress"`
	ConservativeDifference uint64 `json:"conservative_difference"`
}

type Gate struct {
	Outcome        string `json:"outcome"`
	Classification string `json:"classification"`
	Reason         string `json:"reason"`
}

type Analysis struct {
	SchemaVersion          string               `json:"schema_version"`
	ScenarioName           string               `json:"scenario_name"`
	GeneratedAt            time.Time            `json:"generated_at"`
	Runs                   []RunSummary         `json:"runs"`
	Pairs                  []PairEvidence       `json:"pairs"`
	ControlMinimumProgress Statistic            `json:"control_minimum_progress"`
	FaultMaximumProgress   Statistic            `json:"fault_maximum_progress"`
	RecoveryByClient       map[string]Statistic `json:"recovery_by_client"`
	GateA                  Gate                 `json:"gate_a"`
	GateB                  Gate                 `json:"gate_b"`
	GateC                  Gate                 `json:"gate_c"`
	Conclusion             string               `json:"conclusion"`
}

func Analyze(value scenario.Scenario, runs []RunSummary, generatedAt time.Time) (Analysis, error) {
	if err := value.Validate(); err != nil {
		return Analysis{}, fmt.Errorf("validate scenario: %w", err)
	}
	orderedRuns, controls, faults, err := validateRuns(value, runs)
	if err != nil {
		return Analysis{}, err
	}
	analysis := Analysis{
		SchemaVersion:    ReportSchemaVersion,
		ScenarioName:     value.Metadata.Name,
		GeneratedAt:      generatedAt.UTC(),
		Runs:             orderedRuns,
		RecoveryByClient: make(map[string]Statistic),
		GateC: Gate{
			Outcome:        "not_evaluated",
			Classification: "not_evaluated",
			Reason:         "Gate C is evaluated only after Gate A:YES and Gate B:YES.",
		},
	}
	controlProgress := make([]uint64, 0, 3)
	faultProgress := make([]uint64, 0, 3)
	gateAValid := true
	participantByTarget := make(map[string]scenario.Participant, len(value.Spec.Topology.Participants))
	groupByParticipant := make(map[string]string, len(value.Spec.Topology.Participants))
	for _, participant := range value.Spec.Topology.Participants {
		participantByTarget[participant.BeaconTarget] = participant
	}
	for _, group := range value.Spec.Fault.Groups {
		for _, participantID := range group.Participants {
			groupByParticipant[participantID] = group.Name
		}
	}
	for repetition := uint64(1); repetition <= 3; repetition++ {
		control := controls[repetition]
		faultRun := faults[repetition]
		if !primaryWindowValid(control, participantByTarget, groupByParticipant) ||
			!primaryWindowValid(faultRun, participantByTarget, groupByParticipant) {
			gateAValid = false
			continue
		}
		controlValue := minimumProgress(control.Targets)
		faultValue := maximumProgress(faultRun.Targets)
		controlProgress = append(controlProgress, controlValue)
		faultProgress = append(faultProgress, faultValue)
		difference := uint64(0)
		if controlValue > faultValue {
			difference = controlValue - faultValue
		} else {
			gateAValid = false
		}
		analysis.Pairs = append(analysis.Pairs, PairEvidence{
			Repetition:             repetition,
			ControlMinimumProgress: controlValue,
			FaultMaximumProgress:   faultValue,
			ConservativeDifference: difference,
		})
	}
	analysis.ControlMinimumProgress = summarize(controlProgress)
	analysis.FaultMaximumProgress = summarize(faultProgress)
	if !gateAValid || len(analysis.Pairs) != 3 {
		analysis.GateA = Gate{
			Outcome:        "no",
			Classification: "invalid_experiment",
			Reason:         "The maximum fault-target finality progress was not strictly lower than the minimum paired control-target progress in every complete repetition.",
		}
		analysis.GateB = Gate{Outcome: "not_evaluated", Classification: "not_evaluated", Reason: "Gate B cannot be interpreted after Gate A:NO."}
		analysis.Conclusion = "invalid_experiment"
		return analysis, nil
	}
	analysis.GateA = Gate{
		Outcome:        "yes",
		Classification: "valid_experiment",
		Reason:         "The maximum fault-target finality progress was strictly lower than the minimum paired control-target progress across three complete repetitions.",
	}
	for _, run := range faults {
		if !run.RecoveryMeasurementComplete {
			analysis.GateB = Gate{
				Outcome:        "not_evaluated",
				Classification: "invalid_measurement",
				Reason:         "Recovery measurement was incomplete in at least one fault repetition, so the preregistered prediction cannot be evaluated.",
			}
			analysis.Conclusion = "invalid_experiment"
			return analysis, nil
		}
	}

	gateBMatched := true
	for _, run := range faults {
		for _, target := range run.Targets {
			if target.FinalizedEpochProgress != 0 || target.RecoverySlots == nil {
				gateBMatched = false
			}
		}
	}
	if !gateBMatched {
		analysis.GateB = Gate{
			Outcome:        "no",
			Classification: "network_level_finding",
			Reason:         "The valid network-level effect did not match the preregistered zero-progress-and-recovery prediction in every repetition.",
		}
		analysis.Conclusion = "network_level_finding"
		return analysis, nil
	}
	analysis.GateB = Gate{
		Outcome:        "yes",
		Classification: "prediction_matched",
		Reason:         "Every fault run had zero finalized-epoch progress and every target recovered inside the observation window.",
	}

	recoveryValues := make(map[string][]uint64)
	for _, run := range faults {
		maximumByClient := make(map[string]uint64)
		for _, target := range run.Targets {
			if current, exists := maximumByClient[target.CLClient]; !exists || *target.RecoverySlots > current {
				maximumByClient[target.CLClient] = *target.RecoverySlots
			}
		}
		for client, maximum := range maximumByClient {
			recoveryValues[client] = append(recoveryValues[client], maximum)
		}
	}
	for client, values := range recoveryValues {
		analysis.RecoveryByClient[client] = summarize(values)
	}
	differentiated, reason := evaluateGateC(value, controls, faults)
	if differentiated {
		analysis.GateC = Gate{Outcome: "yes", Classification: "client_specific_differentiation", Reason: reason}
		analysis.Conclusion = "client_specific_differentiation"
	} else {
		analysis.GateC = Gate{Outcome: "no", Classification: "no_client_differentiation", Reason: reason}
		analysis.Conclusion = "valid_predicted_uniform_effect"
	}
	return analysis, nil
}

func validateRuns(value scenario.Scenario, runs []RunSummary) ([]RunSummary, map[uint64]RunSummary, map[uint64]RunSummary, error) {
	if len(runs) != len(value.Spec.Methodology.RunOrder) {
		return nil, nil, nil, fmt.Errorf("received %d runs; expected %d", len(runs), len(value.Spec.Methodology.RunOrder))
	}
	byID := make(map[string]RunSummary, len(runs))
	var referenceDependencies *DependencyMetadata
	for _, run := range runs {
		if run.SchemaVersion != RunSchemaVersion {
			return nil, nil, nil, fmt.Errorf("run %q has unsupported schema_version %q", run.RunID, run.SchemaVersion)
		}
		if _, exists := byID[run.RunID]; exists {
			return nil, nil, nil, fmt.Errorf("run ID %q is duplicated", run.RunID)
		}
		condition, repetition, err := parseRunID(run.RunID)
		if err != nil {
			return nil, nil, nil, err
		}
		if run.Condition != condition || run.Repetition != repetition {
			return nil, nil, nil, fmt.Errorf("run %q condition or repetition does not match its ID", run.RunID)
		}
		if err := validateDependencyMetadata(run.Dependencies); err != nil {
			return nil, nil, nil, fmt.Errorf("run %q dependency closure: %w", run.RunID, err)
		}
		if referenceDependencies == nil {
			copy := run.Dependencies
			referenceDependencies = &copy
		} else if !dependencyMetadataEqual(*referenceDependencies, run.Dependencies) {
			return nil, nil, nil, fmt.Errorf("run %q dependency closure differs from other repetitions", run.RunID)
		}
		byID[run.RunID] = run
	}
	ordered := make([]RunSummary, 0, len(runs))
	controls := make(map[uint64]RunSummary, 3)
	faults := make(map[uint64]RunSummary, 3)
	for _, runID := range value.Spec.Methodology.RunOrder {
		run, exists := byID[runID]
		if !exists {
			return nil, nil, nil, fmt.Errorf("required run %q is missing", runID)
		}
		ordered = append(ordered, run)
		if run.Condition == ConditionControl {
			controls[run.Repetition] = run
		} else {
			faults[run.Repetition] = run
		}
	}
	return ordered, controls, faults, nil
}

func primaryWindowValid(
	run RunSummary,
	participantByTarget map[string]scenario.Participant,
	groupByParticipant map[string]string,
) bool {
	if !run.PrimaryMeasurementComplete || len(run.Targets) != 4 || !run.RealizedSplit.WithinTolerance {
		return false
	}
	if !run.Confounders.ResourcesEqual || !run.Confounders.NodePlacementControlled || !run.Confounders.DependencyClosurePinned {
		return false
	}
	seen := make(map[string]struct{}, len(run.Targets))
	for _, target := range run.Targets {
		participant, exists := participantByTarget[target.Target]
		if !exists || target.CLClient != participant.CLClient || target.Group != groupByParticipant[participant.ID] {
			return false
		}
		if _, duplicate := seen[target.Target]; duplicate {
			return false
		}
		seen[target.Target] = struct{}{}
		if target.EndFinalizedEpoch < target.StartFinalizedEpoch ||
			target.FinalizedEpochProgress != target.EndFinalizedEpoch-target.StartFinalizedEpoch {
			return false
		}
	}
	return true
}

func maximumProgress(targets []TargetResult) uint64 {
	var maximum uint64
	for _, target := range targets {
		if target.FinalizedEpochProgress > maximum {
			maximum = target.FinalizedEpochProgress
		}
	}
	return maximum
}

func minimumProgress(targets []TargetResult) uint64 {
	minimum := targets[0].FinalizedEpochProgress
	for _, target := range targets[1:] {
		if target.FinalizedEpochProgress < minimum {
			minimum = target.FinalizedEpochProgress
		}
	}
	return minimum
}

func evaluateGateC(value scenario.Scenario, controls, faults map[uint64]RunSummary) (bool, string) {
	clients := make([]string, 0)
	seenClients := make(map[string]struct{})
	for _, participant := range value.Spec.Topology.Participants {
		if _, exists := seenClients[participant.CLClient]; !exists {
			seenClients[participant.CLClient] = struct{}{}
			clients = append(clients, participant.CLClient)
		}
	}
	sort.Strings(clients)
	if len(clients) != 2 {
		return false, "Gate C requires exactly two preregistered CL client families."
	}
	direction := 0
	threshold := int64(value.Spec.Thresholds.ClientRecoveryDifferenceSlots)
	for repetition := uint64(1); repetition <= 3; repetition++ {
		if !uniformProgress(controls[repetition].Targets) {
			return false, "Control runs contain a client-family finality-progress skew, so attribution is confounded."
		}
		byGroupAndClient, ok := recoveryByGroupAndClient(faults[repetition].Targets)
		if !ok {
			return false, "Recovery is right-censored, missing, or duplicated for at least one client family and partition side."
		}
		for _, group := range value.Spec.Fault.Groups {
			left := byGroupAndClient[group.Name][clients[0]]
			right := byGroupAndClient[group.Name][clients[1]]
			currentDirection, meetsThreshold := differenceDirection(left, right, uint64(threshold))
			if !meetsThreshold {
				return false, "The preregistered one-slot client recovery difference was not present on both partition sides in every fault repetition."
			}
			if direction == 0 {
				direction = currentDirection
			} else if direction != currentDirection {
				return false, "The direction of the client recovery difference was not reproducible across sides and repetitions."
			}
		}
	}
	slower := clients[0]
	if direction < 0 {
		slower = clients[1]
	}
	return true, fmt.Sprintf("%s recovered at least %d slot later on both partition sides across all three fault repetitions, without matching control skew.", slower, threshold)
}

func uniformProgress(targets []TargetResult) bool {
	if len(targets) == 0 {
		return false
	}
	reference := targets[0].FinalizedEpochProgress
	for _, target := range targets[1:] {
		if target.FinalizedEpochProgress != reference {
			return false
		}
	}
	return true
}

func recoveryByGroupAndClient(targets []TargetResult) (map[string]map[string]uint64, bool) {
	result := make(map[string]map[string]uint64)
	for _, target := range targets {
		if target.RecoverySlots == nil {
			return nil, false
		}
		if result[target.Group] == nil {
			result[target.Group] = make(map[string]uint64)
		}
		if _, duplicate := result[target.Group][target.CLClient]; duplicate {
			return nil, false
		}
		result[target.Group][target.CLClient] = *target.RecoverySlots
	}
	return result, true
}

func differenceDirection(left, right, threshold uint64) (int, bool) {
	if left >= right {
		difference := left - right
		return 1, difference >= threshold
	}
	difference := right - left
	return -1, difference >= threshold
}

func summarize(input []uint64) Statistic {
	if len(input) == 0 {
		return Statistic{}
	}
	values := append([]uint64(nil), input...)
	sort.Slice(values, func(left, right int) bool { return values[left] < values[right] })
	median := float64(values[len(values)/2])
	if len(values)%2 == 0 {
		median = float64(values[len(values)/2-1]+values[len(values)/2]) / 2
	}
	return Statistic{Values: values, Median: median, Min: values[0], Max: values[len(values)-1]}
}

func parseRunID(runID string) (Condition, uint64, error) {
	conditionText, repetitionText, found := strings.Cut(runID, "-")
	if !found || (conditionText != string(ConditionControl) && conditionText != string(ConditionFault)) {
		return "", 0, fmt.Errorf("run ID %q is invalid", runID)
	}
	if len(repetitionText) != 1 || repetitionText[0] < '1' || repetitionText[0] > '3' {
		return "", 0, fmt.Errorf("run ID %q has invalid repetition", runID)
	}
	return Condition(conditionText), uint64(repetitionText[0] - '0'), nil
}

func validateDependencyMetadata(metadata DependencyMetadata) error {
	if !isLowerHex(metadata.EthereumPackageRevision, 40) {
		return errors.New("ethereum-package revision must be a full commit SHA")
	}
	if len(metadata.ImportedPackages) == 0 || len(metadata.RuntimeImages) == 0 {
		return errors.New("imported packages and runtime images are required")
	}
	for name, revision := range metadata.ImportedPackages {
		if name == "" || !isLowerHex(revision, 40) {
			return fmt.Errorf("imported package %q is not pinned to a full commit SHA", name)
		}
	}
	for name, image := range metadata.RuntimeImages {
		parts := strings.Split(image, "@sha256:")
		if name == "" || len(parts) != 2 || parts[0] == "" || !isLowerHex(parts[1], 64) {
			return fmt.Errorf("runtime image %q is not digest-pinned", name)
		}
	}
	return nil
}

func isLowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func dependencyMetadataEqual(left, right DependencyMetadata) bool {
	if left.EthereumPackageRevision != right.EthereumPackageRevision || len(left.ImportedPackages) != len(right.ImportedPackages) || len(left.RuntimeImages) != len(right.RuntimeImages) {
		return false
	}
	for key, value := range left.ImportedPackages {
		if right.ImportedPackages[key] != value {
			return false
		}
	}
	for key, value := range left.RuntimeImages {
		if right.RuntimeImages[key] != value {
			return false
		}
	}
	return true
}
