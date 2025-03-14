package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Pipeline metrics
	EventsReceived = promauto.NewCounter(prometheus.CounterOpts{
		Name: "hass2ch_events_received_total",
		Help: "The total number of events received from Home Assistant",
	})

	EventsFiltered = promauto.NewCounter(prometheus.CounterOpts{
		Name: "hass2ch_events_filtered_total",
		Help: "The total number of events filtered out",
	})

	EventsProcessed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "hass2ch_events_processed_total",
		Help: "The total number of events successfully processed",
	})

	BatchesProcessed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "hass2ch_batches_processed_total",
		Help: "The total number of batches processed",
	})

	BatchSize = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "hass2ch_batch_size",
		Help:    "Histogram of batch sizes",
		Buckets: prometheus.LinearBuckets(10, 100, 10), // 10, 110, 210, ... 910
	})

	DatabaseOperationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hass2ch_database_operations_total",
		Help: "The total number of database operations by type and status",
	}, []string{"operation", "status"})

	BatchProcessingDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "hass2ch_batch_processing_duration_seconds",
		Help:    "Duration of processing a batch of events",
		Buckets: prometheus.DefBuckets,
	})

	// Home Assistant client metrics
	HassConnectionStatus = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "hass2ch_hass_connection_status",
		Help: "Status of the Home Assistant connection (1=connected, 0=disconnected)",
	})

	HassReconnectTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "hass2ch_hass_reconnect_total",
		Help: "Total number of reconnection attempts to Home Assistant",
	})

	// ClickHouse client metrics
	CHConnectionStatus = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "hass2ch_clickhouse_connection_status",
		Help: "Status of the ClickHouse connection (1=connected, 0=disconnected)",
	})

	CHQueryDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "hass2ch_clickhouse_query_duration_seconds",
		Help:    "Duration of ClickHouse queries",
		Buckets: prometheus.DefBuckets,
	}, []string{"query_type"})

	CHRetryAttempts = promauto.NewCounter(prometheus.CounterOpts{
		Name: "hass2ch_clickhouse_retry_attempts_total",
		Help: "Total number of retry attempts for ClickHouse operations",
	})

	CHRetrySuccess = promauto.NewCounter(prometheus.CounterOpts{
		Name: "hass2ch_clickhouse_retry_success_total",
		Help: "Total number of successful retries for ClickHouse operations",
	})
)
