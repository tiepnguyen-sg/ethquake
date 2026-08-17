// Package topology discovers and verifies the network under test.
package topology

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/tiepnguyen-sg/ethquake/internal/beacon"
	"github.com/tiepnguyen-sg/ethquake/internal/scenario"
	"go.yaml.in/yaml/v3"
)

const maxValidatorRangesBytes = 1 << 20

type ValidatorRange struct {
	Start uint64 `json:"start"`
	End   uint64 `json:"end"`
	Owner string `json:"owner"`
}

type Placement struct {
	ParticipantID  string `json:"participant_id"`
	Target         string `json:"target"`
	Pod            string `json:"pod"`
	Node           string `json:"node"`
	NodePool       string `json:"node_pool"`
	MachineType    string `json:"machine_type"`
	ResourcePolicy string `json:"resource_policy"`
}

type ParticipantWeight struct {
	ParticipantID    string `json:"participant_id"`
	ValidatorCount   uint64 `json:"validator_count"`
	EffectiveBalance uint64 `json:"effective_balance"`
}

type GroupWeight struct {
	Name             string  `json:"name"`
	RequestedShare   float64 `json:"requested_share"`
	RealizedShare    float64 `json:"realized_share"`
	EffectiveBalance uint64  `json:"effective_balance"`
}

type RealizedSplit struct {
	SplitBy            string              `json:"split_by"`
	TotalActiveBalance uint64              `json:"total_active_balance"`
	Participants       []ParticipantWeight `json:"participants"`
	Groups             []GroupWeight       `json:"groups"`
	WithinTolerance    bool                `json:"within_tolerance"`
	ToleranceFraction  float64             `json:"tolerance_fraction"`
}

func ParseValidatorRanges(data []byte) ([]ValidatorRange, error) {
	if len(data) > maxValidatorRangesBytes {
		return nil, fmt.Errorf("validator ranges exceed the %d-byte input limit", maxValidatorRangesBytes)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var raw map[string]string
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode validator ranges YAML: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("validator ranges must contain exactly one YAML document")
		}
		return nil, fmt.Errorf("decode trailing validator ranges YAML: %w", err)
	}
	if len(raw) == 0 {
		return nil, errors.New("validator ranges are empty")
	}
	ranges := make([]ValidatorRange, 0, len(raw))
	for rawRange, owner := range raw {
		startText, endText, found := strings.Cut(rawRange, "-")
		if !found || startText == "" || endText == "" || strings.Contains(endText, "-") {
			return nil, fmt.Errorf("validator range %q must use START-END", rawRange)
		}
		start, err := strconv.ParseUint(startText, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse validator range %q start: %w", rawRange, err)
		}
		end, err := strconv.ParseUint(endText, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse validator range %q end: %w", rawRange, err)
		}
		if end < start {
			return nil, fmt.Errorf("validator range %q ends before it starts", rawRange)
		}
		if owner == "" {
			return nil, fmt.Errorf("validator range %q has an empty owner", rawRange)
		}
		ranges = append(ranges, ValidatorRange{Start: start, End: end, Owner: owner})
	}
	sort.Slice(ranges, func(left, right int) bool {
		if ranges[left].Start == ranges[right].Start {
			return ranges[left].End < ranges[right].End
		}
		return ranges[left].Start < ranges[right].Start
	})
	for index := 1; index < len(ranges); index++ {
		if ranges[index].Start <= ranges[index-1].End {
			return nil, fmt.Errorf("validator ranges overlap at index %d", ranges[index].Start)
		}
	}
	return ranges, nil
}

func CalculateRealizedSplit(value scenario.Scenario, ranges []ValidatorRange, validators []beacon.Validator) (RealizedSplit, error) {
	if len(ranges) == 0 {
		return RealizedSplit{}, errors.New("validator ranges are required")
	}
	if len(validators) == 0 {
		return RealizedSplit{}, errors.New("active validators are required")
	}
	participantsByRange := make(map[string]scenario.Participant, len(value.Spec.Topology.Participants))
	participantsByID := make(map[string]scenario.Participant, len(value.Spec.Topology.Participants))
	for _, participant := range value.Spec.Topology.Participants {
		participantsByRange[participant.ValidatorRangeName] = participant
		participantsByID[participant.ID] = participant
	}
	type ownedRange struct {
		ValidatorRange
		participantID string
	}
	ownedRanges := make([]ownedRange, 0, len(ranges))
	for _, validatorRange := range ranges {
		participant, exists := participantsByRange[validatorRange.Owner]
		if !exists {
			return RealizedSplit{}, fmt.Errorf("validator range owner %q is not in scenario topology", validatorRange.Owner)
		}
		ownedRanges = append(ownedRanges, ownedRange{ValidatorRange: validatorRange, participantID: participant.ID})
	}
	weights := make(map[string]uint64, len(participantsByID))
	counts := make(map[string]uint64, len(participantsByID))
	var total uint64
	for _, validator := range validators {
		position := sort.Search(len(ownedRanges), func(index int) bool {
			return ownedRanges[index].End >= validator.Index
		})
		participantID := ""
		if position < len(ownedRanges) && ownedRanges[position].Start <= validator.Index {
			participantID = ownedRanges[position].participantID
		}
		exists := participantID != ""
		if !exists {
			return RealizedSplit{}, fmt.Errorf("active validator index %d has no ownership range", validator.Index)
		}
		if math.MaxUint64-weights[participantID] < validator.EffectiveBalance || math.MaxUint64-total < validator.EffectiveBalance {
			return RealizedSplit{}, errors.New("effective balance sum overflows uint64")
		}
		weights[participantID] += validator.EffectiveBalance
		counts[participantID]++
		total += validator.EffectiveBalance
	}
	if total == 0 {
		return RealizedSplit{}, errors.New("total active effective balance is zero")
	}
	result := RealizedSplit{
		SplitBy:            value.Spec.Fault.SplitBy,
		TotalActiveBalance: total,
		ToleranceFraction:  value.Spec.Thresholds.RealizedSplitTolerance,
		WithinTolerance:    true,
	}
	for _, participant := range value.Spec.Topology.Participants {
		if counts[participant.ID] != participant.ValidatorCount {
			return RealizedSplit{}, fmt.Errorf(
				"participant %q has %d active validators; expected %d",
				participant.ID,
				counts[participant.ID],
				participant.ValidatorCount,
			)
		}
		result.Participants = append(result.Participants, ParticipantWeight{
			ParticipantID:    participant.ID,
			ValidatorCount:   counts[participant.ID],
			EffectiveBalance: weights[participant.ID],
		})
	}
	for _, group := range value.Spec.Fault.Groups {
		var groupBalance uint64
		for _, participantID := range group.Participants {
			groupBalance += weights[participantID]
		}
		realizedShare := float64(groupBalance) / float64(total)
		if math.Abs(realizedShare-group.Share) > value.Spec.Thresholds.RealizedSplitTolerance {
			result.WithinTolerance = false
		}
		result.Groups = append(result.Groups, GroupWeight{
			Name:             group.Name,
			RequestedShare:   group.Share,
			RealizedShare:    realizedShare,
			EffectiveBalance: groupBalance,
		})
	}
	return result, nil
}
