package experiment

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/tiepnguyen-sg/ethquake/internal/beacon"
	"github.com/tiepnguyen-sg/ethquake/internal/scenario"
	"github.com/tiepnguyen-sg/ethquake/internal/topology"
)

var checksummedRunArtifacts = []string{
	"manifest.json",
	"metadata.json",
	"raw-timeseries.jsonl",
	"realized-split.json",
	"scenario.yaml",
}

func LoadRunSummaries(root string, value scenario.Scenario) ([]RunSummary, error) {
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return nil, errors.New("canonical evidence cannot be loaded from inside Kubernetes")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve runs root: %w", err)
	}
	rootInfo, err := os.Lstat(absoluteRoot)
	if err != nil {
		return nil, fmt.Errorf("inspect runs root: %w", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return nil, errors.New("runs root must be a real directory, not a symlink")
	}
	resolvedRoot, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve runs root symlinks: %w", err)
	}
	cleanRoot := filepath.ToSlash(resolvedRoot)
	if strings.Contains(cleanRoot, "/.kurtosis/") || strings.HasSuffix(cleanRoot, "/.kurtosis") {
		return nil, errors.New("cluster-lifecycle storage cannot be an evidence root")
	}
	runs := make([]RunSummary, 0, len(value.Spec.Methodology.RunOrder))
	for _, runID := range value.Spec.Methodology.RunOrder {
		runDirectory := filepath.Join(resolvedRoot, runID)
		directoryInfo, err := os.Lstat(runDirectory)
		if err != nil {
			return nil, fmt.Errorf("inspect run directory %q: %w", runID, err)
		}
		if directoryInfo.Mode()&os.ModeSymlink != 0 || !directoryInfo.IsDir() {
			return nil, fmt.Errorf("run directory %q must be a real directory, not a symlink", runID)
		}
		if err := verifyRunChecksums(runDirectory); err != nil {
			return nil, fmt.Errorf("verify run %q: %w", runID, err)
		}
		run, err := loadRunEvidence(runDirectory, runID, value)
		if err != nil {
			return nil, fmt.Errorf("validate run %q evidence: %w", runID, err)
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func loadRunEvidence(runDirectory, expectedRunID string, expectedScenario scenario.Scenario) (RunSummary, error) {
	manifestData, err := readRegularArtifact(runDirectory, "manifest.json")
	if err != nil {
		return RunSummary{}, err
	}
	run, err := parseRunSummary(manifestData)
	if err != nil {
		return RunSummary{}, fmt.Errorf("parse manifest.json: %w", err)
	}
	if run.RunID != expectedRunID {
		return RunSummary{}, fmt.Errorf("manifest run_id %q does not match directory %q", run.RunID, expectedRunID)
	}

	scenarioData, err := readRegularArtifact(runDirectory, "scenario.yaml")
	if err != nil {
		return RunSummary{}, err
	}
	executedScenario, err := scenario.Parse(scenarioData)
	if err != nil {
		return RunSummary{}, fmt.Errorf("parse scenario.yaml: %w", err)
	}
	if !reflect.DeepEqual(executedScenario, expectedScenario) {
		return RunSummary{}, errors.New("executed scenario does not match the analysis scenario")
	}

	metadataData, err := readRegularArtifact(runDirectory, "metadata.json")
	if err != nil {
		return RunSummary{}, err
	}
	metadata, err := parseStrictJSON[RunMetadata](metadataData, "metadata.json")
	if err != nil {
		return RunSummary{}, err
	}
	if err := validateRunMetadata(metadata, run, expectedScenario, scenarioData); err != nil {
		return RunSummary{}, fmt.Errorf("validate metadata.json: %w", err)
	}

	splitData, err := readRegularArtifact(runDirectory, "realized-split.json")
	if err != nil {
		return RunSummary{}, err
	}
	split, err := parseStrictJSON[topology.RealizedSplit](splitData, "realized-split.json")
	if err != nil {
		return RunSummary{}, err
	}
	if !reflect.DeepEqual(split, run.RealizedSplit) {
		return RunSummary{}, errors.New("realized-split.json does not match manifest.json")
	}
	if err := topology.ValidateRealizedSplit(expectedScenario, split); err != nil {
		return RunSummary{}, fmt.Errorf("validate realized-split.json: %w", err)
	}
	if err := requireNonemptyRegularArtifact(runDirectory, "raw-timeseries.jsonl"); err != nil {
		return RunSummary{}, err
	}
	return run, nil
}

func validateRunMetadata(metadata RunMetadata, run RunSummary, value scenario.Scenario, scenarioData []byte) error {
	if metadata.SchemaVersion != MetadataSchemaVersion {
		return fmt.Errorf("unsupported schema_version %q", metadata.SchemaVersion)
	}
	if metadata.RunID != run.RunID {
		return fmt.Errorf("run_id %q does not match manifest %q", metadata.RunID, run.RunID)
	}
	digest := sha256.Sum256(scenarioData)
	if metadata.ScenarioSHA256 != fmt.Sprintf("%x", digest[:]) {
		return errors.New("scenario_sha256 does not match scenario.yaml")
	}
	if metadata.ChainID != value.Spec.Target.ChainID {
		return fmt.Errorf("chain_id %d does not match scenario target %d", metadata.ChainID, value.Spec.Target.ChainID)
	}
	if metadata.OrderSeed != value.Spec.Methodology.OrderSeed || !reflect.DeepEqual(metadata.RunOrder, value.Spec.Methodology.RunOrder) {
		return errors.New("committed run order metadata does not match the scenario")
	}
	if !dependencyMetadataEqual(metadata.Dependencies, run.Dependencies) {
		return errors.New("dependency metadata does not match manifest.json")
	}
	if err := ValidateDependencyMetadata(metadata.Dependencies); err != nil {
		return fmt.Errorf("validate dependency metadata: %w", err)
	}
	if err := topology.ValidatePlacements(value, metadata.Placements); err != nil {
		return fmt.Errorf("validate placements: %w", err)
	}
	spec, err := validateRuntimeMetadata(metadata, value)
	if err != nil {
		return err
	}
	condition, _, err := parseRunID(run.RunID)
	if err != nil {
		return err
	}
	if condition == ConditionControl {
		if metadata.FaultDeadmanSeconds != 0 {
			return errors.New("control run records a non-zero fault deadman duration")
		}
		return nil
	}
	deadmanEpochs := value.Spec.Fault.DurationEpochs + 2
	deadmanSlots, overflow := multiplyUint64(deadmanEpochs, spec.SlotsPerEpoch)
	if overflow {
		return errors.New("fault deadman slot calculation overflows uint64")
	}
	expectedSeconds, overflow := multiplyUint64(deadmanSlots, spec.SecondsPerSlot)
	if overflow || metadata.FaultDeadmanSeconds != expectedSeconds {
		return fmt.Errorf("fault_deadman_seconds %d does not match runtime-derived value %d", metadata.FaultDeadmanSeconds, expectedSeconds)
	}
	return nil
}

func validateRuntimeMetadata(metadata RunMetadata, value scenario.Scenario) (beacon.Spec, error) {
	if len(metadata.RuntimeSpecs) != len(value.Spec.Topology.Participants) || len(metadata.RuntimeGenesis) != len(value.Spec.Topology.Participants) {
		return beacon.Spec{}, errors.New("runtime spec and genesis metadata must cover every participant target")
	}
	var referenceSpec beacon.Spec
	var referenceGenesis beacon.Genesis
	for index, participant := range value.Spec.Topology.Participants {
		spec, specExists := metadata.RuntimeSpecs[participant.BeaconTarget]
		genesis, genesisExists := metadata.RuntimeGenesis[participant.BeaconTarget]
		if !specExists || !genesisExists {
			return beacon.Spec{}, fmt.Errorf("runtime metadata is missing target %q", participant.BeaconTarget)
		}
		if spec.SecondsPerSlot == 0 || spec.SlotsPerEpoch == 0 || len(spec.ForkEpochs) == 0 {
			return beacon.Spec{}, fmt.Errorf("target %q has incomplete runtime spec metadata", participant.BeaconTarget)
		}
		for name, epoch := range spec.ForkEpochs {
			parsed, err := strconv.ParseUint(epoch, 10, 64)
			if name == "" || err != nil || strconv.FormatUint(parsed, 10) != epoch {
				return beacon.Spec{}, fmt.Errorf("target %q has invalid fork epoch %q=%q", participant.BeaconTarget, name, epoch)
			}
		}
		if genesis.Time == 0 || !isPrefixedLowerHex(genesis.ValidatorsRoot, 64) || !isPrefixedLowerHex(genesis.ForkVersion, 8) {
			return beacon.Spec{}, fmt.Errorf("target %q has incomplete runtime genesis metadata", participant.BeaconTarget)
		}
		if index == 0 {
			referenceSpec = spec
			referenceGenesis = genesis
			continue
		}
		if !spec.Compatible(referenceSpec) || !referenceSpec.Compatible(spec) {
			return beacon.Spec{}, fmt.Errorf("target %q runtime spec conflicts with other targets", participant.BeaconTarget)
		}
		if !genesis.Compatible(referenceGenesis) {
			return beacon.Spec{}, fmt.Errorf("target %q genesis conflicts with other targets", participant.BeaconTarget)
		}
	}
	return referenceSpec, nil
}

func readRegularArtifact(runDirectory, name string) ([]byte, error) {
	path := filepath.Join(runDirectory, name)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", name, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a regular file, not a symlink", name)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	return data, nil
}

func requireNonemptyRegularArtifact(runDirectory, name string) error {
	path := filepath.Join(runDirectory, name)
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", name, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("%s must be a regular file, not a symlink", name)
	}
	if info.Size() == 0 {
		return fmt.Errorf("%s must not be empty", name)
	}
	return nil
}

