// Package orchestrator coordinates scenarios without leaking experiment
// concepts into the standalone Observer or fault backend.
package orchestrator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tiepnguyen-sg/ethquake/internal/artifact"
	"github.com/tiepnguyen-sg/ethquake/internal/beacon"
	"github.com/tiepnguyen-sg/ethquake/internal/experiment"
	"github.com/tiepnguyen-sg/ethquake/internal/fault"
	"github.com/tiepnguyen-sg/ethquake/internal/observer"
	"github.com/tiepnguyen-sg/ethquake/internal/scenario"
	"github.com/tiepnguyen-sg/ethquake/internal/timeseries"
	"github.com/tiepnguyen-sg/ethquake/internal/topology"
	"golang.org/x/sync/errgroup"
)

type ChainIDClient interface {
	ChainID(context.Context) (uint64, error)
}

type ValidatorClient interface {
	Validators(context.Context) ([]beacon.Validator, error)
}

type Config struct {
	Scenario         scenario.Scenario
	ScenarioYAML     []byte
	RunID            string
	Targets          []observer.Target
	ValidatorClient  ValidatorClient
	ValidatorRanges  []topology.ValidatorRange
	ChainIDClient    ChainIDClient
	FaultBackend     fault.Backend
	Namespace        string
	Placements       []topology.Placement
	Dependencies     experiment.DependencyMetadata
	Artifacts        *artifact.Directory
	PollInterval     time.Duration
	HeadHistoryLimit int
	Now              func() time.Time
}

