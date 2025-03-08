package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// PrometheusMetrics holds all the Prometheus metrics for the application
type PrometheusMetrics struct {
	Registry *prometheus.Registry

	// Home Assistant metrics
	HassConnectionStatus  prometheus.Gauge
	HassEventsReceived    *prometheus.CounterVec
	HassReconnectAttempts prometheus.Counter

	// ClickHouse metrics
	InsertCounter      *prometheus.CounterVec
	BufferedCounter    prometheus.Counter
	BufferUtilization  prometheus.Gauge
	InsertLatency      *prometheus.HistogramVec
	InsertFailures     *prometheus.CounterVec
	BufferDrainCounter prometheus.Counter
}

// NewPrometheusMetrics creates a new PrometheusMetrics instance
func NewPrometheusMetrics() *PrometheusMetrics {
	registry := prometheus.NewRegistry()

	// Register default Go metrics
	registry.MustRegister(prometheus.NewGoCollector())
	registry.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))

	m := &PrometheusMetrics{
		Registry: registry,

		// Home Assistant metrics
		HassConnectionStatus: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "hass2ch_hass_connection_status",
			Help: "Current connection status to Home Assistant (1=connected, 0=disconnected)",
		}),
		HassEventsReceived: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "hass2ch_hass_events_received_total",
				Help: "Total number of events received from Home Assistant",
			},
			[]string{"event_type"},
		),
		HassReconnectAttempts: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "hass2ch_hass_reconnect_attempts_total",
				Help: "Total number of reconnection attempts to Home Assistant",
			},
		),

		// ClickHouse metrics
		InsertCounter: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "hass2ch_clickhouse_inserts_total",
				Help: "Total number of inserts to ClickHouse",
			},
			[]string{"success"},
		),
		BufferedCounter: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "hass2ch_clickhouse_buffered_states_total",
				Help: "Total number of states buffered due to insert failures",
			},
		),
		BufferUtilization: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "hass2ch_clickhouse_buffer_utilization",
				Help: "Current buffer utilization (0-1)",
			},
		),
		InsertLatency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "hass2ch_clickhouse_insert_duration_seconds",
				Help:    "Duration of insert operations",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"success"},
		),
		InsertFailures: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "hass2ch_clickhouse_insert_failures_total",
				Help: "Total number of insert failures by error type",
			},
			[]string{"error_type"},
		),
		BufferDrainCounter: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "hass2ch_clickhouse_buffer_drain_total",
				Help: "Total number of states drained from buffer",
			},
		),
	}

	// Register all metrics
	registry.MustRegister(
		m.HassConnectionStatus,
		m.HassEventsReceived,
		m.HassReconnectAttempts,
		m.InsertCounter,
		m.BufferedCounter,
		m.BufferUtilization,
		m.InsertLatency,
		m.InsertFailures,
		m.BufferDrainCounter,
	)

	return m
}

// Handler returns an HTTP handler for the metrics endpoint
func (m *PrometheusMetrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})
}
