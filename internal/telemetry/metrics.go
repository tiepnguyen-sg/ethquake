// Package telemetry exposes Observer measurements and self-health metrics.
package telemetry

import (
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/tiepnguyen-sg/ethquake/internal/observer"
)

type Metrics struct {
	registry             *prometheus.Registry
	headSlot             *prometheus.GaugeVec
	finalizedEpoch       *prometheus.GaugeVec
	finalityLagSlots     *prometheus.GaugeVec
	beaconHeadAgreement  *optionalGauge
	beaconComparisonSlot *optionalGauge
	beaconComparisons    *prometheus.CounterVec
	beaconOptimistic     *prometheus.GaugeVec
	secondsPerSlot       *prometheus.GaugeVec
	slotsPerEpoch        *prometheus.GaugeVec
	targetUp             *prometheus.GaugeVec
	lastSuccessTimestamp *prometheus.GaugeVec
	beaconPolls          *prometheus.CounterVec
	beaconPollDuration   *prometheus.HistogramVec
	errors               *prometheus.CounterVec
	executionHeadNumber  *prometheus.GaugeVec
	executionContinuity  *prometheus.GaugeVec
	executionHeads       *prometheus.CounterVec
	executionReorgs      *prometheus.CounterVec
	executionReorgDepth  *prometheus.GaugeVec
}

func NewMetrics() (*Metrics, error) {
	metrics := &Metrics{
		registry: prometheus.NewRegistry(),
		headSlot: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "ethquake",
			Subsystem: "observer",
			Name:      "beacon_head_slot",
			Help:      "Most recently observed Beacon head slot, absent after a failed poll.",
		}, []string{"target"}),
		finalizedEpoch: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "ethquake",
			Subsystem: "observer",
			Name:      "beacon_finalized_epoch",
			Help:      "Most recently observed finalized epoch, absent after a failed poll.",
		}, []string{"target"}),
		finalityLagSlots: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "ethquake",
			Subsystem: "observer",
			Name:      "finality_lag_slots",
			Help:      "Head slot minus the first slot of the finalized epoch, absent after a failed poll.",
		}, []string{"target"}),
		beaconHeadAgreement: newOptionalGauge(
			"ethquake",
			"observer",
			"beacon_head_agreement",
			"Whether all targets reported the same block root at the latest compared slot.",
		),
		beaconComparisonSlot: newOptionalGauge(
			"ethquake",
			"observer",
			"beacon_head_comparison_slot",
			"Latest slot for which all Beacon targets reported a block root.",
		),
		beaconComparisons: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "ethquake",
			Subsystem: "observer",
			Name:      "beacon_head_comparisons_total",
			Help:      "Complete same-slot head comparisons partitioned by agreement result.",
		}, []string{"result"}),
		beaconOptimistic: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "ethquake",
			Subsystem: "observer",
			Name:      "beacon_execution_optimistic",
			Help:      "Whether a Beacon response reports execution_optimistic, partitioned by source.",
		}, []string{"source", "target"}),
		secondsPerSlot: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "ethquake",
			Subsystem: "observer",
			Name:      "runtime_seconds_per_slot",
			Help:      "SECONDS_PER_SLOT read from the target Beacon API at runtime.",
		}, []string{"target"}),
		slotsPerEpoch: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "ethquake",
			Subsystem: "observer",
			Name:      "runtime_slots_per_epoch",
			Help:      "SLOTS_PER_EPOCH read from the target Beacon API at runtime.",
		}, []string{"target"}),
		targetUp: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "ethquake",
			Subsystem: "observer",
			Name:      "target_up",
			Help:      "Whether the most recent target poll produced a complete valid observation.",
		}, []string{"protocol", "target"}),
		lastSuccessTimestamp: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "ethquake",
			Subsystem: "observer",
			Name:      "last_success_timestamp_seconds",
			Help:      "Unix timestamp of the most recent complete valid target observation.",
		}, []string{"protocol", "target"}),
		beaconPolls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "ethquake",
			Subsystem: "observer",
			Name:      "beacon_polls_total",
			Help:      "Beacon target polls partitioned by success or error result.",
		}, []string{"target", "result"}),
		beaconPollDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "ethquake",
			Subsystem: "observer",
			Name:      "beacon_poll_duration_seconds",
			Help:      "Duration of Beacon target polls.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"target"}),
		errors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "ethquake",
			Subsystem: "observer",
			Name:      "errors_total",
			Help:      "Measurement errors partitioned by protocol and operation.",
		}, []string{"protocol", "operation", "target"}),
		executionHeadNumber: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "ethquake",
			Subsystem: "observer",
			Name:      "execution_head_number",
			Help:      "Most recently observed execution head number, absent after subscription failure.",
		}, []string{"target"}),
		executionContinuity: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "ethquake",
			Subsystem: "observer",
			Name:      "execution_continuity",
			Help:      "Whether reorg continuity is established at the most recent execution head.",
		}, []string{"target"}),
		executionHeads: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "ethquake",
			Subsystem: "observer",
			Name:      "execution_heads_total",
			Help:      "Validated newHeads notifications received from an execution target.",
		}, []string{"target"}),
		executionReorgs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "ethquake",
			Subsystem: "observer",
			Name:      "execution_reorgs_total",
			Help:      "Execution reorgs detected from parent-hash continuity.",
		}, []string{"target"}),
		executionReorgDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "ethquake",
			Subsystem: "observer",
			Name:      "execution_reorg_depth",
			Help:      "Depth of the most recent reorg when known; absent when depth is unknown.",
		}, []string{"target"}),
	}

	collectors := []prometheus.Collector{
		metrics.headSlot,
		metrics.finalizedEpoch,
		metrics.finalityLagSlots,
		metrics.beaconHeadAgreement,
		metrics.beaconComparisonSlot,
		metrics.beaconComparisons,
		metrics.beaconOptimistic,
		metrics.secondsPerSlot,
		metrics.slotsPerEpoch,
		metrics.targetUp,
		metrics.lastSuccessTimestamp,
		metrics.beaconPolls,
		metrics.beaconPollDuration,
		metrics.errors,
		metrics.executionHeadNumber,
		metrics.executionContinuity,
		metrics.executionHeads,
		metrics.executionReorgs,
		metrics.executionReorgDepth,
	}
	for _, collector := range collectors {
		if err := metrics.registry.Register(collector); err != nil {
			return nil, fmt.Errorf("register Observer metric: %w", err)
		}
	}
	return metrics, nil
}