func Run(ctx context.Context, config Config) (_ experiment.RunSummary, returnErr error) {
	condition, repetition, err := validateConfig(config)
	if err != nil {
		return experiment.RunSummary{}, err
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	chainID, err := config.ChainIDClient.ChainID(ctx)
	if err != nil {
		return experiment.RunSummary{}, fmt.Errorf("verify execution chain ID: %w", err)
	}
	if chainID != config.Scenario.Spec.Target.ChainID {
		return experiment.RunSummary{}, fmt.Errorf("execution chain ID %d does not match scenario target %d", chainID, config.Scenario.Spec.Target.ChainID)
	}
	validators, err := config.ValidatorClient.Validators(ctx)
	if err != nil {
		return experiment.RunSummary{}, fmt.Errorf("read active validator weights: %w", err)
	}
	realizedSplit, err := topology.CalculateRealizedSplit(config.Scenario, config.ValidatorRanges, validators)
	if err != nil {
		return experiment.RunSummary{}, fmt.Errorf("calculate realized validator split: %w", err)
	}
	if !realizedSplit.WithinTolerance {
		return experiment.RunSummary{}, errors.New("realized validator-weight split is outside the preregistered tolerance")
	}

	if err := config.Artifacts.Write("scenario.yaml", bytes.NewReader(config.ScenarioYAML)); err != nil {
		return experiment.RunSummary{}, err
	}
	if err := config.Artifacts.WriteJSON("realized-split.json", realizedSplit); err != nil {
		return experiment.RunSummary{}, err
	}
	rawFile, err := config.Artifacts.CreateFile("raw-timeseries.jsonl")
	if err != nil {
		return experiment.RunSummary{}, err
	}
	rawOpen := true
	closeRaw := func() error {
		if !rawOpen {
			return nil
		}
		rawOpen = false
		return errors.Join(rawFile.Sync(), rawFile.Close())
	}
	defer func() {
		returnErr = errors.Join(returnErr, closeRaw())
	}()

	jsonRecorder, err := timeseries.NewJSONLRecorder(rawFile)
	if err != nil {
		return experiment.RunSummary{}, fmt.Errorf("create raw time-series recorder: %w", err)
	}
	datasetRecorder := observer.NewDatasetRecorder()
	multiRecorder, err := observer.NewMultiRecorder(jsonRecorder, datasetRecorder)
	if err != nil {
		return experiment.RunSummary{}, fmt.Errorf("create experiment recorders: %w", err)
	}
	measurementObserver, err := observer.New(config.Targets, multiRecorder, observer.Options{
		PollInterval:     config.PollInterval,
		HeadHistoryLimit: config.HeadHistoryLimit,
		Now:              config.Now,
	})
	if err != nil {
		return experiment.RunSummary{}, fmt.Errorf("create experiment Observer: %w", err)
	}

	observerContext, cancelObserver := context.WithCancel(ctx)
	group, groupContext := errgroup.WithContext(observerContext)
	group.Go(func() error { return measurementObserver.Run(groupContext) })
	observerRunning := true
	stopObserver := func() error {
		if !observerRunning {
			return nil
		}
		observerRunning = false
		cancelObserver()
		return group.Wait()
	}
	defer func() {
		returnErr = errors.Join(returnErr, stopObserver())
	}()

	initial, spec, err := waitForSpecsAndHeads(ctx, groupContext, datasetRecorder, config.Targets)
	if err != nil {
		return experiment.RunSummary{}, errors.Join(err, stopObserver())
	}
	boundaryTimeout, err := runtimeDuration(spec, 2*spec.SlotsPerEpoch)
	if err != nil {
		return experiment.RunSummary{}, err
	}
	boundaryContext, cancelBoundary := context.WithTimeout(ctx, boundaryTimeout)
	defer cancelBoundary()

	var partition fault.PartitionRequest
	var startSlot uint64
	faultApplied := false
	recoveryWindowComplete := true
	if condition == experiment.ConditionFault {
		preBoundarySnapshot, err := waitForCommonSlot(boundaryContext, groupContext, datasetRecorder, config.Targets, func(slot uint64) bool {
			return slot%spec.SlotsPerEpoch == spec.SlotsPerEpoch-1
		})
		if err != nil {
			return experiment.RunSummary{}, fmt.Errorf("wait for pre-fault epoch boundary: %w", err)
		}
		deadmanSlots, overflow := multiply(config.Scenario.Spec.Fault.DurationEpochs+2, spec.SlotsPerEpoch)
		if overflow {
			return experiment.RunSummary{}, errors.New("fault deadman slot count overflows uint64")
		}
		deadmanTTL, err := runtimeDuration(spec, deadmanSlots)
		if err != nil {
			return experiment.RunSummary{}, err
		}
		partition = partitionRequest(config, deadmanTTL)
		if err := config.FaultBackend.Apply(ctx, partition); err != nil {
			return experiment.RunSummary{}, fmt.Errorf("apply CL P2P partition: %w", err)
		}
		faultApplied = true
		defer func() {
			if !faultApplied {
				return
			}
			cleanupContext, cancelCleanup := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancelCleanup()
			returnErr = errors.Join(returnErr, config.FaultBackend.Revert(cleanupContext, partition))
		}()
		preBoundary, _ := commonLatestSlot(preBoundarySnapshot, targetNames(config.Targets))
		if latestSlotExceeds(datasetRecorder.Snapshot(), targetNames(config.Targets), preBoundary) {
			return experiment.RunSummary{}, errors.New("fault injection completed after the committed epoch boundary; run is invalid")
		}
		if preBoundary == math.MaxUint64 {
			return experiment.RunSummary{}, errors.New("next epoch boundary slot overflows uint64")
		}
		nextBoundary := preBoundary + 1
		initial, err = waitForExactSlot(boundaryContext, groupContext, datasetRecorder, config.Targets, nextBoundary)
		if err != nil {
			return experiment.RunSummary{}, fmt.Errorf("observe injected epoch boundary: %w", err)
		}
		startSlot = nextBoundary
	} else {
		initial, err = waitForCommonSlot(boundaryContext, groupContext, datasetRecorder, config.Targets, func(slot uint64) bool {
			return slot%spec.SlotsPerEpoch == 0
		})
		if err != nil {
			return experiment.RunSummary{}, fmt.Errorf("wait for control epoch boundary: %w", err)
		}
		startSlot, _ = commonLatestSlot(initial, targetNames(config.Targets))
	}
	cancelBoundary()
	durationSlots, overflow := multiply(config.Scenario.Spec.Fault.DurationEpochs, spec.SlotsPerEpoch)
	if overflow || math.MaxUint64-startSlot < durationSlots {
		return experiment.RunSummary{}, errors.New("measurement window slot calculation overflows uint64")
	}
	endSlot := startSlot + durationSlots
	windowTimeout, err := runtimeDuration(spec, durationSlots+spec.SlotsPerEpoch)
	if err != nil {
		return experiment.RunSummary{}, err
	}
	windowContext, cancelWindow := context.WithTimeout(ctx, windowTimeout)
	ending, err := waitForExactSlot(windowContext, groupContext, datasetRecorder, config.Targets, endSlot)
	cancelWindow()
	if err != nil {
		return experiment.RunSummary{}, fmt.Errorf("observe complete primary window: %w", err)
	}

	removedAt := time.Time{}
	removalSlot := endSlot
	if condition == experiment.ConditionFault {
		if err := config.FaultBackend.Revert(ctx, partition); err != nil {
			return experiment.RunSummary{}, fmt.Errorf("remove CL P2P partition: %w", err)
		}
		faultApplied = false
		removedAt = config.Now().UTC()
		postRemoval := datasetRecorder.Snapshot()
		if latest := latestByTarget(postRemoval.Beacon); len(latest) == len(config.Targets) {
			for _, observation := range latest {
				if observation.CurrentSlot > removalSlot {
					removalSlot = observation.CurrentSlot
				}
			}
		}
		recoverySlots, overflow := multiply(config.Scenario.Spec.Methodology.PostFaultObservationEpochs, spec.SlotsPerEpoch)
		if overflow || math.MaxUint64-removalSlot < recoverySlots {
			return experiment.RunSummary{}, errors.New("recovery window slot calculation overflows uint64")
		}
		recoveryTimeout, err := runtimeDuration(spec, recoverySlots+spec.SlotsPerEpoch)
		if err != nil {
			return experiment.RunSummary{}, err
		}
		recoveryContext, cancelRecovery := context.WithTimeout(ctx, recoveryTimeout)
		recoveryErr := waitForRecoveryOrCensor(recoveryContext, groupContext, datasetRecorder, config.Targets, ending, endSlot, removalSlot+recoverySlots)
		cancelRecovery()
		if recoveryErr != nil {
			if !errors.Is(recoveryErr, context.DeadlineExceeded) {
				return experiment.RunSummary{}, fmt.Errorf("observe recovery window: %w", recoveryErr)
			}
			recoveryWindowComplete = false
		}
	}

	if err := stopObserver(); err != nil {
		return experiment.RunSummary{}, fmt.Errorf("stop experiment Observer: %w", err)
	}
	if err := closeRaw(); err != nil {
		return experiment.RunSummary{}, fmt.Errorf("close raw time series: %w", err)
	}
	finalDataset := datasetRecorder.Snapshot()
	summary := summarizeRun(config, condition, repetition, spec, realizedSplit, initial, ending, finalDataset, startSlot, endSlot, removedAt, removalSlot, recoveryWindowComplete)
	if err := config.Artifacts.WriteJSON("manifest.json", summary); err != nil {
		return experiment.RunSummary{}, err
	}
	metadata := experiment.RunMetadata{
		SchemaVersion:  experiment.MetadataSchemaVersion,
		RunID:          config.RunID,
		ScenarioSHA256: sha256Bytes(config.ScenarioYAML),
		ChainID:        chainID,
		RuntimeSpecs:   cloneSpecs(finalDataset.Specs),
		RuntimeGenesis: cloneGenesis(finalDataset.Genesis),
		Placements:     append([]topology.Placement(nil), config.Placements...),
		Dependencies:   config.Dependencies,
		OrderSeed:      config.Scenario.Spec.Methodology.OrderSeed,
		RunOrder:       append([]string(nil), config.Scenario.Spec.Methodology.RunOrder...),
	}
	if condition == experiment.ConditionFault {
		metadata.FaultDeadmanSeconds = uint64(partition.TTL / time.Second)
	}
	if err := config.Artifacts.WriteJSON("metadata.json", metadata); err != nil {
		return experiment.RunSummary{}, err
	}
	checksums, err := artifactChecksums(config.Artifacts.Path(), []string{
		"manifest.json", "metadata.json", "raw-timeseries.jsonl", "realized-split.json", "scenario.yaml",
	})
	if err != nil {
		return experiment.RunSummary{}, err
	}
	if err := config.Artifacts.WriteJSON("checksums.json", checksums); err != nil {
		return experiment.RunSummary{}, err
	}
	return summary, nil
}

func validateConfig(config Config) (experiment.Condition, uint64, error) {
	if err := config.Scenario.Validate(); err != nil {
		return "", 0, fmt.Errorf("validate scenario: %w", err)
	}
	condition, repetition, found := runIdentity(config.RunID, config.Scenario.Spec.Methodology.RunOrder)
	if !found {
		return "", 0, fmt.Errorf("run ID %q is not in the committed run order", config.RunID)
	}
	if len(config.ScenarioYAML) == 0 || len(config.Targets) != 4 || config.ValidatorClient == nil || config.ChainIDClient == nil || config.Artifacts == nil {
		return "", 0, errors.New("scenario bytes, four targets, validator and chain clients, and artifact directory are required")
	}
	if config.PollInterval <= 0 || config.HeadHistoryLimit < 2 {
		return "", 0, errors.New("positive poll interval and head history limit of at least two are required")
	}
	expectedTargets := make(map[string]struct{}, len(config.Scenario.Spec.Topology.Participants))
	for _, participant := range config.Scenario.Spec.Topology.Participants {
		expectedTargets[participant.BeaconTarget] = struct{}{}
	}
	seenTargets := make(map[string]struct{}, len(config.Targets))
	for _, target := range config.Targets {
		if _, expected := expectedTargets[target.Name]; !expected {
			return "", 0, fmt.Errorf("Observer target %q is not in scenario topology", target.Name)
		}
		if _, duplicate := seenTargets[target.Name]; duplicate {
			return "", 0, fmt.Errorf("Observer target %q is duplicated", target.Name)
		}
		seenTargets[target.Name] = struct{}{}
	}
	if condition == experiment.ConditionFault && config.FaultBackend == nil {
		return "", 0, errors.New("fault run requires a fault backend")
	}
	expectedNamespace := config.Scenario.Spec.Target.NamespacePrefix + config.RunID
	if config.Namespace != expectedNamespace {
		return "", 0, fmt.Errorf("namespace %q does not match exact run namespace %q", config.Namespace, expectedNamespace)
	}
	if err := topology.ValidatePlacements(config.Scenario, config.Placements); err != nil {
		return "", 0, err
	}
	if err := experiment.ValidateDependencyMetadata(config.Dependencies); err != nil {
		return "", 0, fmt.Errorf("validate dependency metadata: %w", err)
	}
	return condition, repetition, nil
}

func partitionRequest(config Config, ttl time.Duration) fault.PartitionRequest {
	podByParticipant := make(map[string]string, len(config.Placements))
	for _, placement := range config.Placements {
		podByParticipant[placement.ParticipantID] = placement.Pod
	}
	groups := config.Scenario.Spec.Fault.Groups
	request := fault.PartitionRequest{
		RunID:           config.RunID,
		Namespace:       config.Namespace,
		NamespacePrefix: config.Scenario.Spec.Target.NamespacePrefix,
		TTL:             ttl,
	}
	for _, participantID := range groups[0].Participants {
		request.GroupA = append(request.GroupA, podByParticipant[participantID])
	}
	for _, participantID := range groups[1].Participants {
		request.GroupB = append(request.GroupB, podByParticipant[participantID])
	}
	return request
}

func latestSlotExceeds(snapshot observer.DatasetSnapshot, targets []string, maximum uint64) bool {
	latest := latestByTarget(snapshot.Beacon)
	for _, target := range targets {
		if observation, exists := latest[target]; exists && observation.CurrentSlot > maximum {
			return true
		}
	}
	return false
}

func waitForSpecsAndHeads(ctx, observerContext context.Context, recorder *observer.DatasetRecorder, targets []observer.Target) (observer.DatasetSnapshot, beacon.Spec, error) {
	names := targetNames(targets)
	var result observer.DatasetSnapshot
	err := waitFor(ctx, observerContext, recorder, func(snapshot observer.DatasetSnapshot) bool {
		if len(snapshot.Specs) != len(names) || len(latestByTarget(snapshot.Beacon)) != len(names) {
			return false
		}
		result = snapshot
		return true
	})
	if err != nil {
		return observer.DatasetSnapshot{}, beacon.Spec{}, err
	}
	spec := result.Specs[names[0]]
	return result, spec, nil
}

func waitForCommonSlot(ctx, observerContext context.Context, recorder *observer.DatasetRecorder, targets []observer.Target, slotRule func(uint64) bool) (observer.DatasetSnapshot, error) {
	names := targetNames(targets)
	var result observer.DatasetSnapshot
	err := waitFor(ctx, observerContext, recorder, func(snapshot observer.DatasetSnapshot) bool {
		slot, ok := commonLatestSlot(snapshot, names)
		if !ok || !slotRule(slot) {
			return false
		}
		result = snapshot
		return true
	})
	return result, err
}

func waitForExactSlot(ctx, observerContext context.Context, recorder *observer.DatasetRecorder, targets []observer.Target, slot uint64) (observer.DatasetSnapshot, error) {
	names := targetNames(targets)
	var result observer.DatasetSnapshot
	err := waitFor(ctx, observerContext, recorder, func(snapshot observer.DatasetSnapshot) bool {
		for _, name := range names {
			if _, exists := observationAtSlot(snapshot.Beacon, name, slot); !exists {
				return false
			}
		}
		result = snapshot
		return true
	})
	return result, err
}

func waitForRecoveryOrCensor(ctx, observerContext context.Context, recorder *observer.DatasetRecorder, targets []observer.Target, ending observer.DatasetSnapshot, endSlot, censorSlot uint64) error {
	endByTarget := observationsAtSlotMap(ending.Beacon, targetNames(targets), endSlot)
	return waitFor(ctx, observerContext, recorder, func(snapshot observer.DatasetSnapshot) bool {
		latest := latestByTarget(snapshot.Beacon)
		allRecovered := len(latest) == len(targets)
		allCensored := len(latest) == len(targets)
		for _, target := range targets {
			observation, exists := latest[target.Name]
			if !exists {
				return false
			}
			allRecovered = allRecovered && observation.Finality.Epoch > endByTarget[target.Name].Finality.Epoch
			allCensored = allCensored && observation.CurrentSlot >= censorSlot
		}
		return allRecovered || allCensored
	})
}

func waitFor(ctx, observerContext context.Context, recorder *observer.DatasetRecorder, predicate func(observer.DatasetSnapshot) bool) error {
	for {
		if predicate(recorder.Snapshot()) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-observerContext.Done():
			return errors.New("Observer stopped before the measurement condition completed")
		case <-recorder.Changed():
		}
	}
}

