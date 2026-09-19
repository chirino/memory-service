package clickhouse

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	processorEvents = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "memory_service_clickhouse_processor_events_total",
		Help: "Source events accepted by the ClickHouse processor.",
	}, []string{"exporter", "kind", "action"})
	processorBatchAttempts = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "memory_service_clickhouse_processor_batch_attempts_total",
		Help: "Frozen ClickHouse batch write attempts.",
	}, []string{"exporter", "result"})
	processorBatchesWritten = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "memory_service_clickhouse_processor_batches_written_total",
		Help: "Frozen ClickHouse batches successfully written.",
	}, []string{"exporter"})
	processorRowsWritten = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "memory_service_clickhouse_processor_rows_written_total",
		Help: "Rows in successfully written ClickHouse batches.",
	}, []string{"exporter", "table"})
	processorLastWrite = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "memory_service_clickhouse_processor_last_write_timestamp_seconds",
		Help: "Unix timestamp of the most recent successfully written ClickHouse batch.",
	}, []string{"exporter"})
	processorLeaseGeneration = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "memory_service_clickhouse_processor_lease_generation",
		Help: "Current checkpoint lease fencing generation.",
	}, []string{"exporter"})
	processorReady = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "memory_service_clickhouse_processor_ready",
		Help: "Whether the processor owns its lease and has an active event subscription.",
	}, []string{"exporter"})
	processorPurges = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "memory_service_clickhouse_processor_purges_total",
		Help: "ClickHouse hard-delete purge attempts by result.",
	}, []string{"exporter", "resource_kind", "result"})
	processorProjectionResults = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "memory_service_clickhouse_processor_projection_results_total",
		Help: "Typed analytics projection results by projection and stable result code.",
	}, []string{"exporter", "projection", "result"})
	processorSourceLag = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "memory_service_clickhouse_processor_source_lag_seconds",
		Help: "Age of the most recently accepted source event.",
	}, []string{"exporter"})
	processorBufferedRows = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "memory_service_clickhouse_processor_buffered_rows",
		Help: "Rows currently held in the frozen batch.",
	}, []string{"exporter"})
	processorBufferedBytes = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "memory_service_clickhouse_processor_buffered_bytes",
		Help: "Estimated encoded bytes currently held in the frozen batch.",
	}, []string{"exporter"})
	processorBootstrapPages = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "memory_service_clickhouse_processor_bootstrap_pages_total",
		Help: "Acknowledged analytics backfill pages by phase.",
	}, []string{"exporter", "phase"})
	processorBootstrapRows = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "memory_service_clickhouse_processor_bootstrap_rows_total",
		Help: "Source records received during analytics backfill by phase.",
	}, []string{"exporter", "phase"})
	processorPurgeDelay = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "memory_service_clickhouse_processor_purge_delay_seconds",
		Help: "Age of the most recently completed hard-delete purge.",
	}, []string{"exporter", "resource_kind"})
)
