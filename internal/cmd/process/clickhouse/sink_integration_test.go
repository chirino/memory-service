package clickhouse

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/require"
)

func TestClickHouseRejectsUnregisteredProjectionRelation(t *testing.T) {
	requireClickHouseIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir, manifest, projection := writeIntegrationProjection(t, ctx, "existing_relation_v1", "existing/relation/v1")
	database, admin := createIntegrationDatabase(t, ctx)
	require.NoError(t, admin.Exec(ctx, "CREATE TABLE "+quoteIdentifier(database)+"."+quoteIdentifier(projection.TableName)+" (value String) ENGINE=MergeTree ORDER BY tuple()"))

	_, err := OpenSink(ctx, integrationDatabaseConfig(database, "existing-relation", dir, manifest))
	require.ErrorContains(t, err, "unowned analytics database")
}

func TestClickHouseHistoricalProjectionRetention(t *testing.T) {
	requireClickHouseIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir, historicalManifest, historical := writeIntegrationProjection(t, ctx, "maintenance_a_v1", "maintenance-a/v1")
	database, _ := createIntegrationDatabase(t, ctx)
	cfg := integrationDatabaseConfig(database, "maintenance", dir, historicalManifest)
	cfg.ProjectionRetention = time.Hour
	cfg.TombstoneRetention = 2 * time.Hour
	first, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	require.NoError(t, first.Close())

	_, current, _ := writeIntegrationProjectionInDir(t, ctx, dir, "maintenance_b_v1", "maintenance-b/v1")
	cfg.ProjectionPaths = []string{current}
	sink, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })
	var ddl string
	require.NoError(t, sink.conn.QueryRow(ctx, "SELECT create_table_query FROM system.tables WHERE database=? AND name=?", database, historical.TableName).Scan(&ddl))
	require.Contains(t, ddl, "TTL")
}

func TestClickHouseRejectsConflictingSharedRetentionPolicy(t *testing.T) {
	requireClickHouseIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir, manifest, _ := writeIntegrationProjection(t, ctx, "retention_policy_v1", "retention-policy/v1")
	database, admin := createIntegrationDatabase(t, ctx)
	firstConfig := integrationDatabaseConfig(database, "retention-a", dir, manifest)
	firstConfig.LifecycleRetention = time.Hour
	first, err := OpenSink(ctx, firstConfig)
	require.NoError(t, err)

	conflicting := integrationDatabaseConfig(database, "retention-b", dir, manifest)
	conflicting.LifecycleRetention = 2 * time.Hour
	_, err = OpenSink(ctx, conflicting)
	require.ErrorContains(t, err, "retention policy conflicts")

	// Simulate a process crash: its owner row remains active after its last
	// liveness deadline, and a new owner must be able to reclaim the policy.
	first.stopRetentionHeartbeat()
	require.NoError(t, admin.Exec(ctx, "INSERT INTO "+quoteIdentifier(database)+".retention_policy VALUES (?,?,?,?,?,?,?,?,?)", firstConfig.ExporterID, first.retentionOwner, int64(3600), int64(0), int64(0), int64(0), uint8(1), uint64(time.Now().UTC().UnixNano()), time.Now().UTC().Add(-retentionOwnerTTL-time.Second)))
	require.NoError(t, first.conn.Close())
	first.conn = nil

	updated := integrationDatabaseConfig(database, "retention-a", dir, manifest)
	updated.LifecycleRetention = 2 * time.Hour
	second, err := OpenSink(ctx, updated)
	require.NoError(t, err)
	_, err = OpenSink(ctx, integrationDatabaseConfig(database, "retention-c", dir, manifest))
	require.ErrorContains(t, err, "retention policy conflicts")
	require.NoError(t, second.Close())

	matching := integrationDatabaseConfig(database, "retention-c", dir, manifest)
	matching.LifecycleRetention = 2 * time.Hour
	third, err := OpenSink(ctx, matching)
	require.NoError(t, err)
	require.NoError(t, third.Close())
	var ddl string
	require.NoError(t, admin.QueryRow(ctx, "SELECT create_table_query FROM system.tables WHERE database=? AND name='lifecycle_events'", database).Scan(&ddl))
	require.Contains(t, ddl, "toIntervalSecond(7200)")
}