func (m *Metrics) RecordSpec(observation observer.SpecObservation) error {
	m.secondsPerSlot.WithLabelValues(observation.Target).Set(float64(observation.Spec.SecondsPerSlot))
	m.slotsPerEpoch.WithLabelValues(observation.Target).Set(float64(observation.Spec.SlotsPerEpoch))
	return nil
}

func (m *Metrics) RecordBeacon(observation observer.BeaconObservation) error {
	m.headSlot.WithLabelValues(observation.Target).Set(float64(observation.Head.Slot))
	m.finalizedEpoch.WithLabelValues(observation.Target).Set(float64(observation.Finality.Epoch))
	m.finalityLagSlots.WithLabelValues(observation.Target).Set(float64(observation.FinalityLagSlots))
	headOptimistic := 0.0
	if observation.Head.ExecutionOptimistic {
		headOptimistic = 1
	}
	finalityOptimistic := 0.0
	if observation.Finality.ExecutionOptimistic {
		finalityOptimistic = 1
	}
	m.beaconOptimistic.WithLabelValues("head", observation.Target).Set(headOptimistic)
	m.beaconOptimistic.WithLabelValues("finality", observation.Target).Set(finalityOptimistic)
	m.targetUp.WithLabelValues("beacon", observation.Target).Set(1)
	m.lastSuccessTimestamp.WithLabelValues("beacon", observation.Target).Set(float64(observation.Timestamp.Unix()))
	m.beaconPolls.WithLabelValues(observation.Target, "success").Inc()
	m.beaconPollDuration.WithLabelValues(observation.Target).Observe(observation.PollDuration.Seconds())
	return nil
}

func (m *Metrics) RecordHeadComparison(comparison observer.HeadComparison) error {
	agreement := 0.0
	result := "mismatch"
	if comparison.Agreement {
		agreement = 1
		result = "agreement"
	}
	m.beaconHeadAgreement.Set(agreement)
	m.beaconComparisonSlot.Set(float64(comparison.Slot))
	m.beaconComparisons.WithLabelValues(result).Inc()
	return nil
}

func (m *Metrics) RecordPollFailure(failure observer.PollFailure) error {
	m.errors.WithLabelValues(failure.Protocol, failure.Operation, failure.Target).Inc()
	switch failure.Protocol {
	case "beacon":
		m.headSlot.DeleteLabelValues(failure.Target)
		m.finalizedEpoch.DeleteLabelValues(failure.Target)
		m.finalityLagSlots.DeleteLabelValues(failure.Target)
		m.beaconOptimistic.DeleteLabelValues("head", failure.Target)
		m.beaconOptimistic.DeleteLabelValues("finality", failure.Target)
		m.beaconHeadAgreement.Clear()
		m.beaconComparisonSlot.Clear()
		m.targetUp.WithLabelValues("beacon", failure.Target).Set(0)
		m.beaconPolls.WithLabelValues(failure.Target, "error").Inc()
		m.beaconPollDuration.WithLabelValues(failure.Target).Observe(failure.PollDuration.Seconds())
	case "execution":
		m.executionContinuity.WithLabelValues(failure.Target).Set(0)
		if failure.Operation == "execution_subscription" {
			m.executionHeadNumber.DeleteLabelValues(failure.Target)
			m.targetUp.WithLabelValues("execution", failure.Target).Set(0)
		}
	}
	return nil
}

func (m *Metrics) RecordExecution(observation observer.ExecutionObservation) error {
	continuity := 0.0
	if observation.Continuity {
		continuity = 1
	}
	m.executionHeadNumber.WithLabelValues(observation.Target).Set(float64(observation.Head.Number))
	m.executionContinuity.WithLabelValues(observation.Target).Set(continuity)
	m.executionHeads.WithLabelValues(observation.Target).Inc()
	m.targetUp.WithLabelValues("execution", observation.Target).Set(1)
	m.lastSuccessTimestamp.WithLabelValues("execution", observation.Target).Set(float64(observation.Timestamp.Unix()))
	return nil
}

func (m *Metrics) RecordReorg(observation observer.ReorgObservation) error {
	m.executionReorgs.WithLabelValues(observation.Target).Inc()
	if observation.Depth == nil {
		m.executionReorgDepth.DeleteLabelValues(observation.Target)
		return nil
	}
	m.executionReorgDepth.WithLabelValues(observation.Target).Set(float64(*observation.Depth))
	return nil
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}