func summarizeRun(config Config, condition experiment.Condition, repetition uint64, spec beacon.Spec, split topology.RealizedSplit, initial, ending, dataset observer.DatasetSnapshot, startSlot, endSlot uint64, removedAt time.Time, removalSlot uint64, recoveryWindowComplete bool) experiment.RunSummary {
	names := targetNames(config.Targets)
	startByTarget := observationsAtSlotMap(initial.Beacon, names, startSlot)
	endByTarget := observationsAtSlotMap(ending.Beacon, names, endSlot)
	groups := make(map[string]string, len(config.Scenario.Spec.Topology.Participants))
	clients := make(map[string]string, len(config.Scenario.Spec.Topology.Participants))
	for _, group := range config.Scenario.Spec.Fault.Groups {
		for _, participantID := range group.Participants {
			groups[participantID] = group.Name
		}
	}
	for _, participant := range config.Scenario.Spec.Topology.Participants {
		clients[participant.BeaconTarget] = participant.CLClient
		for _, placement := range config.Placements {
			if placement.ParticipantID == participant.ID {
				groups[participant.BeaconTarget] = groups[participant.ID]
			}
		}
	}
	divergenceExceeded := headDivergenceExceeded(dataset.Beacon, names, startSlot, endSlot, config.Scenario.Spec.Thresholds.HeadDivergenceToleranceSlots)
	primaryGaps := primaryMeasurementGaps(dataset, startSlot, endSlot, startByTarget, endByTarget, names)
	targetResults := make([]experiment.TargetResult, 0, len(names))
	for _, name := range names {
		start := startByTarget[name]
		end := endByTarget[name]
		progress, progressErr := finalizedEpochProgress(start.Finality.Epoch, end.Finality.Epoch)
		if progressErr != nil {
			primaryGaps = append(primaryGaps, fmt.Sprintf("target %s: %s", name, progressErr))
		}
		var recovery *uint64
		if condition == experiment.ConditionFault {
			for _, observation := range dataset.Beacon {
				if observation.Target != name || observation.Timestamp.Before(removedAt) || observation.Finality.Epoch <= end.Finality.Epoch || observation.CurrentSlot < removalSlot {
					continue
				}
				value := observation.CurrentSlot - removalSlot
				recovery = &value
				break
			}
		}
		targetResults = append(targetResults, experiment.TargetResult{
			Target:                 name,
			CLClient:               clients[name],
			Group:                  groups[name],
			StartFinalizedEpoch:    start.Finality.Epoch,
			EndFinalizedEpoch:      end.Finality.Epoch,
			FinalizedEpochProgress: progress,
			RecoverySlots:          recovery,
			DivergenceExceeded:     divergenceExceeded,
		})
	}
	recoveryGaps := []string(nil)
	if condition == experiment.ConditionFault {
		recoveryGaps = recoveryMeasurementGaps(dataset, removedAt, recoveryWindowComplete)
	}
	gaps := append(primaryGaps, recoveryGaps...)
	sort.Strings(gaps)
	startTime := latestTimestamp(startByTarget)
	endTime := latestTimestamp(endByTarget)
	return experiment.RunSummary{
		SchemaVersion:               experiment.RunSchemaVersion,
		RunID:                       config.RunID,
		Condition:                   condition,
		Repetition:                  repetition,
		Window:                      experiment.Window{StartedAt: startTime, EndedAt: endTime, StartSlot: startSlot, EndSlot: endSlot},
		PrimaryMeasurementComplete:  len(primaryGaps) == 0,
		RecoveryMeasurementComplete: len(recoveryGaps) == 0,
		MeasurementGaps:             gaps,
		Targets:                     targetResults,
		RealizedSplit:               split,
		Dependencies:                config.Dependencies,
		Confounders: experiment.Confounders{
			ResourcesEqual:          true,
			NodePlacementControlled: true,
			DependencyClosurePinned: true,
		},
	}
}

