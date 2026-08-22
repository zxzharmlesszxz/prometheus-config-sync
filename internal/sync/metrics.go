package sync

import (
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	versioncollector "github.com/prometheus/client_golang/prometheus/collectors/version"
)

type Metrics struct {
	healthy atomic.Bool

	syncsTotal        prometheus.Counter
	syncFailuresTotal prometheus.Counter
	syncErrorsTotal   *prometheus.CounterVec
	reloadsTotal      prometheus.Counter
	changesTotal      prometheus.Counter
	healthyGauge      prometheus.Gauge
	lastSuccess       prometheus.Gauge
	lastFailure       prometheus.Gauge
	lastReload        prometheus.Gauge
	lastChange        prometheus.Gauge
	syncDuration      prometheus.Histogram
}

func NewMetrics() *Metrics {
	return &Metrics{
		syncsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "prometheus_config_sync_syncs_total",
			Help: "Total number of sync attempts.",
		}),
		syncFailuresTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "prometheus_config_sync_sync_failures_total",
			Help: "Total number of failed sync attempts.",
		}),
		syncErrorsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "prometheus_config_sync_sync_errors_total",
			Help: "Total number of sync errors by processing stage.",
		}, []string{"stage"}),
		reloadsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "prometheus_config_sync_reloads_total",
			Help: "Total number of Prometheus reloads triggered by this service.",
		}),
		changesTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "prometheus_config_sync_changes_total",
			Help: "Total number of published generated configuration changes.",
		}),
		healthyGauge: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "prometheus_config_sync_healthy",
			Help: "Whether the latest synchronization attempt completed successfully.",
		}),
		lastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "prometheus_config_sync_last_success_timestamp_seconds",
			Help: "Unix timestamp of the last successful sync.",
		}),
		lastFailure: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "prometheus_config_sync_last_failure_timestamp_seconds",
			Help: "Unix timestamp of the last failed sync.",
		}),
		lastReload: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "prometheus_config_sync_last_reload_timestamp_seconds",
			Help: "Unix timestamp of the last Prometheus reload triggered by this service.",
		}),
		lastChange: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "prometheus_config_sync_last_change_timestamp_seconds",
			Help: "Unix timestamp of the last published config change.",
		}),
		syncDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "prometheus_config_sync_sync_duration_seconds",
			Help:    "Duration of sync attempts.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 20, 30, 60, 120, 300},
		}),
	}
}

func (m *Metrics) Registry() *prometheus.Registry {
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(versioncollector.NewCollector("prometheus_config_sync"))
	registry.MustRegister(
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		collectors.NewGoCollector(),
		m.syncsTotal,
		m.syncFailuresTotal,
		m.syncErrorsTotal,
		m.reloadsTotal,
		m.changesTotal,
		m.healthyGauge,
		m.lastSuccess,
		m.lastFailure,
		m.lastReload,
		m.lastChange,
		m.syncDuration,
	)
	return registry
}

func (m *Metrics) MarkHealthy(ok bool) {
	m.healthy.Store(ok)
	if ok {
		m.healthyGauge.Set(1)
		return
	}
	m.healthyGauge.Set(0)
}

func (m *Metrics) ObserveSync(duration time.Duration, err error, failureStage string) {
	m.syncsTotal.Inc()
	m.syncDuration.Observe(duration.Seconds())
	now := float64(time.Now().Unix())
	if err != nil {
		m.syncFailuresTotal.Inc()
		if failureStage == "" {
			failureStage = "unknown"
		}
		m.syncErrorsTotal.WithLabelValues(failureStage).Inc()
		m.lastFailure.Set(now)
		m.MarkHealthy(false)
		return
	}
	m.lastSuccess.Set(now)
	m.MarkHealthy(true)
}

func (m *Metrics) MarkReload() {
	m.reloadsTotal.Inc()
	m.lastReload.Set(float64(time.Now().Unix()))
}

func (m *Metrics) MarkPublishedChange() {
	m.changesTotal.Inc()
	m.lastChange.Set(float64(time.Now().Unix()))
}