func TestClickHouseDisabledProjectionIsValidatedButNotCreated(t *testing.T) {
	requireClickHouseIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir, manifest, projection := writeIntegrationProjection(t, ctx, "disabled_projection_v1", "disabled-projection/v1")
	database, admin := createIntegrationDatabase(t, ctx)
	cfg := integrationDatabaseConfig(database, "disabled-projection", dir, manifest)
	cfg.Disable = []string{"*:projections"}
	sink, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })

	var count uint64
	require.NoError(t, admin.QueryRow(ctx, "SELECT count() FROM system.tables WHERE database=? AND name=?", database, projection.TableName).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, admin.QueryRow(ctx, "SELECT count() FROM "+quoteIdentifier(database)+".projection_registry WHERE exporter_id=?", cfg.ExporterID).Scan(&count))
	require.Zero(t, count)
}

func TestClickHouseValidateModeRejectsRetentionTTLMismatchWithoutDDL(t *testing.T) {
	requireClickHouseIntegration(t)
	tests := []struct {
		name        string
		provision   func(*Config)
		configure   func(*Config)
		targetTable func(*Projection) string
	}{
		{
			name:      "missing lifecycle",
			provision: func(cfg *Config) { cfg.LifecycleRetention = 0 },
			configure: func(cfg *Config) { cfg.LifecycleRetention = time.Hour },
			targetTable: func(*Projection) string {
				return "lifecycle_events"
			},
		},
		{
			name:      "membership two clause",
			configure: func(cfg *Config) { cfg.ProjectionRetention = time.Hour },
			targetTable: func(*Projection) string {
				return "conversation_memberships"
			},
		},
		{
			name:      "projection failure",
			configure: func(cfg *Config) { cfg.ProjectionFailureRetention = time.Hour },
			targetTable: func(*Projection) string {
				return "projection_failures"
			},
		},
		{
			name:      "historical projection",
			configure: func(cfg *Config) {},
			targetTable: func(historical *Projection) string {
				return historical.TableName
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			dir, historicalManifest, historical := writeIntegrationProjection(t, ctx, "validate_retention_a_v1", "validate-retention-a/v1")
			database, admin := createIntegrationDatabase(t, ctx)
			cfg := integrationDatabaseConfig(database, "validate-retention", dir, historicalManifest)
			cfg.LifecycleRetention = 2 * time.Hour
			cfg.TombstoneRetention = 2 * time.Hour
			cfg.ProjectionRetention = 2 * time.Hour
			cfg.ProjectionFailureRetention = 2 * time.Hour
			if tt.provision != nil {
				tt.provision(&cfg)
			}
			first, err := OpenSink(ctx, cfg)
			require.NoError(t, err)
			require.NoError(t, first.Close())

			_, currentManifest, _ := writeIntegrationProjectionInDir(t, ctx, dir, "validate_retention_b_v1", "validate-retention-b/v1")
			cfg.ProjectionPaths = []string{currentManifest}
			second, err := OpenSink(ctx, cfg)
			require.NoError(t, err)
			require.NoError(t, second.Close())
			ddlBefore := readIntegrationSchemaDDL(t, ctx, admin, database)

			validateConfig := cfg
			validateConfig.SchemaMode = "validate"
			if tt.name == "historical projection" {
				require.NoError(t, admin.Exec(ctx, "ALTER TABLE "+quoteIdentifier(database)+"."+quoteIdentifier(historical.TableName)+" REMOVE TTL"))
				ddlBefore = readIntegrationSchemaDDL(t, ctx, admin, database)
			}
			tt.configure(&validateConfig)
			_, err = OpenSink(ctx, validateConfig)
			require.ErrorContains(t, err, "TTL mismatch")
			require.ErrorContains(t, err, tt.targetTable(historical))
			require.Equal(t, ddlBefore, readIntegrationSchemaDDL(t, ctx, admin, database), "validate mode must not modify DDL after a retention mismatch")
		})
	}
}

