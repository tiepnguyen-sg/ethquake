package experiment

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tiepnguyen-sg/ethquake/internal/beacon"
	"github.com/tiepnguyen-sg/ethquake/internal/scenario"
	"github.com/tiepnguyen-sg/ethquake/internal/topology"
)

func TestLoadRunSummariesUsesCommittedOrder(t *testing.T) {
	value := readScenario(t)
	runs := validRuns(value, []uint64{5, 5, 5}, []uint64{8, 8, 8})
	root := t.TempDir()
	for _, run := range runs {
		directory := filepath.Join(root, run.RunID)
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		writeRunEvidence(t, directory, value, run)
	}
	loaded, err := LoadRunSummaries(root, value)
	if err != nil {
		t.Fatalf("LoadRunSummaries() error = %v", err)
	}
	for index, runID := range value.Spec.Methodology.RunOrder {
		if loaded[index].RunID != runID {
			t.Fatalf("loaded order = %+v", loaded)
		}
	}
}

func TestLoadRunSummariesRejectsTamperedManifest(t *testing.T) {
	value := readScenario(t)
	runs := validRuns(value, []uint64{5, 5, 5}, []uint64{8, 8, 8})
	root := t.TempDir()
	for _, run := range runs {
		directory := filepath.Join(root, run.RunID)
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		writeRunEvidence(t, directory, value, run)
	}
	path := filepath.Join(root, runs[0].RunID, "manifest.json")
	if err := os.WriteFile(path, []byte(`{"tampered":true}`), 0o600); err != nil {
		t.Fatalf("tamper manifest: %v", err)
	}
	if _, err := LoadRunSummaries(root, value); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("LoadRunSummaries() error = %v", err)
	}
}

func TestLoadRunSummariesRejectsDifferentExecutedScenario(t *testing.T) {
	value := readScenario(t)
	runs := validRuns(value, []uint64{5, 5, 5}, []uint64{8, 8, 8})
	root := writeRunSet(t, value, runs)
	directory := filepath.Join(root, runs[0].RunID)
	path := filepath.Join(directory, "scenario.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read scenario: %v", err)
	}
	changed := strings.Replace(string(data), "Finality should not advance", "Finality is expected not to advance", 1)
	if changed == string(data) {
		t.Fatal("scenario fixture did not contain expected text")
	}
	if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
		t.Fatalf("write changed scenario: %v", err)
	}
	writeChecksums(t, directory)
	if _, err := LoadRunSummaries(root, value); err == nil || !strings.Contains(err.Error(), "executed scenario does not match") {
		t.Fatalf("LoadRunSummaries() error = %v", err)
	}
}

func TestLoadRunSummariesRejectsMetadataMismatch(t *testing.T) {
	value := readScenario(t)
	runs := validRuns(value, []uint64{5, 5, 5}, []uint64{8, 8, 8})
	root := writeRunSet(t, value, runs)
	directory := filepath.Join(root, runs[0].RunID)
	path := filepath.Join(directory, "metadata.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	var metadata RunMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	metadata.ChainID++
	writeJSONFixture(t, path, metadata)
	writeChecksums(t, directory)
	if _, err := LoadRunSummaries(root, value); err == nil || !strings.Contains(err.Error(), "chain_id") {
		t.Fatalf("LoadRunSummaries() error = %v", err)
	}
}

func TestLoadRunSummariesRejectsRealizedSplitMismatch(t *testing.T) {
	value := readScenario(t)
	runs := validRuns(value, []uint64{5, 5, 5}, []uint64{8, 8, 8})
	root := writeRunSet(t, value, runs)
	directory := filepath.Join(root, runs[0].RunID)
	path := filepath.Join(directory, "realized-split.json")
	split := runs[0].RealizedSplit
	split.TotalActiveBalance++
	writeJSONFixture(t, path, split)
	writeChecksums(t, directory)
	if _, err := LoadRunSummaries(root, value); err == nil || !strings.Contains(err.Error(), "does not match manifest") {
		t.Fatalf("LoadRunSummaries() error = %v", err)
	}
}

func TestParseRunSummaryRejectsUnknownField(t *testing.T) {
	_, err := parseRunSummary([]byte(`{"schema_version":"ethquake.run/v1alpha1","unknown":true}`))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("parseRunSummary() error = %v", err)
	}
}