func finalizedEpochProgress(start, end uint64) (uint64, error) {
	if end < start {
		return 0, fmt.Errorf("finalized epoch regressed from %d to %d", start, end)
	}
	return end - start, nil
}

func recoveryMeasurementGaps(dataset observer.DatasetSnapshot, removedAt time.Time, windowComplete bool) []string {
	gaps := pollGaps(dataset.Gaps, removedAt, latestDatasetTimestamp(dataset))
	if !windowComplete {
		gaps = append(gaps, "recovery observation window did not reach recovery or the preregistered censor slot")
	}
	return gaps
}

func primaryMeasurementGaps(dataset observer.DatasetSnapshot, startSlot, endSlot uint64, starts, ends map[string]observer.BeaconObservation, targets []string) []string {
	startTime := latestTimestamp(starts)
	endTime := latestTimestamp(ends)
	gaps := pollGaps(dataset.Gaps, startTime, endTime)
	for slot := startSlot; ; slot++ {
		for _, target := range targets {
			if _, exists := observationAtSlot(dataset.Beacon, target, slot); !exists {
				gaps = append(gaps, fmt.Sprintf("missing Beacon observation for target %s at current slot %d", target, slot))
			}
		}
		if slot == endSlot {
			break
		}
	}
	sort.Strings(gaps)
	return gaps
}

