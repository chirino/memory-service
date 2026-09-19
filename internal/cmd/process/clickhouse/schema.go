package clickhouse

import "fmt"

// schemaVersion starts at 1 because the ClickHouse exporter has not shipped
// with an earlier schema. Development databases may be reset while the
// initial schema is still being built.
const schemaVersion = 1

func schemaStatements(database string) []string {
	prefix := "`" + database + "`."
	common := `
  exporter_id String,
  batch_id FixedString(64),
  event_id FixedString(64),
  source_cursor String,
  ingest_version UInt64,
  observed_at DateTime64(9, 'UTC'),
  schema_version UInt16,`
	return []string{
		fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s`", database),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %sschema_versions (
  component LowCardinality(String), schema_version UInt16, checksum FixedString(64), applied_at DateTime64(9, 'UTC')
) ENGINE=MergeTree ORDER BY (component, schema_version)`, prefix),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %slifecycle_events (%s
  occurred_at DateTime64(9, 'UTC'), resource_kind LowCardinality(String), analytics_resource_id String,
  action LowCardinality(String), change LowCardinality(String), conversation_id String,
  conversation_group_id String, content_type LowCardinality(String), memory_kind LowCardinality(String),
  snapshot_available UInt8, summary_json String
) ENGINE=ReplacingMergeTree(ingest_version) PARTITION BY toYYYYMM(occurred_at)
ORDER BY (exporter_id, resource_kind, occurred_at, event_id)`, prefix, common),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %sresources (%s
  resource_id String, conversation_id String, conversation_group_id String,
  resource_type LowCardinality(String), created_at DateTime64(9, 'UTC'), updated_at DateTime64(9, 'UTC'),
  is_archived UInt8, is_deleted UInt8, payload_json String
) ENGINE=ReplacingMergeTree(ingest_version) ORDER BY (exporter_id, resource_type, resource_id)`, prefix, common),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %sdeletion_fences (
  exporter_id String, batch_id FixedString(64), event_id FixedString(64),
  resource_type LowCardinality(String), resource_id String,
  conversation_id String, conversation_group_id String,
  ingest_version UInt64, observed_at DateTime64(9, 'UTC')
) ENGINE=ReplacingMergeTree(ingest_version) ORDER BY (exporter_id, resource_type, resource_id)`, prefix),
		resourceTableStatement(prefix, common, "conversations"),
		resourceTableStatement(prefix, common, "conversation_lineage"),
		resourceTableStatement(prefix, common, "entries"),
		resourceTableStatement(prefix, common, "memories"),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %sprojection_registry (
  exporter_id String, projection_name String, projection_digest FixedString(64), selector String,
  table_name String, state LowCardinality(String), updated_at DateTime64(9, 'UTC'), version UInt64
) ENGINE=ReplacingMergeTree(version) ORDER BY (exporter_id, projection_name)`, prefix),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %sretention_policy (
  exporter_id String, owner_id String, lifecycle_seconds Int64, tombstone_seconds Int64, generic_seconds Int64,
  projection_seconds Int64, failure_seconds Int64, active UInt8, version UInt64, updated_at DateTime64(9, 'UTC')
) ENGINE=ReplacingMergeTree(version) ORDER BY (exporter_id, owner_id)`, prefix),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %sprojection_failures (
  exporter_id String, batch_id FixedString(64), event_id FixedString(64), analytics_resource_id String,
  conversation_id String, conversation_group_id String,
  projection_name String, error_code LowCardinality(String), attempt_count UInt32,
  first_seen_at DateTime64(9, 'UTC'), last_seen_at DateTime64(9, 'UTC'), version UInt64
) ENGINE=ReplacingMergeTree(version) ORDER BY (exporter_id, projection_name, event_id)`, prefix),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %spurge_subjects (
  exporter_id String, purge_id FixedString(64), subject_kind LowCardinality(String),
  subject_id String, version UInt64
) ENGINE=ReplacingMergeTree(version) ORDER BY (exporter_id, purge_id, subject_kind, subject_id)`, prefix),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %spurge_queue (
  exporter_id String, batch_id FixedString(64), purge_id FixedString(64), event_id FixedString(64),
  resource_kind LowCardinality(String), analytics_resource_id String,
  requested_at DateTime64(9, 'UTC'), completed_at Nullable(DateTime64(9, 'UTC')),
  status LowCardinality(String), status_code LowCardinality(String), status_version UInt64
) ENGINE=ReplacingMergeTree(status_version) ORDER BY (exporter_id, purge_id)`, prefix),
		fmt.Sprintf(`CREATE OR REPLACE VIEW %slifecycle_events_current AS
		SELECT * EXCEPT(rn) FROM (
	 SELECT e.*, row_number() OVER (PARTITION BY e.exporter_id, e.event_id ORDER BY e.ingest_version DESC) rn
	 FROM %slifecycle_events e
) WHERE rn=1`, prefix, prefix),
		fmt.Sprintf(`CREATE OR REPLACE VIEW %sresources_all AS
	SELECT * EXCEPT(rn) FROM (
	 SELECT r.*, row_number() OVER (PARTITION BY r.exporter_id, r.resource_type, r.resource_id ORDER BY r.ingest_version DESC, r.event_id DESC) rn
	 FROM %sresources r
) WHERE rn=1`, prefix, prefix),
		fmt.Sprintf(`CREATE OR REPLACE VIEW %sresources_current AS
SELECT r.* FROM %sresources_all r
LEFT JOIN (%s) d ON r.exporter_id=d.exporter_id AND r.resource_type=d.resource_type AND r.resource_id=d.resource_id
WHERE r.is_deleted=0 AND r.ingest_version > ifNull(d.deletion_version, 0)`, prefix, prefix, deletionFenceSubquery(prefix)),
		resourceViewStatement(prefix, "conversations"),
		resourceViewStatement(prefix, "conversation_lineage"),
		resourceViewStatement(prefix, "entries"),
		resourceViewStatement(prefix, "memories"),
	}
}