func writeRunSet(t *testing.T, value scenario.Scenario, runs []RunSummary) string {
	t.Helper()
	root := t.TempDir()
	for _, run := range runs {
		directory := filepath.Join(root, run.RunID)
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		writeRunEvidence(t, directory, value, run)
	}
	return root
}

func writeRunEvidence(t *testing.T, directory string, value scenario.Scenario, run RunSummary) {
	t.Helper()
	scenarioData, err := os.ReadFile(filepath.Join("..", "..", "scenarios", "cl-p2p-partition.yaml"))
	if err != nil {
		t.Fatalf("read scenario fixture: %v", err)
	}
	writeJSONFixture(t, filepath.Join(directory, "manifest.json"), run)
	writeJSONFixture(t, filepath.Join(directory, "realized-split.json"), run.RealizedSplit)
	if err := os.WriteFile(filepath.Join(directory, "scenario.yaml"), scenarioData, 0o600); err != nil {
		t.Fatalf("write scenario: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "raw-timeseries.jsonl"), []byte("{\"type\":\"fixture\"}\n"), 0o600); err != nil {
		t.Fatalf("write raw time series: %v", err)
	}
	digest := sha256.Sum256(scenarioData)
	metadata := RunMetadata{
		SchemaVersion:  MetadataSchemaVersion,
		RunID:          run.RunID,
		ScenarioSHA256: fmt.Sprintf("%x", digest[:]),
		ChainID:        value.Spec.Target.ChainID,
		RuntimeSpecs:   make(map[string]beacon.Spec, len(value.Spec.Topology.Participants)),
		RuntimeGenesis: make(map[string]beacon.Genesis, len(value.Spec.Topology.Participants)),
		Placements:     runMetadataPlacements(value),
		Dependencies:   run.Dependencies,
		OrderSeed:      value.Spec.Methodology.OrderSeed,
		RunOrder:       append([]string(nil), value.Spec.Methodology.RunOrder...),
	}
	if run.Condition == ConditionFault {
		metadata.FaultDeadmanSeconds = 10
	}
	for _, participant := range value.Spec.Topology.Participants {
		metadata.RuntimeSpecs[participant.BeaconTarget] = beacon.Spec{
			SecondsPerSlot: 1,
			SlotsPerEpoch:  2,
			ForkEpochs:     map[string]string{"DENEB_FORK_EPOCH": "0"},
		}
		metadata.RuntimeGenesis[participant.BeaconTarget] = beacon.Genesis{
			Time:           1,
			ValidatorsRoot: "0x" + strings.Repeat("1", 64),
			ForkVersion:    "0x" + strings.Repeat("2", 8),
		}
	}
	writeJSONFixture(t, filepath.Join(directory, "metadata.json"), metadata)
	writeChecksums(t, directory)
}

func runMetadataPlacements(value scenario.Scenario) []topology.Placement {
	placements := make([]topology.Placement, 0, len(value.Spec.Topology.Participants))
	for index, participant := range value.Spec.Topology.Participants {
		placements = append(placements, topology.Placement{
			ParticipantID:  participant.ID,
			Target:         participant.BeaconTarget,
			Pod:            "pod-" + participant.ID,
			Node:           fmt.Sprintf("node-%d", index+1),
			NodePool:       fmt.Sprintf("pool-%d", index+1),
			MachineType:    "fixture-instance",
			ResourcePolicy: "fixture-policy",
		})
	}
	return placements
}

func writeJSONFixture(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %s: %v", filepath.Base(path), err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", filepath.Base(path), err)
	}
}

func writeChecksums(t *testing.T, directory string) {
	t.Helper()
	checksums := make(map[string]string, len(checksummedRunArtifacts))
	for _, name := range checksummedRunArtifacts {
		path := filepath.Join(directory, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		digest := sha256.Sum256(data)
		checksums[name] = fmt.Sprintf("%x", digest[:])
	}
	data, err := json.Marshal(checksums)
	if err != nil {
		t.Fatalf("marshal checksums: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "checksums.json"), data, 0o600); err != nil {
		t.Fatalf("write checksums: %v", err)
	}
}