func pollGaps(input []observer.PollFailure, start, end time.Time) []string {
	gaps := make([]string, 0)
	for _, gap := range input {
		if !gap.Timestamp.Before(start) && !gap.Timestamp.After(end) {
			gaps = append(gaps, fmt.Sprintf("%s %s at %s", gap.Target, gap.Operation, gap.Timestamp.Format(time.RFC3339Nano)))
		}
	}
	return gaps
}

func latestDatasetTimestamp(dataset observer.DatasetSnapshot) time.Time {
	var result time.Time
	for _, observation := range dataset.Beacon {
		if observation.Timestamp.After(result) {
			result = observation.Timestamp
		}
	}
	for _, gap := range dataset.Gaps {
		if gap.Timestamp.After(result) {
			result = gap.Timestamp
		}
	}
	return result
}

func headDivergenceExceeded(observations []observer.BeaconObservation, targets []string, startSlot, endSlot, tolerance uint64) bool {
	var consecutive uint64
	for slot := startSlot; ; slot++ {
		if headsDisagreeAtCurrentSlot(observations, targets, slot) {
			consecutive++
			if consecutive > tolerance {
				return true
			}
		} else {
			consecutive = 0
		}
		if slot == endSlot {
			break
		}
	}
	return false
}

func headsDisagreeAtCurrentSlot(observations []observer.BeaconObservation, targets []string, slot uint64) bool {
	var reference beacon.Head
	for index, target := range targets {
		observation, exists := observationAtSlot(observations, target, slot)
		if !exists {
			return false
		}
		if index == 0 {
			reference = observation.Head
			continue
		}
		if observation.Head.Slot != reference.Slot || observation.Head.Root != reference.Root {
			return true
		}
	}
	return false
}

