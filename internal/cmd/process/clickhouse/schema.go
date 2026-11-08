package clickhouse

import "fmt"

func commonColumns() string {
	return `exporter_id String, batch_id FixedString(64), event_id FixedString(64),
 source_cursor String, ingest_version UInt64, observed_at DateTime64(9, 'UTC')`
}

func schemaStatements(database string) []string {
	p := quoteIdentifier(database) + "."
	common := commonColumns()
	return []string{
		fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s", quoteIdentifier(database)),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %sschema_versions (
 component LowCardinality(String), checksum FixedString(64), applied_at DateTime64(9, 'UTC')
 ) ENGINE=MergeTree ORDER BY component`, p),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %slifecycle_events (%s,
 occurred_at DateTime64(9, 'UTC'), resource_kind LowCardinality(String), analytics_resource_id String,
 action LowCardinality(String), change LowCardinality(String), conversation_id String,
 conversation_group_id String, content_type LowCardinality(String), memory_kind LowCardinality(String), snapshot_available UInt8
 ) ENGINE=ReplacingMergeTree(ingest_version) PARTITION BY toYYYYMM(occurred_at)
 ORDER BY (exporter_id,resource_kind,occurred_at,event_id)`, p, common),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %sdeletion_fences (%s,
 resource_kind LowCardinality(String), resource_id String, conversation_id String, conversation_group_id String
 ) ENGINE=ReplacingMergeTree(ingest_version) ORDER BY (exporter_id,resource_kind,resource_id)`, p, common),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %sprojection_registry (
 exporter_id String, projection_name String, projection_digest FixedString(64), selector String,
 table_name String, state LowCardinality(String), updated_at DateTime64(9, 'UTC'), version UInt64,
 previous String, row_mode LowCardinality(String), ordering LowCardinality(String)
 ) ENGINE=ReplacingMergeTree(version) ORDER BY (exporter_id,projection_name)`, p),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %sretention_policy (
 exporter_id String, owner_id String, lifecycle_seconds Int64, tombstone_seconds Int64,
 projection_seconds Int64, failure_seconds Int64, active UInt8, version UInt64, updated_at DateTime64(9, 'UTC')
 ) ENGINE=ReplacingMergeTree(version) ORDER BY (exporter_id,owner_id)`, p),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %sprojection_failures (%s,
 resource_kind LowCardinality(String), analytics_resource_id String, conversation_id String,
 conversation_group_id String, projection_name String, error_code LowCardinality(String), attempt_count UInt32,
 first_seen_at DateTime64(9, 'UTC'), last_seen_at DateTime64(9, 'UTC')
 ) ENGINE=ReplacingMergeTree(ingest_version) ORDER BY (exporter_id,projection_name,event_id)`, p, common),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %spurge_queue (
 exporter_id String, batch_id FixedString(64), purge_id FixedString(64), event_id FixedString(64),
 resource_kind LowCardinality(String), analytics_resource_id String, conversation_id String,
 conversation_group_id String, purge_scope LowCardinality(String), cutoff_ingest_version UInt64,
 requested_at DateTime64(9, 'UTC'), completed_at Nullable(DateTime64(9, 'UTC')),
 status LowCardinality(String), status_code LowCardinality(String), status_version UInt8
 ) ENGINE=ReplacingMergeTree(status_version) ORDER BY (exporter_id,purge_id)`, p),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %sconversation_memberships (%s,
 resource_id String, conversation_group_id String, user_id String, access_level LowCardinality(String),
 created_at Nullable(DateTime64(9, 'UTC')), is_deleted UInt8
 ) ENGINE=ReplacingMergeTree(ingest_version) ORDER BY (exporter_id,conversation_group_id,user_id)`, p, common),
		fmt.Sprintf(`CREATE OR REPLACE VIEW %slifecycle_events_current AS
 SELECT * EXCEPT(rn) FROM (SELECT e.*, row_number() OVER (PARTITION BY exporter_id,event_id ORDER BY ingest_version DESC) rn
 FROM %slifecycle_events e) WHERE rn=1`, p, p),
		fmt.Sprintf(`CREATE OR REPLACE VIEW %sconversation_memberships_current AS
 SELECT r.* EXCEPT(rn) FROM (SELECT m.*, row_number() OVER
 (PARTITION BY exporter_id,conversation_group_id,user_id ORDER BY ingest_version DESC,event_id DESC) rn
 FROM %sconversation_memberships m) r
 LEFT JOIN (%s) d ON r.exporter_id=d._fence_exporter_id AND d._fence_resource_kind='membership' AND r.resource_id=d._fence_resource_id
 LEFT JOIN (%s) g ON r.exporter_id=g._fence_exporter_id AND g._fence_resource_kind='conversation_group' AND r.conversation_group_id=g._fence_resource_id
 WHERE rn=1 AND r.is_deleted=0 AND r.ingest_version > ifNull(d._fence_version,0) AND ifNull(g._fence_version,0)=0`, p, p, deletionFenceSubquery(p), deletionFenceSubquery(p)),
	}
}

func deletionFenceSubquery(prefix string) string {
	return fmt.Sprintf(`SELECT exporter_id AS _fence_exporter_id,resource_kind AS _fence_resource_kind,resource_id AS _fence_resource_id,max(ingest_version) _fence_version
 FROM %sdeletion_fences GROUP BY exporter_id,resource_kind,resource_id`, prefix)
}
