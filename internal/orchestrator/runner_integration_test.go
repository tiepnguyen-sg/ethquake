package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiepnguyen-sg/ethquake/internal/artifact"
	"github.com/tiepnguyen-sg/ethquake/internal/beacon"
	"github.com/tiepnguyen-sg/ethquake/internal/experiment"
	"github.com/tiepnguyen-sg/ethquake/internal/fault"
	"github.com/tiepnguyen-sg/ethquake/internal/observer"
	"github.com/tiepnguyen-sg/ethquake/internal/report"
	"github.com/tiepnguyen-sg/ethquake/internal/scenario"
	"github.com/tiepnguyen-sg/ethquake/internal/topology"
)

func TestSixRunWorkflowProducesAdjudicableEvidence(t *testing.T) {
	scenarioYAML, err := os.ReadFile(filepath.Join("..", "..", "scenarios", "cl-p2p-partition.yaml"))
	if err != nil {
		t.Fatalf("read scenario: %v", err)
	}
	value, err := scenario.Parse(scenarioYAML)
	if err != nil {
		t.Fatalf("parse scenario: %v", err)
	}
	workspace := t.TempDir()
	runsRoot := filepath.Join(workspace, "runs")
	dependencies := fixtureDependencies()
	ranges, validators := fixtureValidatorTopology(value)

	for _, runID := range value.Spec.Methodology.RunOrder {
		directory, err := artifact.Create(runsRoot, runID)
		if err != nil {
			t.Fatalf("create artifacts for %s: %v", runID, err)
		}
		network := newControlledBeaconNetwork(value)
		backend := newControlledFaultBackend(network)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		outcomes := make(chan runOutcome, 1)
		go func() {
			summary, runErr := Run(ctx, Config{
				Scenario:         value,
				ScenarioYAML:     scenarioYAML,
				RunID:            runID,
				Targets:          network.targets(value),
				ValidatorClient:  staticValidatorClient{validators: validators},
				ValidatorRanges:  ranges,
				ChainIDClient:    staticChainIDClient(value.Spec.Target.ChainID),
				FaultBackend:     backend,
				Namespace:        value.Spec.Target.NamespacePrefix + runID,
				Placements:       fixturePlacements(value),
				Dependencies:     dependencies,
				Artifacts:        directory,
				PollInterval:     time.Millisecond,
				HeadHistoryLimit: 16,
				Now:              network.now,
			})
			outcomes <- runOutcome{summary: summary, err: runErr}
		}()

		driveErr := driveControlledRun(ctx, network, backend, strings.HasPrefix(runID, "fault-"))
		if driveErr != nil {
			cancel()
		}
		var outcome runOutcome
		select {
		case outcome = <-outcomes:
		case <-ctx.Done():
			outcome.err = ctx.Err()
		}
		cancel()
		if driveErr != nil || outcome.err != nil {
			t.Fatalf("run %s failed: %v", runID, errors.Join(driveErr, outcome.err))
		}
		if outcome.summary.RunID != runID || !outcome.summary.PrimaryMeasurementComplete {
			t.Fatalf("run %s summary is incomplete: %+v", runID, outcome.summary)
		}
		if err := backend.validate(runID, strings.HasPrefix(runID, "fault-")); err != nil {
			t.Fatalf("run %s fault lifecycle: %v", runID, err)
		}
	}

	loaded, err := experiment.LoadRunSummaries(runsRoot, value)
	if err != nil {
		t.Fatalf("load evidence: %v", err)
	}
	for index, run := range loaded {
		if run.RunID != value.Spec.Methodology.RunOrder[index] {
			t.Fatalf("loaded run %d = %s; want %s", index, run.RunID, value.Spec.Methodology.RunOrder[index])
		}
	}
	analysis, err := experiment.Analyze(value, loaded, time.Unix(1_900_000_000, 0))
	if err != nil {
		t.Fatalf("analyze evidence: %v", err)
	}
	if analysis.GateA.Outcome != "yes" || analysis.GateB.Outcome != "yes" || analysis.GateC.Outcome != "no" || analysis.Conclusion != "valid_predicted_uniform_effect" {
		t.Fatalf("unexpected analysis gates: A=%s B=%s C=%s conclusion=%s", analysis.GateA.Outcome, analysis.GateB.Outcome, analysis.GateC.Outcome, analysis.Conclusion)
	}

	analysisRoot := filepath.Join(workspace, "analysis")
	directory, err := artifact.Create(analysisRoot, value.Metadata.Name+"-analysis")
	if err != nil {
		t.Fatalf("create analysis artifacts: %v", err)
	}
	if err := directory.WriteJSON("report.json", analysis); err != nil {
		t.Fatalf("write JSON report: %v", err)
	}
	if err := directory.Write("report.md", strings.NewReader(string(report.Markdown(analysis)))); err != nil {
		t.Fatalf("write Markdown report: %v", err)
	}
	if err := directory.Write("finality-progress.svg", strings.NewReader(string(report.SVG(analysis)))); err != nil {
		t.Fatalf("write SVG report: %v", err)
	}
	for _, name := range []string{"report.json", "report.md", "finality-progress.svg"} {
		info, err := os.Stat(filepath.Join(directory.Path(), name))
		if err != nil || info.Size() == 0 {
			t.Fatalf("analysis artifact %s is missing or empty: %v", name, err)
		}
	}
}

