package telemetry

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

type optionalGauge struct {
	description *prometheus.Desc

	mu      sync.Mutex
	present bool
	value   float64
}

func newOptionalGauge(namespace, subsystem, name, help string) *optionalGauge {
	return &optionalGauge{
		description: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, subsystem, name),
			help,
			nil,
			nil,
		),
	}
}

func (g *optionalGauge) Describe(descriptions chan<- *prometheus.Desc) {
	descriptions <- g.description
}

func (g *optionalGauge) Collect(metrics chan<- prometheus.Metric) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.present {
		return
	}
	metrics <- prometheus.MustNewConstMetric(g.description, prometheus.GaugeValue, g.value)
}

func (g *optionalGauge) Set(value float64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.value = value
	g.present = true
}

func (g *optionalGauge) Clear() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.present = false
}
