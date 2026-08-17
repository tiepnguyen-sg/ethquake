package topology

import (
	"errors"
	"fmt"
	"math"

	"github.com/tiepnguyen-sg/ethquake/internal/scenario"
)

// ValidatePlacements verifies that every participant has an isolated,
// equivalently provisioned placement matching the committed scenario.
func ValidatePlacements(value scenario.Scenario, placements []Placement) error {
	if len(placements) != len(value.Spec.Topology.Participants) {
		return fmt.Errorf("received %d placements; expected %d", len(placements), len(value.Spec.Topology.Participants))
	}
	byParticipant := make(map[string]Placement, len(placements))
	nodes := make(map[string]struct{}, len(placements))
	referenceMachine := ""
	referencePolicy := ""
	for _, placement := range placements {
		if placement.ParticipantID == "" || placement.Target == "" || placement.Pod == "" || placement.Node == "" || placement.NodePool == "" || placement.MachineType == "" || placement.ResourcePolicy == "" {
			return errors.New("every placement field is required")
		}
		if _, duplicate := byParticipant[placement.ParticipantID]; duplicate {
			return fmt.Errorf("participant placement %q is duplicated", placement.ParticipantID)
		}
		if _, duplicate := nodes[placement.Node]; duplicate {
			return fmt.Errorf("node %q hosts more than one experiment participant", placement.Node)
		}
		if referenceMachine == "" {
			referenceMachine = placement.MachineType
			referencePolicy = placement.ResourcePolicy
		} else if placement.MachineType != referenceMachine || placement.ResourcePolicy != referencePolicy {
			return errors.New("participant machine types and resource policies are not equal")
		}
		byParticipant[placement.ParticipantID] = placement
		nodes[placement.Node] = struct{}{}
	}
	for _, participant := range value.Spec.Topology.Participants {
		placement, exists := byParticipant[participant.ID]
		if !exists || placement.Target != participant.BeaconTarget {
			return fmt.Errorf("participant %q lacks its exact target placement", participant.ID)
		}
	}
	return nil
}

// ValidateRealizedSplit verifies the independently stored split against the
// committed participant counts and recomputes every aggregate.
func ValidateRealizedSplit(value scenario.Scenario, split RealizedSplit) error {
	if split.SplitBy != value.Spec.Fault.SplitBy {
		return fmt.Errorf("realized split_by %q does not match scenario %q", split.SplitBy, value.Spec.Fault.SplitBy)
	}
	if split.TotalActiveBalance == 0 {
		return errors.New("realized split total active balance must be positive")
	}
	if split.ToleranceFraction != value.Spec.Thresholds.RealizedSplitTolerance {
		return errors.New("realized split tolerance does not match the scenario")
	}
	if len(split.Participants) != len(value.Spec.Topology.Participants) {
		return fmt.Errorf("realized split has %d participants; expected %d", len(split.Participants), len(value.Spec.Topology.Participants))
	}

	expectedParticipants := make(map[string]scenario.Participant, len(value.Spec.Topology.Participants))
	for _, participant := range value.Spec.Topology.Participants {
		expectedParticipants[participant.ID] = participant
	}
	balances := make(map[string]uint64, len(split.Participants))
	var total uint64
	for _, participant := range split.Participants {
		expected, exists := expectedParticipants[participant.ParticipantID]
		if !exists {
			return fmt.Errorf("realized split contains unknown participant %q", participant.ParticipantID)
		}
		if _, duplicate := balances[participant.ParticipantID]; duplicate {
			return fmt.Errorf("realized split participant %q is duplicated", participant.ParticipantID)
		}
		if participant.ValidatorCount != expected.ValidatorCount || participant.EffectiveBalance == 0 {
			return fmt.Errorf("realized split participant %q has invalid count or balance", participant.ParticipantID)
		}
		if math.MaxUint64-total < participant.EffectiveBalance {
			return errors.New("realized split participant balance sum overflows uint64")
		}
		balances[participant.ParticipantID] = participant.EffectiveBalance
		total += participant.EffectiveBalance
	}
	if total != split.TotalActiveBalance {
		return fmt.Errorf("realized split participant balance sum %d does not match total %d", total, split.TotalActiveBalance)
	}
	if len(split.Groups) != len(value.Spec.Fault.Groups) {
		return fmt.Errorf("realized split has %d groups; expected %d", len(split.Groups), len(value.Spec.Fault.Groups))
	}

	expectedGroups := make(map[string]scenario.FaultGroup, len(value.Spec.Fault.Groups))
	for _, group := range value.Spec.Fault.Groups {
		expectedGroups[group.Name] = group
	}
	seenGroups := make(map[string]struct{}, len(split.Groups))
	withinTolerance := true
	for _, group := range split.Groups {
		expected, exists := expectedGroups[group.Name]
		if !exists {
			return fmt.Errorf("realized split contains unknown group %q", group.Name)
		}
		if _, duplicate := seenGroups[group.Name]; duplicate {
			return fmt.Errorf("realized split group %q is duplicated", group.Name)
		}
		var groupBalance uint64
		for _, participantID := range expected.Participants {
			balance, exists := balances[participantID]
			if !exists {
				return fmt.Errorf("realized split group %q lacks participant %q", group.Name, participantID)
			}
			if math.MaxUint64-groupBalance < balance {
				return errors.New("realized split group balance sum overflows uint64")
			}
			groupBalance += balance
		}
		realizedShare := float64(groupBalance) / float64(total)
		if group.RequestedShare != expected.Share || group.EffectiveBalance != groupBalance || math.Abs(group.RealizedShare-realizedShare) > 1e-12 {
			return fmt.Errorf("realized split group %q aggregates do not match participant balances", group.Name)
		}
		if math.Abs(realizedShare-expected.Share) > split.ToleranceFraction {
			withinTolerance = false
		}
		seenGroups[group.Name] = struct{}{}
	}
	if split.WithinTolerance != withinTolerance {
		return errors.New("realized split within_tolerance does not match recomputed shares")
	}
	return nil
}