func commonLatestSlot(snapshot observer.DatasetSnapshot, targets []string) (uint64, bool) {
	latest := latestByTarget(snapshot.Beacon)
	if len(latest) != len(targets) {
		return 0, false
	}
	slot := latest[targets[0]].CurrentSlot
	for _, target := range targets[1:] {
		if latest[target].CurrentSlot != slot {
			return 0, false
		}
	}
	return slot, true
}

func latestByTarget(observations []observer.BeaconObservation) map[string]observer.BeaconObservation {
	result := make(map[string]observer.BeaconObservation)
	for _, observation := range observations {
		current, exists := result[observation.Target]
		if !exists || observation.Timestamp.After(current.Timestamp) {
			result[observation.Target] = observation
		}
	}
	return result
}

func observationAtSlot(observations []observer.BeaconObservation, target string, slot uint64) (observer.BeaconObservation, bool) {
	var result observer.BeaconObservation
	found := false
	for _, observation := range observations {
		if observation.Target != target || observation.CurrentSlot != slot {
			continue
		}
		if !found || observation.Timestamp.After(result.Timestamp) {
			result = observation
			found = true
		}
	}
	return result, found
}

func observationsAtSlotMap(observations []observer.BeaconObservation, targets []string, slot uint64) map[string]observer.BeaconObservation {
	result := make(map[string]observer.BeaconObservation, len(targets))
	for _, target := range targets {
		result[target], _ = observationAtSlot(observations, target, slot)
	}
	return result
}

