// Package timeseries writes versioned Observer events.
package timeseries

import (
	"encoding/json"
	"errors"
	"io"
	"sync"

	"github.com/tiepnguyen-sg/ethquake/internal/beacon"
	"github.com/tiepnguyen-sg/ethquake/internal/execution"
	"github.com/tiepnguyen-sg/ethquake/internal/observer"
)

const schemaVersion = "ethquake.observer/v1alpha1"

type JSONLRecorder struct {
	mu      sync.Mutex
	encoder *json.Encoder
}

type event struct {
	SchemaVersion    string            `json:"schema_version"`
	Type             string            `json:"type"`
	Timestamp        string            `json:"timestamp"`
	Target           string            `json:"target,omitempty"`
	Protocol         string            `json:"protocol,omitempty"`
	Spec             *beacon.Spec      `json:"spec,omitempty"`
	Genesis          *beacon.Genesis   `json:"genesis,omitempty"`
	CurrentSlot      *uint64           `json:"current_slot,omitempty"`
	Head             *beacon.Head      `json:"head,omitempty"`
	Finality         *beacon.Finality  `json:"finality,omitempty"`
	FinalityLagSlots *uint64           `json:"finality_lag_slots,omitempty"`
	ComparisonSlot   *uint64           `json:"comparison_slot,omitempty"`
	Roots            map[string]string `json:"roots,omitempty"`
	Agreement        *bool             `json:"agreement,omitempty"`
	ExecutionHead    *execution.Head   `json:"execution_head,omitempty"`
	Continuity       *bool             `json:"continuity,omitempty"`
	PreviousHead     *execution.Head   `json:"previous_head,omitempty"`
	NewHead          *execution.Head   `json:"new_head,omitempty"`
	ReorgDepth       *uint64           `json:"reorg_depth,omitempty"`
	Operation        string            `json:"operation,omitempty"`
	Error            string            `json:"error,omitempty"`
	PollDurationMS   *float64          `json:"poll_duration_ms,omitempty"`
}

func NewJSONLRecorder(writer io.Writer) (*JSONLRecorder, error) {
	if writer == nil {
		return nil, errors.New("time-series writer is required")
	}
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return &JSONLRecorder{encoder: encoder}, nil
}

func (r *JSONLRecorder) RecordSpec(observation observer.SpecObservation) error {
	return r.write(event{
		SchemaVersion: schemaVersion,
		Type:          "runtime_spec",
		Timestamp:     observation.Timestamp.Format("2006-01-02T15:04:05.000000000Z07:00"),
		Target:        observation.Target,
		Spec:          &observation.Spec,
		Genesis:       &observation.Genesis,
	})
}

func (r *JSONLRecorder) RecordBeacon(observation observer.BeaconObservation) error {
	pollDurationMS := float64(observation.PollDuration) / float64(1_000_000)
	return r.write(event{
		SchemaVersion:    schemaVersion,
		Type:             "beacon_observation",
		Timestamp:        observation.Timestamp.Format("2006-01-02T15:04:05.000000000Z07:00"),
		Target:           observation.Target,
		CurrentSlot:      &observation.CurrentSlot,
		Head:             &observation.Head,
		Finality:         &observation.Finality,
		FinalityLagSlots: &observation.FinalityLagSlots,
		PollDurationMS:   &pollDurationMS,
	})
}

func (r *JSONLRecorder) RecordHeadComparison(comparison observer.HeadComparison) error {
	return r.write(event{
		SchemaVersion:  schemaVersion,
		Type:           "head_comparison",
		Timestamp:      comparison.Timestamp.Format("2006-01-02T15:04:05.000000000Z07:00"),
		ComparisonSlot: &comparison.Slot,
		Roots:          comparison.Roots,
		Agreement:      &comparison.Agreement,
	})
}

func (r *JSONLRecorder) RecordPollFailure(failure observer.PollFailure) error {
	value := event{
		SchemaVersion: schemaVersion,
		Type:          "measurement_gap",
		Timestamp:     failure.Timestamp.Format("2006-01-02T15:04:05.000000000Z07:00"),
		Target:        failure.Target,
		Protocol:      failure.Protocol,
		Operation:     failure.Operation,
		Error:         failure.Error,
	}
	if failure.Protocol == "beacon" {
		pollDurationMS := float64(failure.PollDuration) / float64(1_000_000)
		value.PollDurationMS = &pollDurationMS
	}
	return r.write(value)
}

func (r *JSONLRecorder) RecordExecution(observation observer.ExecutionObservation) error {
	return r.write(event{
		SchemaVersion: schemaVersion,
		Type:          "execution_head",
		Timestamp:     observation.Timestamp.Format("2006-01-02T15:04:05.000000000Z07:00"),
		Target:        observation.Target,
		ExecutionHead: &observation.Head,
		Continuity:    &observation.Continuity,
	})
}

func (r *JSONLRecorder) RecordReorg(observation observer.ReorgObservation) error {
	value := event{
		SchemaVersion: schemaVersion,
		Type:          "execution_reorg",
		Timestamp:     observation.Timestamp.Format("2006-01-02T15:04:05.000000000Z07:00"),
		Target:        observation.Target,
		PreviousHead:  &observation.PreviousHead,
		NewHead:       &observation.NewHead,
		ReorgDepth:    observation.Depth,
	}
	if observation.Depth == nil {
		return r.writeReorgWithUnknownDepth(value)
	}
	return r.write(value)
}

func (r *JSONLRecorder) writeReorgWithUnknownDepth(value event) error {
	type reorgEvent struct {
		SchemaVersion string          `json:"schema_version"`
		Type          string          `json:"type"`
		Timestamp     string          `json:"timestamp"`
		Target        string          `json:"target"`
		PreviousHead  *execution.Head `json:"previous_head"`
		NewHead       *execution.Head `json:"new_head"`
		ReorgDepth    *uint64         `json:"reorg_depth"`
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.encoder.Encode(reorgEvent{
		SchemaVersion: value.SchemaVersion,
		Type:          value.Type,
		Timestamp:     value.Timestamp,
		Target:        value.Target,
		PreviousHead:  value.PreviousHead,
		NewHead:       value.NewHead,
		ReorgDepth:    nil,
	})
}

func (r *JSONLRecorder) write(value event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.encoder.Encode(value)
}
