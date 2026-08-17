// Package scenario owns the versioned experiment input contract.
package scenario

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

const (
	APIVersion       = "ethquake.dev/v1alpha1"
	Kind             = "Scenario"
	phase3ChainID    = uint64(3151908)
	maxScenarioBytes = 1 << 20
)

var (
	namePattern         = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	artifactNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
)

type Scenario struct {
	APIVersion string   `yaml:"apiVersion" json:"apiVersion"`
	Kind       string   `yaml:"kind" json:"kind"`
	Metadata   Metadata `yaml:"metadata" json:"metadata"`
	Spec       Spec     `yaml:"spec" json:"spec"`
}

type Metadata struct {
	Name string `yaml:"name" json:"name"`
}

type Spec struct {
	Target      Target      `yaml:"target" json:"target"`
	Topology    Topology    `yaml:"topology" json:"topology"`
	Fault       Fault       `yaml:"fault" json:"fault"`
	Thresholds  Thresholds  `yaml:"thresholds" json:"thresholds"`
	Methodology Methodology `yaml:"methodology" json:"methodology"`
	Prediction  Prediction  `yaml:"prediction" json:"prediction"`
	Limitations Limitations `yaml:"limitations" json:"limitations"`
}

type Target struct {
	ChainID         uint64 `yaml:"chain_id" json:"chain_id"`
	NamespacePrefix string `yaml:"namespace_prefix" json:"namespace_prefix"`
}

type Topology struct {
	Participants []Participant `yaml:"participants" json:"participants"`
}

type Participant struct {
	ID                 string `yaml:"id" json:"id"`
	ELClient           string `yaml:"el_client" json:"el_client"`
	CLClient           string `yaml:"cl_client" json:"cl_client"`
	BeaconTarget       string `yaml:"beacon_target" json:"beacon_target"`
	BeaconServiceID    string `yaml:"beacon_service_id" json:"beacon_service_id"`
	ExecutionServiceID string `yaml:"execution_service_id" json:"execution_service_id"`
	ValidatorServiceID string `yaml:"validator_service_id" json:"validator_service_id"`
	ValidatorRangeName string `yaml:"validator_range_name" json:"validator_range_name"`
	ValidatorCount     uint64 `yaml:"validator_count" json:"validator_count"`
}

type Fault struct {
	Type           string       `yaml:"type" json:"type"`
	SplitBy        string       `yaml:"split_by" json:"split_by"`
	Layers         []string     `yaml:"layers" json:"layers"`
	DurationEpochs uint64       `yaml:"duration_epochs" json:"duration_epochs"`
	Groups         []FaultGroup `yaml:"groups" json:"groups"`
}

type FaultGroup struct {
	Name         string   `yaml:"name" json:"name"`
	Share        float64  `yaml:"share" json:"share"`
	Participants []string `yaml:"participants" json:"participants"`
}

type Thresholds struct {
	HeadDivergenceToleranceSlots  uint64  `yaml:"head_divergence_tolerance_slots" json:"head_divergence_tolerance_slots"`
	RealizedSplitTolerance        float64 `yaml:"realized_split_tolerance_fraction" json:"realized_split_tolerance_fraction"`
	ClientRecoveryDifferenceSlots uint64  `yaml:"client_recovery_difference_slots" json:"client_recovery_difference_slots"`
}

type Methodology struct {
	ControlRepetitions         uint64   `yaml:"control_repetitions" json:"control_repetitions"`
	FaultRepetitions           uint64   `yaml:"fault_repetitions" json:"fault_repetitions"`
	Pairing                    string   `yaml:"pairing" json:"pairing"`
	OrderSeed                  string   `yaml:"order_seed" json:"order_seed"`
	RunOrder                   []string `yaml:"run_order" json:"run_order"`
	PostFaultObservationEpochs uint64   `yaml:"post_fault_observation_epochs" json:"post_fault_observation_epochs"`
}

type Prediction struct {
	PrimaryMetric string `yaml:"primary_metric" json:"primary_metric"`
	GateARule     string `yaml:"gate_a_rule" json:"gate_a_rule"`
	GateBRule     string `yaml:"gate_b_rule" json:"gate_b_rule"`
	Explanation   string `yaml:"explanation" json:"explanation"`
}

