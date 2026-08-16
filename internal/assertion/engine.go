// Package assertion evaluates explicit conditions over recorded measurements.
package assertion

import (
	"errors"
	"fmt"
	"sort"

	"github.com/tiepnguyen-sg/ethquake/internal/observer"
)

type Dataset struct {
	BeaconObservations []observer.BeaconObservation `json:"beacon_observations"`
	HeadComparisons    []observer.HeadComparison    `json:"head_comparisons"`
	MeasurementGaps    []observer.PollFailure       `json:"measurement_gaps"`
	Reorgs             []observer.ReorgObservation  `json:"reorgs"`
}

type Plan struct {
	FinalityMustAdvanceTargets []string `json:"finality_must_advance_targets"`
	RequireHeadComparison      bool     `json:"require_head_comparison"`
	RequireNoMeasurementGaps   bool     `json:"require_no_measurement_gaps"`
}

type Check struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}

type Result struct {
	Passed bool    `json:"passed"`
	Checks []Check `json:"checks"`
}

func Evaluate(plan Plan, dataset Dataset) (Result, error) {
	if len(plan.FinalityMustAdvanceTargets) == 0 && !plan.RequireHeadComparison && !plan.RequireNoMeasurementGaps {
		return Result{}, errors.New("assertion plan must contain at least one check")
	}
	targets := append([]string(nil), plan.FinalityMustAdvanceTargets...)
	sort.Strings(targets)
	for index, target := range targets {
		if target == "" {
			return Result{}, errors.New("finality assertion target must not be empty")
		}
		if index > 0 && target == targets[index-1] {
			return Result{}, fmt.Errorf("finality assertion target %q is duplicated", target)
		}
	}

	result := Result{Passed: true}
	for _, target := range targets {
		check := finalityAdvanced(target, dataset.BeaconObservations)
		result.Checks = append(result.Checks, check)
		result.Passed = result.Passed && check.Passed
	}
	if plan.RequireHeadComparison {
		check := Check{
			Name:   "head_comparison_available",
			Passed: len(dataset.HeadComparisons) > 0,
			Detail: fmt.Sprintf("complete comparisons: %d", len(dataset.HeadComparisons)),
		}
		result.Checks = append(result.Checks, check)
		result.Passed = result.Passed && check.Passed
	}
	if plan.RequireNoMeasurementGaps {
		check := Check{
			Name:   "measurement_stream_complete",
			Passed: len(dataset.MeasurementGaps) == 0,
			Detail: fmt.Sprintf("measurement gaps: %d", len(dataset.MeasurementGaps)),
		}
		result.Checks = append(result.Checks, check)
		result.Passed = result.Passed && check.Passed
	}
	return result, nil
}

func finalityAdvanced(target string, observations []observer.BeaconObservation) Check {
	matching := make([]observer.BeaconObservation, 0)
	for _, observation := range observations {
		if observation.Target == target {
			matching = append(matching, observation)
		}
	}
	sort.Slice(matching, func(left, right int) bool {
		return matching[left].Timestamp.Before(matching[right].Timestamp)
	})
	if len(matching) < 2 {
		return Check{
			Name:   "finality_advances:" + target,
			Passed: false,
			Detail: fmt.Sprintf("complete observations: %d; need at least 2", len(matching)),
		}
	}
	initialEpoch := matching[0].Finality.Epoch
	maximumEpoch := initialEpoch
	for _, observation := range matching[1:] {
		if observation.Finality.Epoch > maximumEpoch {
			maximumEpoch = observation.Finality.Epoch
		}
	}
	return Check{
		Name:   "finality_advances:" + target,
		Passed: maximumEpoch > initialEpoch,
		Detail: fmt.Sprintf("initial epoch: %d; maximum later epoch: %d", initialEpoch, maximumEpoch),
	}
}