func TestClickHouseRetentionRenewalFailureIsTerminal(t *testing.T) {
	requireClickHouseIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir, manifest, _ := writeIntegrationProjection(t, ctx, "retention_renewal_failure_v1", "retention-renewal-failure/v1")
	database, _ := createIntegrationDatabase(t, ctx)
	cfg := integrationDatabaseConfig(database, "retention-renewal-failure", dir, manifest)
	sink, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })
	sink.stopRetentionHeartbeat()
	require.NoError(t, sink.conn.Close())

	err = sink.renewRetentionOwnership(ctx)
	require.ErrorContains(t, err, "retention policy ownership lost")
	select {
	case backgroundErr := <-sink.BackgroundErrors():
		require.ErrorContains(t, backgroundErr, "verify live retention owner")
	case <-time.After(time.Second):
		t.Fatal("retention renewal failure was not reported to the processor runtime")
	}
}

func createIntegrationDatabase(t *testing.T, ctx context.Context) (string, driver.Conn) {
	t.Helper()
	database := "analytics_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	admin, err := ch.Open(&ch.Options{Addr: []string{integrationNativeAddress()}, Auth: ch.Auth{Database: "default", Username: "clickhouse", Password: "clickhouse"}})
	require.NoError(t, err)
	require.NoError(t, admin.Ping(ctx))
	t.Cleanup(func() { _ = admin.Close() })
	require.NoError(t, admin.Exec(ctx, "CREATE DATABASE "+quoteIdentifier(database)))
	t.Cleanup(func() { _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+quoteIdentifier(database)) })
	return database, admin
}

func readIntegrationSchemaDDL(t *testing.T, ctx context.Context, conn driver.Conn, database string) map[string]string {
	t.Helper()
	rows, err := conn.Query(ctx, "SELECT name, create_table_query FROM system.tables WHERE database=? ORDER BY name", database)
	require.NoError(t, err)
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var name, ddl string
		require.NoError(t, rows.Scan(&name, &ddl))
		result[name] = ddl
	}
	require.NoError(t, rows.Err())
	return result
}

func integrationDatabaseConfig(database, exporterID, dir, manifestPath string) Config {
	return Config{ExporterID: exporterID, Database: database, Addresses: []string{integrationNativeAddress()}, Username: "clickhouse", PasswordFile: filepath.Join(dir, "password"), AllowInsecureClickHouse: true, PayloadMode: PayloadProjected, ProjectionPaths: []string{manifestPath}}
}

func writeIntegrationProjection(t *testing.T, ctx context.Context, name, selector string) (string, string, *Projection) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "password"), []byte("clickhouse"), 0o600))
	_, path, projection := writeIntegrationProjectionInDir(t, ctx, dir, name, selector)
	return dir, path, projection
}

func writeIntegrationProjectionInDir(t *testing.T, ctx context.Context, dir, name, selector string) (string, string, *Projection) {
	t.Helper()
	path := filepath.Join(dir, "projection-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".yaml")
	manifest := fmt.Sprintf(`apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata: {name: %s}
spec:
  resource: entry
  selector: {contentType: %s}
  columns:
    outcome: {type: string, nullable: false}
  projectionRego: |
    package memoryservice.analytics
    output := {"outcome": input.record.content.outcome}
`, name, selector)
	require.NoError(t, os.WriteFile(path, []byte(manifest), 0o600))
	set, err := LoadProjectionSet(ctx, []string{path})
	require.NoError(t, err)
	require.Len(t, set.Items, 1)
	return dir, path, set.Items[0]
}
