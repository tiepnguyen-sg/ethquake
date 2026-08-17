package scenario

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParsePhase3Scenario(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "scenarios", "cl-p2p-partition.yaml"))
	if err != nil {
		t.Fatalf("read scenario: %v", err)
	}
	value, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if value.Metadata.Name != "cl-p2p-partition" {
		t.Fatalf("name = %q", value.Metadata.Name)
	}
	wantOrder := []string{"fault-3", "control-2", "control-1", "fault-2", "control-3", "fault-1"}
	if !reflect.DeepEqual(value.Spec.Methodology.RunOrder, wantOrder) {
		t.Fatalf("run order = %v, want %v", value.Spec.Methodology.RunOrder, wantOrder)
	}
}

func TestParseRejectsUnknownField(t *testing.T) {
	data := validScenarioYAML()
	data = strings.Replace(data, "  target:\n", "  unknown: true\n  target:\n", 1)
	_, err := Parse([]byte(data))
	if err == nil || !strings.Contains(err.Error(), "field unknown not found") {
		t.Fatalf("Parse() error = %v", err)
	}
}

func TestParseRejectsPublicNetwork(t *testing.T) {
	data := strings.Replace(validScenarioYAML(), "chain_id: 3151908", "chain_id: 1", 1)
	_, err := Parse([]byte(data))
	if err == nil || !strings.Contains(err.Error(), "outside the exact Phase 3 devnet allowlist") {
		t.Fatalf("Parse() error = %v", err)
	}
}

func TestParseRejectsUnapprovedPrivateChain(t *testing.T) {
	data := strings.Replace(validScenarioYAML(), "chain_id: 3151908", "chain_id: 42", 1)
	_, err := Parse([]byte(data))
	if err == nil || !strings.Contains(err.Error(), "outside the exact Phase 3 devnet allowlist") {
		t.Fatalf("Parse() error = %v", err)
	}
}

func TestParseRejectsUnbalancedValidatorCounts(t *testing.T) {
	data := strings.Replace(validScenarioYAML(), "validator_count: 32", "validator_count: 31", 1)
	_, err := Parse([]byte(data))
	if err == nil || !strings.Contains(err.Error(), "configured validator counts are not 50/50") {
		t.Fatalf("Parse() error = %v", err)
	}
}

func TestParseRejectsInvalidClientName(t *testing.T) {
	data := strings.Replace(validScenarioYAML(), "el_client: geth", "el_client: 'geth|unsafe'", 1)
	_, err := Parse([]byte(data))
	if err == nil || !strings.Contains(err.Error(), "invalid EL or CL client name") {
		t.Fatalf("Parse() error = %v", err)
	}
}

func TestParseRejectsHomogeneousSide(t *testing.T) {
	data := strings.Replace(
		validScenarioYAML(),
		"          - lighthouse-geth-a\n          - teku-reth-a\n      - name: b\n        share: 0.5\n        participants:\n          - lighthouse-geth-b\n          - teku-reth-b",
		"          - lighthouse-geth-a\n          - lighthouse-geth-b\n      - name: b\n        share: 0.5\n        participants:\n          - teku-reth-a\n          - teku-reth-b",
		1,
	)
	_, err := Parse([]byte(data))
	if err == nil || !strings.Contains(err.Error(), "does not contain CL client") {
		t.Fatalf("Parse() error = %v", err)
	}
}

func TestParseRejectsOrderNotDerivedFromSeed(t *testing.T) {
	data := strings.Replace(validScenarioYAML(), "      - fault-3\n      - control-2", "      - control-2\n      - fault-3", 1)
	_, err := Parse([]byte(data))
	if err == nil || !strings.Contains(err.Error(), "does not match SHA-256") {
		t.Fatalf("Parse() error = %v", err)
	}
}

func TestParseRejectsMultipleDocuments(t *testing.T) {
	_, err := Parse([]byte(validScenarioYAML() + "\n---\n{}\n"))
	if err == nil || !strings.Contains(err.Error(), "exactly one YAML document") {
		t.Fatalf("Parse() error = %v", err)
	}
}

func TestParseRejectsOversizedInput(t *testing.T) {
	_, err := Parse(bytes.Repeat([]byte{'x'}, maxScenarioBytes+1))
	if err == nil || !strings.Contains(err.Error(), "input limit") {
		t.Fatalf("Parse() error = %v", err)
	}
}

func validScenarioYAML() string {
	data, err := os.ReadFile(filepath.Join("..", "..", "scenarios", "cl-p2p-partition.yaml"))
	if err != nil {
		panic(err)
	}
	return string(data)
}