func parseStrictJSON[T any](data []byte, name string) (T, error) {
	var value T
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, fmt.Errorf("decode %s: %w", name, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return value, fmt.Errorf("%s must contain exactly one JSON value", name)
		}
		return value, fmt.Errorf("decode trailing %s: %w", name, err)
	}
	return value, nil
}

func multiplyUint64(left, right uint64) (uint64, bool) {
	if left != 0 && right > math.MaxUint64/left {
		return 0, true
	}
	return left * right, false
}

func isPrefixedLowerHex(value string, digits int) bool {
	return strings.HasPrefix(value, "0x") && isLowerHex(value[2:], digits)
}

func verifyRunChecksums(runDirectory string) error {
	path := filepath.Join(runDirectory, "checksums.json")
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect checksums: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("checksums.json must be a regular file, not a symlink")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read checksums: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var checksums map[string]string
	if err := decoder.Decode(&checksums); err != nil {
		return fmt.Errorf("decode checksums: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("checksums.json must contain exactly one JSON value")
		}
		return fmt.Errorf("decode trailing checksums: %w", err)
	}
	if len(checksums) != len(checksummedRunArtifacts) {
		return fmt.Errorf("checksums.json has %d entries; expected %d", len(checksums), len(checksummedRunArtifacts))
	}
	for _, name := range checksummedRunArtifacts {
		expected, exists := checksums[name]
		if !exists || !isLowerHex(expected, 64) {
			return fmt.Errorf("artifact %q has no valid SHA-256", name)
		}
		artifactPath := filepath.Join(runDirectory, name)
		artifactInfo, err := os.Lstat(artifactPath)
		if err != nil {
			return fmt.Errorf("inspect artifact %q: %w", name, err)
		}
		if artifactInfo.Mode()&os.ModeSymlink != 0 || !artifactInfo.Mode().IsRegular() {
			return fmt.Errorf("artifact %q must be a regular file, not a symlink", name)
		}
		file, err := os.Open(artifactPath)
		if err != nil {
			return fmt.Errorf("open artifact %q: %w", name, err)
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return fmt.Errorf("hash artifact %q: %w", name, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close artifact %q: %w", name, closeErr)
		}
		actual := fmt.Sprintf("%x", hash.Sum(nil))
		if actual != expected {
			return fmt.Errorf("artifact %q checksum mismatch", name)
		}
	}
	return nil
}

func parseRunSummary(data []byte) (RunSummary, error) {
	return parseStrictJSON[RunSummary](data, "run manifest")
}