type runOutcome struct {
	summary experiment.RunSummary
	err     error
}

type headRequest struct {
	target   string
	response chan uint64
}

type controlledBeaconNetwork struct {
	mu             sync.Mutex
	requests       chan headRequest
	targetCount    int
	genesisTime    uint64
	currentSlot    uint64
	faultActive    bool
	frozenFinality uint64
	spec           beacon.Spec
	genesis        beacon.Genesis
}

func newControlledBeaconNetwork(value scenario.Scenario) *controlledBeaconNetwork {
	const genesisTime = uint64(1_800_000_000)
	return &controlledBeaconNetwork{
		requests:    make(chan headRequest, len(value.Spec.Topology.Participants)*2),
		targetCount: len(value.Spec.Topology.Participants),
		genesisTime: genesisTime,
		spec: beacon.Spec{
			SecondsPerSlot: 1,
			SlotsPerEpoch:  2,
			ForkEpochs:     map[string]string{"DENEB_FORK_EPOCH": "0"},
		},
		genesis: beacon.Genesis{
			Time:           genesisTime,
			ValidatorsRoot: "0x" + strings.Repeat("1", 64),
			ForkVersion:    "0x" + strings.Repeat("2", 8),
		},
	}
}

func (network *controlledBeaconNetwork) targets(value scenario.Scenario) []observer.Target {
	targets := make([]observer.Target, 0, len(value.Spec.Topology.Participants))
	for _, participant := range value.Spec.Topology.Participants {
		targets = append(targets, observer.Target{
			Name:   participant.BeaconTarget,
			Beacon: &controlledBeaconClient{target: participant.BeaconTarget, network: network},
		})
	}
	return targets
}

func (network *controlledBeaconNetwork) now() time.Time {
	network.mu.Lock()
	defer network.mu.Unlock()
	return time.Unix(int64(network.genesisTime+network.currentSlot), 0).UTC()
}