func resourceTableStatement(prefix, common, table string) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s%s (%s
  resource_id String, conversation_id String, conversation_group_id String,
  resource_type LowCardinality(String), created_at DateTime64(9, 'UTC'), updated_at DateTime64(9, 'UTC'),
  is_archived UInt8, is_deleted UInt8, payload_json String
) ENGINE=ReplacingMergeTree(ingest_version) ORDER BY (exporter_id, resource_id)`, prefix, table, common)
}

func resourceViewStatement(prefix, table string) string {
	return fmt.Sprintf(`CREATE OR REPLACE VIEW %s%s_current AS
	SELECT r.* EXCEPT(rn) FROM (
	 SELECT r.*, row_number() OVER (PARTITION BY r.exporter_id, r.resource_id ORDER BY r.ingest_version DESC, r.event_id DESC) rn
	 FROM %s%s r
	) r
	LEFT JOIN (%s) d ON r.exporter_id=d.exporter_id AND d.resource_type='%s' AND r.resource_id=d.resource_id
	WHERE rn=1 AND is_deleted=0 AND r.ingest_version > ifNull(d.deletion_version, 0)`, prefix, table, prefix, table, deletionFenceSubquery(prefix), resourceKindForTable(table))
}

func deletionFenceSubquery(prefix string) string {
	return fmt.Sprintf(`SELECT f.exporter_id, f.resource_type, f.resource_id, max(f.ingest_version) deletion_version
	 FROM %sdeletion_fences f
	 GROUP BY f.exporter_id, f.resource_type, f.resource_id`, prefix)
}

func deletionFenceMaterializedViewStatement(database string) string {
	prefix := quoteIdentifier(database) + "."
	return fmt.Sprintf(`CREATE MATERIALIZED VIEW IF NOT EXISTS %sdeletion_fence_writer
TO %sdeletion_fences AS
SELECT exporter_id, batch_id, event_id, resource_type, resource_id,
       conversation_id, conversation_group_id, ingest_version, observed_at
FROM %sresources
WHERE is_deleted=1`, prefix, prefix, prefix)
}

func resourceKindForTable(table string) string {
	switch table {
	case "conversations":
		return "conversation"
	case "conversation_lineage":
		return "lineage"
	case "entries":
		return "entry"
	case "memories":
		return "memory"
	default:
		return ""
	}
}
