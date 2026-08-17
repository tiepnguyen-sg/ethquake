package topology

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tiepnguyen-sg/ethquake/internal/beacon"
	"github.com/tiepnguyen-sg/ethquake/internal/scenario"
)

func TestParseValidatorRangesRejectsOversizedInput(t *testing.T) {
	_, err := ParseValidatorRanges(bytes.Repeat([]byte{'x'}, maxValidatorRangesBytes+1))
	if err == nil || !strings.Contains(err.Error(), "input limit") {
		t.Fatalf("ParseValidatorRanges() error = %v", err)
	}
}

func TestCalculateRealizedSplit(t *testing.T) {
	value := testScenario()
	ranges, err := ParseValidatorRanges([]byte("0-0: 1-lighthouse-geth\n1-1: 2-teku-reth\n2-2: 3-lighthouse-geth\n3-3: 4-teku-reth\n"))
	if err != nil {
		t.Fatalf("ParseValidatorRanges() error = %v", err)
	}
	validators := []beacon.Validator{
		{Index: 0, EffectiveBalance: 32},
		{Index: 1, EffectiveBalance: 32},
		{Index: 2, EffectiveBalance: 32},
		{Index: 3, EffectiveBalance: 32},
	}
	result, err := CalculateRealizedSplit(value, ranges, validators)
	if err != nil {
		t.Fatalf("CalculateRealizedSplit() error = %v", err)
	}
	if !result.WithinTolerance || len(result.Groups) != 2 || result.Groups[0].RealizedShare != 0.5 || result.Groups[1].RealizedShare != 0.5 {
		t.Fatalf("result = %+v", result)
	}
	if err := ValidateRealizedSplit(value, result); err != nil {
		t.Fatalf("ValidateRealizedSplit() error = %v", err)
	}
}

func TestCalculateRealizedSplitRejectsImbalance(t *testing.T) {
	value := testScenario()
	ranges, err := ParseValidatorRanges([]byte("0-0: 1-lighthouse-geth\n1-1: 2-teku-reth\n2-2: 3-lighthouse-geth\n3-3: 4-teku-reth\n"))
	if err != nil {
		t.Fatalf("ParseValidatorRanges() error = %v", err)
	}
	validators := []beacon.Validator{
		{Index: 0, EffectiveBalance: 31},
		{Index: 1, EffectiveBalance: 32},
		{Index: 2, EffectiveBalance: 32},
		{Index: 3, EffectiveBalance: 32},
	}
	result, err := CalculateRealizedSplit(value, ranges, validators)
	if err != nil {
		t.Fatalf("CalculateRealizedSplit() error = %v", err)
	}
	if result.WithinTolerance {
		t.Fatalf("result = %+v", result)
	}
}

func TestCalculateRealizedSplitRejectsUnmappedValidator(t *testing.T) {
	value := testScenario()
	ranges, err := ParseValidatorRanges([]byte("0-0: 1-lighthouse-geth\n1-1: 2-teku-reth\n2-2: 3-lighthouse-geth\n"))
	if err != nil {
		t.Fatalf("ParseValidatorRanges() error = %v", err)
	}
	_, err = CalculateRealizedSplit(value, ranges, []beacon.Validator{{Index: 3, EffectiveBalance: 32}})
	if err == nil || !strings.Contains(err.Error(), "no ownership range") {
		t.Fatalf("CalculateRealizedSplit() error = %v", err)
	}
}

func TestParseValidatorRangesRejectsOverlap(t *testing.T) {
	_, err := ParseValidatorRanges([]byte("0-2: one\n2-3: two\n"))
	if err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("ParseValidatorRanges() error = %v", err)
	}
}

func TestValidateRealizedSplitRejectsInconsistentAggregate(t *testing.T) {
	value := testScenario()
	ranges, err := ParseValidatorRanges([]byte("0-0: 1-lighthouse-geth\n1-1: 2-teku-reth\n2-2: 3-lighthouse-geth\n3-3: 4-teku-reth\n"))
	if err != nil {
		t.Fatalf("ParseValidatorRanges() error = %v", err)
	}
	result, err := CalculateRealizedSplit(value, ranges, []beacon.Validator{
		{Index: 0, EffectiveBalance: 32},
		{Index: 1, EffectiveBalance: 32},
		{Index: 2, EffectiveBalance: 32},
		{Index: 3, EffectiveBalance: 32},
	})
	if err != nil {
		t.Fatalf("CalculateRealizedSplit() error = %v", err)
	}
	result.Groups[0].EffectiveBalance++
	if err := ValidateRealizedSplit(value, result); err == nil || !strings.Contains(err.Error(), "aggregates") {
		t.Fatalf("ValidateRealizedSplit() error = %v", err)
	}
}

func testScenario() scenario.Scenario {
	participants := []scenario.Participant{
		{ID: "lighthouse-geth-a", CLClient: "lighthouse", ValidatorRangeName: "1-lighthouse-geth", ValidatorCount: 1},
		{ID: "teku-reth-a", CLClient: "teku", ValidatorRangeName: "2-teku-reth", ValidatorCount: 1},
		{ID: "lighthouse-geth-b", CLClient: "lighthouse", ValidatorRangeName: "3-lighthouse-geth", ValidatorCount: 1},
		{ID: "teku-reth-b", CLClient: "teku", ValidatorRangeName: "4-teku-reth", ValidatorCount: 1},
	}
	return scenario.Scenario{Spec: scenario.Spec{
		Topology: scenario.Topology{Participants: participants},
		Fault: scenario.Fault{
			SplitBy: "validator_weight",
			Groups: []scenario.FaultGroup{
				{Name: "a", Share: 0.5, Participants: []string{"lighthouse-geth-a", "teku-reth-a"}},
				{Name: "b", Share: 0.5, Participants: []string{"lighthouse-geth-b", "teku-reth-b"}},
			},
		},
	}}
}