func (network *controlledBeaconNetwork) advance(ctx context.Context, slot uint64) error {
	requests := make([]headRequest, 0, network.targetCount)
	seen := make(map[string]struct{}, network.targetCount)
	for len(requests) < network.targetCount {
		select {
		case request := <-network.requests:
			if _, duplicate := seen[request.target]; duplicate {
				return fmt.Errorf("target %q requested slot %d more than once", request.target, slot)
			}
			seen[request.target] = struct{}{}
			requests = append(requests, request)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	network.mu.Lock()
	if slot < network.currentSlot {
		network.mu.Unlock()
		return fmt.Errorf("slot regressed from %d to %d", network.currentSlot, slot)
	}
	network.currentSlot = slot
	network.mu.Unlock()
	for _, request := range requests {
		request.response <- slot
	}
	return nil
}

func (network *controlledBeaconNetwork) setFault(active bool) {
	network.mu.Lock()
	defer network.mu.Unlock()
	if active {
		network.frozenFinality = network.currentSlot / network.spec.SlotsPerEpoch
	}
	network.faultActive = active
}

func (network *controlledBeaconNetwork) finalizedEpoch(slot uint64) uint64 {
	network.mu.Lock()
	defer network.mu.Unlock()
	if network.faultActive {
		return network.frozenFinality
	}
	return slot / network.spec.SlotsPerEpoch
}

type controlledBeaconClient struct {
	mu       sync.Mutex
	target   string
	network  *controlledBeaconNetwork
	lastSlot uint64
}

func (client *controlledBeaconClient) Spec(context.Context) (beacon.Spec, error) {
	return client.network.spec, nil
}

func (client *controlledBeaconClient) Genesis(context.Context) (beacon.Genesis, error) {
	return client.network.genesis, nil
}

func (client *controlledBeaconClient) Head(ctx context.Context) (beacon.Head, error) {
	response := make(chan uint64, 1)
	select {
	case client.network.requests <- headRequest{target: client.target, response: response}:
	case <-ctx.Done():
		return beacon.Head{}, ctx.Err()
	}
	var slot uint64
	select {
	case slot = <-response:
	case <-ctx.Done():
		return beacon.Head{}, ctx.Err()
	}
	client.mu.Lock()
	client.lastSlot = slot
	client.mu.Unlock()
	return beacon.Head{
		Slot:       slot,
		Root:       fixtureRoot(slot + 1),
		ParentRoot: fixtureRoot(slot),
		StateRoot:  fixtureRoot(slot + 100),
		Canonical:  true,
	}, nil
}

func (client *controlledBeaconClient) Finality(ctx context.Context, _ string) (beacon.Finality, error) {
	select {
	case <-ctx.Done():
		return beacon.Finality{}, ctx.Err()
	default:
	}
	client.mu.Lock()
	slot := client.lastSlot
	client.mu.Unlock()
	epoch := client.network.finalizedEpoch(slot)
	return beacon.Finality{Epoch: epoch, Root: fixtureRoot(epoch + 1_000)}, nil
}

func fixtureRoot(value uint64) string {
	return fmt.Sprintf("0x%064x", value)
}

type controlledFaultBackend struct {
	mu       sync.Mutex
	network  *controlledBeaconNetwork
	applied  chan struct{}
	reverted chan struct{}
	request  fault.PartitionRequest
	active   bool
	applies  int
	reverts  int
}

func newControlledFaultBackend(network *controlledBeaconNetwork) *controlledFaultBackend {
	return &controlledFaultBackend{
		network:  network,
		applied:  make(chan struct{}, 1),
		reverted: make(chan struct{}, 1),
	}
}

func (backend *controlledFaultBackend) Apply(_ context.Context, request fault.PartitionRequest) error {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.active {
		return errors.New("fixture partition is already active")
	}
	backend.request = request
	backend.active = true
	backend.applies++
	backend.network.setFault(true)
	backend.applied <- struct{}{}
	return nil
}

func (backend *controlledFaultBackend) Revert(_ context.Context, request fault.PartitionRequest) error {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if !backend.active || !reflect.DeepEqual(request, backend.request) {
		return errors.New("fixture partition revert does not match the active request")
	}
	backend.active = false
	backend.reverts++
	backend.network.setFault(false)
	backend.reverted <- struct{}{}
	return nil
}

func (backend *controlledFaultBackend) Status(context.Context, fault.PartitionRequest) (fault.Status, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return fault.Status{Exists: backend.active, AllInjected: backend.active, AllRecovered: !backend.active}, nil
}

func (backend *controlledFaultBackend) validate(runID string, expectFault bool) error {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if !expectFault {
		if backend.applies != 0 || backend.reverts != 0 {
			return errors.New("control run touched the fault backend")
		}
		return nil
	}
	if backend.applies != 1 || backend.reverts != 1 || backend.active {
		return fmt.Errorf("apply=%d revert=%d active=%t", backend.applies, backend.reverts, backend.active)
	}
	if backend.request.RunID != runID || backend.request.TTL != 10*time.Second {
		return fmt.Errorf("unexpected request: %+v", backend.request)
	}
	return nil
}

func driveControlledRun(ctx context.Context, network *controlledBeaconNetwork, backend *controlledFaultBackend, isFault bool) error {
	if err := network.advance(ctx, 0); err != nil {
		return err
	}
	if !isFault {
		for slot := uint64(1); slot <= 6; slot++ {
			if err := network.advance(ctx, slot); err != nil {
				return err
			}
		}
		return nil
	}
	if err := network.advance(ctx, 1); err != nil {
		return err
	}
	if err := waitForFixtureEvent(ctx, backend.applied); err != nil {
		return fmt.Errorf("wait for fault application: %w", err)
	}
	for slot := uint64(2); slot <= 8; slot++ {
		if err := network.advance(ctx, slot); err != nil {
			return err
		}
	}
	if err := waitForFixtureEvent(ctx, backend.reverted); err != nil {
		return fmt.Errorf("wait for fault removal: %w", err)
	}
	return network.advance(ctx, 9)
}

func waitForFixtureEvent(ctx context.Context, event <-chan struct{}) error {
	select {
	case <-event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type staticChainIDClient uint64

func (client staticChainIDClient) ChainID(context.Context) (uint64, error) {
	return uint64(client), nil
}

type staticValidatorClient struct {
	validators []beacon.Validator
}

func (client staticValidatorClient) Validators(context.Context) ([]beacon.Validator, error) {
	return append([]beacon.Validator(nil), client.validators...), nil
}

func fixtureValidatorTopology(value scenario.Scenario) ([]topology.ValidatorRange, []beacon.Validator) {
	ranges := make([]topology.ValidatorRange, 0, len(value.Spec.Topology.Participants))
	validators := make([]beacon.Validator, 0, 128)
	var start uint64
	for _, participant := range value.Spec.Topology.Participants {
		end := start + participant.ValidatorCount - 1
		ranges = append(ranges, topology.ValidatorRange{Start: start, End: end, Owner: participant.ValidatorRangeName})
		for index := start; index <= end; index++ {
			validators = append(validators, beacon.Validator{Index: index, EffectiveBalance: 32_000_000_000, Status: "active_ongoing"})
		}
		start = end + 1
	}
	return ranges, validators
}

func fixtureDependencies() experiment.DependencyMetadata {
	return experiment.DependencyMetadata{
		EthereumPackageRevision: strings.Repeat("a", 40),
		ImportedPackages: map[string]string{
			"github.com/ethpandaops/ethereum-package": strings.Repeat("b", 40),
		},
		RuntimeImages: map[string]string{
			"fixture": "example.invalid/ethquake/fixture:v1@sha256:" + strings.Repeat("c", 64),
		},
	}
}