func targetNames(targets []observer.Target) []string {
	names := make([]string, 0, len(targets))
	for _, target := range targets {
		names = append(names, target.Name)
	}
	sort.Strings(names)
	return names
}

func latestTimestamp(observations map[string]observer.BeaconObservation) time.Time {
	var result time.Time
	for _, observation := range observations {
		if observation.Timestamp.After(result) {
			result = observation.Timestamp
		}
	}
	return result.UTC()
}

func runIdentity(runID string, order []string) (experiment.Condition, uint64, bool) {
	found := false
	for _, candidate := range order {
		found = found || candidate == runID
	}
	conditionText, repetitionText, separated := strings.Cut(runID, "-")
	if !found || !separated || len(repetitionText) != 1 || repetitionText[0] < '1' || repetitionText[0] > '3' {
		return "", 0, false
	}
	condition := experiment.Condition(conditionText)
	if condition != experiment.ConditionControl && condition != experiment.ConditionFault {
		return "", 0, false
	}
	return condition, uint64(repetitionText[0] - '0'), true
}

func runtimeDuration(spec beacon.Spec, slots uint64) (time.Duration, error) {
	seconds, overflow := multiply(spec.SecondsPerSlot, slots)
	if overflow || seconds > uint64(math.MaxInt64/int64(time.Second)) {
		return 0, errors.New("runtime-derived duration overflows time.Duration")
	}
	return time.Duration(seconds) * time.Second, nil
}

func multiply(left, right uint64) (uint64, bool) {
	if left != 0 && right > math.MaxUint64/left {
		return 0, true
	}
	return left * right, false
}

func sha256Bytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func cloneSpecs(input map[string]beacon.Spec) map[string]beacon.Spec {
	output := make(map[string]beacon.Spec, len(input))
	for target, spec := range input {
		forks := make(map[string]string, len(spec.ForkEpochs))
		for name, epoch := range spec.ForkEpochs {
			forks[name] = epoch
		}
		spec.ForkEpochs = forks
		output[target] = spec
	}
	return output
}

func cloneGenesis(input map[string]beacon.Genesis) map[string]beacon.Genesis {
	output := make(map[string]beacon.Genesis, len(input))
	for target, genesis := range input {
		output[target] = genesis
	}
	return output
}

func artifactChecksums(root string, names []string) (map[string]string, error) {
	result := make(map[string]string, len(names))
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return nil, fmt.Errorf("read artifact %q for checksum: %w", name, err)
		}
		result[name] = sha256Bytes(data)
	}
	return result, nil
}