type Limitations struct {
	DoesNotProve string `yaml:"does_not_prove" json:"does_not_prove"`
}

func Parse(data []byte) (Scenario, error) {
	if len(data) > maxScenarioBytes {
		return Scenario{}, fmt.Errorf("scenario exceeds the %d-byte input limit", maxScenarioBytes)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var value Scenario
	if err := decoder.Decode(&value); err != nil {
		return Scenario{}, fmt.Errorf("decode scenario YAML: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Scenario{}, errors.New("scenario must contain exactly one YAML document")
		}
		return Scenario{}, fmt.Errorf("decode trailing scenario YAML: %w", err)
	}
	if err := value.Validate(); err != nil {
		return Scenario{}, err
	}
	return value, nil
}

func (s Scenario) Validate() error {
	if s.APIVersion != APIVersion {
		return fmt.Errorf("unsupported apiVersion %q; expected %q", s.APIVersion, APIVersion)
	}
	if s.Kind != Kind {
		return fmt.Errorf("unsupported kind %q; expected %q", s.Kind, Kind)
	}
	if !namePattern.MatchString(s.Metadata.Name) {
		return fmt.Errorf("metadata.name %q is invalid", s.Metadata.Name)
	}
	if err := s.Spec.Target.validate(); err != nil {
		return fmt.Errorf("validate target: %w", err)
	}
	participants, err := s.Spec.Topology.validate()
	if err != nil {
		return fmt.Errorf("validate topology: %w", err)
	}
	if err := s.Spec.Fault.validate(participants); err != nil {
		return fmt.Errorf("validate fault: %w", err)
	}
	if err := s.Spec.Thresholds.validate(); err != nil {
		return fmt.Errorf("validate thresholds: %w", err)
	}
	if err := s.Spec.Methodology.validate(); err != nil {
		return fmt.Errorf("validate methodology: %w", err)
	}
	if err := s.Spec.Prediction.validate(); err != nil {
		return fmt.Errorf("validate prediction: %w", err)
	}
	if strings.TrimSpace(s.Spec.Limitations.DoesNotProve) == "" {
		return errors.New("limitations.does_not_prove is required")
	}
	return validateMixedGroups(s.Spec.Fault.Groups, participants)
}

func (target Target) validate() error {
	if target.ChainID != phase3ChainID {
		return fmt.Errorf("chain_id %d is outside the exact Phase 3 devnet allowlist", target.ChainID)
	}
	if target.NamespacePrefix != "kt-ethquake-phase3-" {
		return fmt.Errorf("namespace_prefix %q is not the Phase 3 safety prefix", target.NamespacePrefix)
	}
	return nil
}

func (topology Topology) validate() (map[string]Participant, error) {
	if len(topology.Participants) != 4 {
		return nil, fmt.Errorf("Phase 3 requires exactly 4 participants; got %d", len(topology.Participants))
	}
	participants := make(map[string]Participant, len(topology.Participants))
	beaconTargets := make(map[string]struct{}, len(topology.Participants))
	serviceIDs := make(map[string]struct{}, len(topology.Participants)*3)
	rangeNames := make(map[string]struct{}, len(topology.Participants))
	for _, participant := range topology.Participants {
		if !namePattern.MatchString(participant.ID) {
			return nil, fmt.Errorf("participant id %q is invalid", participant.ID)
		}
		if _, exists := participants[participant.ID]; exists {
			return nil, fmt.Errorf("participant id %q is duplicated", participant.ID)
		}
		if !namePattern.MatchString(participant.ELClient) || !namePattern.MatchString(participant.CLClient) {
			return nil, fmt.Errorf("participant %q has invalid EL or CL client name", participant.ID)
		}
		if !namePattern.MatchString(participant.BeaconTarget) {
			return nil, fmt.Errorf("participant %q has invalid beacon_target", participant.ID)
		}
		if _, exists := beaconTargets[participant.BeaconTarget]; exists {
			return nil, fmt.Errorf("beacon_target %q is duplicated", participant.BeaconTarget)
		}
		services := []struct {
			field string
			id    string
		}{
			{field: "beacon_service_id", id: participant.BeaconServiceID},
			{field: "execution_service_id", id: participant.ExecutionServiceID},
			{field: "validator_service_id", id: participant.ValidatorServiceID},
		}
		for _, service := range services {
			if !namePattern.MatchString(service.id) {
				return nil, fmt.Errorf("participant %q has invalid %s", participant.ID, service.field)
			}
			if _, exists := serviceIDs[service.id]; exists {
				return nil, fmt.Errorf("service ID %q is duplicated", service.id)
			}
			serviceIDs[service.id] = struct{}{}
		}
		if !artifactNamePattern.MatchString(participant.ValidatorRangeName) {
			return nil, fmt.Errorf("participant %q has invalid validator_range_name", participant.ID)
		}
		if _, exists := rangeNames[participant.ValidatorRangeName]; exists {
			return nil, fmt.Errorf("validator_range_name %q is duplicated", participant.ValidatorRangeName)
		}
		if participant.ValidatorCount == 0 {
			return nil, fmt.Errorf("participant %q validator_count must be positive", participant.ID)
		}
		participants[participant.ID] = participant
		beaconTargets[participant.BeaconTarget] = struct{}{}
		rangeNames[participant.ValidatorRangeName] = struct{}{}
	}
	return participants, nil
}

func (fault Fault) validate(participants map[string]Participant) error {
	if fault.Type != "network_partition" {
		return fmt.Errorf("fault type %q is unsupported", fault.Type)
	}
	if fault.SplitBy != "validator_weight" {
		return fmt.Errorf("split_by must be validator_weight; got %q", fault.SplitBy)
	}
	if len(fault.Layers) != 1 || fault.Layers[0] != "cl_p2p" {
		return errors.New("Phase 3 layers must contain only cl_p2p")
	}
	if fault.DurationEpochs != 3 {
		return fmt.Errorf("Phase 3 duration_epochs must be 3; got %d", fault.DurationEpochs)
	}
	if len(fault.Groups) != 2 {
		return fmt.Errorf("network partition requires exactly 2 groups; got %d", len(fault.Groups))
	}
	seenGroups := make(map[string]struct{}, 2)
	seenParticipants := make(map[string]struct{}, len(participants))
	var totalShare float64
	var requestedWeight []uint64
	for _, group := range fault.Groups {
		if !namePattern.MatchString(group.Name) {
			return fmt.Errorf("group name %q is invalid", group.Name)
		}
		if _, exists := seenGroups[group.Name]; exists {
			return fmt.Errorf("group name %q is duplicated", group.Name)
		}
		if math.Abs(group.Share-0.5) > 1e-12 {
			return fmt.Errorf("group %q share must be 0.5", group.Name)
		}
		if len(group.Participants) == 0 {
			return fmt.Errorf("group %q has no participants", group.Name)
		}
		var weight uint64
		for _, participantID := range group.Participants {
			participant, exists := participants[participantID]
			if !exists {
				return fmt.Errorf("group %q references unknown participant %q", group.Name, participantID)
			}
			if _, exists := seenParticipants[participantID]; exists {
				return fmt.Errorf("participant %q appears in multiple groups", participantID)
			}
			seenParticipants[participantID] = struct{}{}
			weight += participant.ValidatorCount
		}
		seenGroups[group.Name] = struct{}{}
		totalShare += group.Share
		requestedWeight = append(requestedWeight, weight)
	}
	if math.Abs(totalShare-1) > 1e-12 {
		return fmt.Errorf("group shares sum to %.12f; expected 1", totalShare)
	}
	if len(seenParticipants) != len(participants) {
		return errors.New("fault groups must include every topology participant exactly once")
	}
	if requestedWeight[0] != requestedWeight[1] {
		return fmt.Errorf("configured validator counts are not 50/50: %d versus %d", requestedWeight[0], requestedWeight[1])
	}
	return nil
}

func (thresholds Thresholds) validate() error {
	if thresholds.HeadDivergenceToleranceSlots != 1 {
		return fmt.Errorf("head_divergence_tolerance_slots must match ADR-0004 value 1; got %d", thresholds.HeadDivergenceToleranceSlots)
	}
	if thresholds.RealizedSplitTolerance != 0 {
		return fmt.Errorf("realized_split_tolerance_fraction must be 0 for the exact Phase 3 split; got %g", thresholds.RealizedSplitTolerance)
	}
	if thresholds.ClientRecoveryDifferenceSlots != 1 {
		return fmt.Errorf("client_recovery_difference_slots must match ADR-0004 value 1; got %d", thresholds.ClientRecoveryDifferenceSlots)
	}
	return nil
}

func (method Methodology) validate() error {
	if method.ControlRepetitions != 3 || method.FaultRepetitions != 3 {
		return errors.New("Phase 3 requires exactly 3 control and 3 fault repetitions")
	}
	if method.Pairing != "by_repetition" {
		return fmt.Errorf("unsupported pairing %q", method.Pairing)
	}
	if strings.TrimSpace(method.OrderSeed) == "" {
		return errors.New("order_seed is required")
	}
	if method.PostFaultObservationEpochs != 4 {
		return fmt.Errorf("post_fault_observation_epochs must match ADR-0004 value 4; got %d", method.PostFaultObservationEpochs)
	}
	expected := []string{"control-1", "control-2", "control-3", "fault-1", "fault-2", "fault-3"}
	if len(method.RunOrder) != len(expected) {
		return fmt.Errorf("run_order requires %d entries; got %d", len(expected), len(method.RunOrder))
	}
	wanted := make(map[string]struct{}, len(expected))
	for _, runID := range expected {
		wanted[runID] = struct{}{}
	}
	for _, runID := range method.RunOrder {
		if _, exists := wanted[runID]; !exists {
			return fmt.Errorf("run_order contains unexpected or duplicated run %q", runID)
		}
		delete(wanted, runID)
	}
	derived := DeriveRunOrder(method.OrderSeed, expected)
	for index := range derived {
		if method.RunOrder[index] != derived[index] {
			return fmt.Errorf("run_order does not match SHA-256 order_seed derivation at position %d", index)
		}
	}
	return nil
}

func (prediction Prediction) validate() error {
	if prediction.PrimaryMetric != "finalized_epoch_progress" {
		return fmt.Errorf("unsupported primary_metric %q", prediction.PrimaryMetric)
	}
	if prediction.GateARule != "max_fault_target_strictly_less_than_min_paired_control_target_all_repetitions" {
		return fmt.Errorf("unsupported gate_a_rule %q", prediction.GateARule)
	}
	if prediction.GateBRule != "zero_fault_progress_and_recovery_all_repetitions" {
		return fmt.Errorf("unsupported gate_b_rule %q", prediction.GateBRule)
	}
	if strings.TrimSpace(prediction.Explanation) == "" {
		return errors.New("prediction.explanation is required")
	}
	return nil
}

func validateMixedGroups(groups []FaultGroup, participants map[string]Participant) error {
	allClients := make(map[string]struct{})
	for _, participant := range participants {
		allClients[participant.CLClient] = struct{}{}
	}
	if len(allClients) < 2 {
		return errors.New("Phase 3 requires at least two CL client families")
	}
	for _, group := range groups {
		clients := make(map[string]struct{})
		for _, participantID := range group.Participants {
			clients[participants[participantID].CLClient] = struct{}{}
		}
		for client := range allClients {
			if _, exists := clients[client]; !exists {
				return fmt.Errorf("group %q does not contain CL client %q", group.Name, client)
			}
		}
	}
	return nil
}

func DeriveRunOrder(seed string, runIDs []string) []string {
	type rankedRun struct {
		id   string
		hash [sha256.Size]byte
	}
	ranked := make([]rankedRun, 0, len(runIDs))
	for _, runID := range runIDs {
		ranked = append(ranked, rankedRun{
			id:   runID,
			hash: sha256.Sum256([]byte(seed + ":" + runID)),
		})
	}
	sort.Slice(ranked, func(left, right int) bool {
		return bytes.Compare(ranked[left].hash[:], ranked[right].hash[:]) < 0
	})
	result := make([]string, 0, len(ranked))
	for _, run := range ranked {
		result = append(result, run.id)
	}
	return result
}
