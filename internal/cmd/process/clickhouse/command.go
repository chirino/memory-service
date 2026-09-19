package clickhouse

import (
	"context"
	"os"
	"time"

	"github.com/urfave/cli/v3"
)

func Command() *cli.Command {
	return &cli.Command{
		Name: "clickhouse", Usage: "Export durable Memory Service analytics to ClickHouse", Flags: flags(),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg := Config{
				ExporterID: cmd.String("exporter-id"), Addresses: cmd.StringSlice("clickhouse-address"), Protocol: cmd.String("clickhouse-protocol"),
				Database: cmd.String("clickhouse-database"), Username: cmd.String("clickhouse-username"), Password: os.Getenv("MEMORY_SERVICE_CLICKHOUSE_PASSWORD"), PasswordFile: cmd.String("clickhouse-password-file"),
				TLS: cmd.Bool("clickhouse-tls"), CAFile: cmd.String("clickhouse-ca-file"), AllowInsecureClickHouse: cmd.Bool("allow-insecure-clickhouse"),
				SchemaMode: cmd.String("schema-mode"), PayloadMode: PayloadMode(cmd.String("payload-mode")), AllowDecryptedContent: cmd.Bool("allow-decrypted-content"),
				MetadataKeys: cmd.StringSlice("metadata-key"), BatchEvents: int(cmd.Int("batch-events")), BatchRows: int(cmd.Int("batch-rows")),
				ProjectionPaths: cmd.StringSlice("projection-path"), ProjectionFailurePolicy: cmd.String("projection-failure-policy"), ProjectionReplayID: cmd.String("projection-replay-id"),
				Disable: cmd.StringSlice("disable"), RetentionMode: cmd.String("retention-mode"), PurgeMode: cmd.String("purge-mode"),
				LifecycleRetention: cmd.Duration("lifecycle-retention"), TombstoneRetention: cmd.Duration("tombstone-retention"),
				GenericPayloadRetention: cmd.Duration("generic-payload-retention"), ProjectionRetention: cmd.Duration("projection-retention"),
				ProjectionFailureRetention: cmd.Duration("projection-failure-retention"),
				TailOnlyDevelopment:        cmd.Bool("tail-only-development"),
				BatchBytes:                 int(cmd.Int("batch-bytes")), MaxRecordBytes: int(cmd.Int("max-record-bytes")), BatchDelay: cmd.Duration("batch-delay"),
				RetryMin: cmd.Duration("retry-min"), RetryMax: cmd.Duration("retry-max"),
			}
			if cfg.ExporterID == "" {
				cfg.ExporterID = cmd.String("client-id")
			}
			running, err := StartProcessor(ctx, StartOptions{
				Endpoint: cmd.String("endpoint"), ClientID: cmd.String("client-id"), APIKey: cmd.String("api-key"), BearerToken: cmd.String("bearer-token"),
				AfterCursor: cmd.String("after-cursor"), CheckpointInterval: cmd.Duration("checkpoint-interval"), Config: cfg,
				LeaseTTL:      cmd.Duration("lease-ttl"),
				HealthAddress: cmd.String("health-address"),
				GRPCTLS:       cmd.Bool("grpc-tls"), GRPCCAFile: cmd.String("grpc-ca-file"), AllowInsecureGRPC: cmd.Bool("allow-insecure-grpc"),
			})
			if err != nil {
				return err
			}
			defer func() {
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_ = running.Shutdown(shutdownCtx)
			}()
			return running.Wait()
		},
	}
}

func flags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{Name: "endpoint", Sources: cli.EnvVars("MEMORY_SERVICE_GRPC_ENDPOINT"), Required: true},
		&cli.StringFlag{Name: "client-id", Sources: cli.EnvVars("MEMORY_SERVICE_PROCESS_CLIENT_ID"), Required: true},
		&cli.StringFlag{Name: "exporter-id", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_EXPORTER_ID")},
		&cli.StringFlag{Name: "api-key", Sources: cli.EnvVars("MEMORY_SERVICE_API_KEY")},
		&cli.StringFlag{Name: "bearer-token", Sources: cli.EnvVars("MEMORY_SERVICE_BEARER_TOKEN")},
		&cli.StringFlag{Name: "after-cursor", Sources: cli.EnvVars("MEMORY_SERVICE_PROCESS_AFTER_CURSOR"), Value: "start"},
		&cli.DurationFlag{Name: "checkpoint-interval", Sources: cli.EnvVars("MEMORY_SERVICE_PROCESS_CHECKPOINT_INTERVAL"), Value: time.Second},
		&cli.DurationFlag{Name: "lease-ttl", Sources: cli.EnvVars("MEMORY_SERVICE_PROCESS_LEASE_TTL"), Value: 30 * time.Second},
		&cli.StringFlag{Name: "health-address", Sources: cli.EnvVars("MEMORY_SERVICE_PROCESS_HEALTH_ADDRESS"), Value: ":8081"},
		&cli.BoolFlag{Name: "grpc-tls", Sources: cli.EnvVars("MEMORY_SERVICE_PROCESS_GRPC_TLS")},
		&cli.StringFlag{Name: "grpc-ca-file", Sources: cli.EnvVars("MEMORY_SERVICE_PROCESS_GRPC_CA_FILE")},
		&cli.BoolFlag{Name: "allow-insecure-grpc", Sources: cli.EnvVars("MEMORY_SERVICE_PROCESS_ALLOW_INSECURE_GRPC")},
		&cli.StringSliceFlag{Name: "clickhouse-address", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_ADDRESS"), Required: true},
		&cli.StringFlag{Name: "clickhouse-protocol", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_PROTOCOL"), Value: "native"},
		&cli.StringFlag{Name: "clickhouse-database", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_DATABASE"), Value: "memory_service"},
		&cli.StringFlag{Name: "clickhouse-username", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_USERNAME"), Required: true},
		&cli.StringFlag{Name: "clickhouse-password-file", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_PASSWORD_FILE")},
		&cli.BoolFlag{Name: "clickhouse-tls", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_TLS")},
		&cli.StringFlag{Name: "clickhouse-ca-file", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_CA_FILE")},
		&cli.BoolFlag{Name: "allow-insecure-clickhouse", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_ALLOW_INSECURE")},
		&cli.StringFlag{Name: "schema-mode", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_SCHEMA_MODE"), Value: "manage"},
		&cli.StringFlag{Name: "payload-mode", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_PAYLOAD_MODE"), Value: "metadata"},
		&cli.BoolFlag{Name: "allow-decrypted-content", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_ALLOW_DECRYPTED_CONTENT")},
		&cli.IntFlag{Name: "batch-events", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_BATCH_EVENTS"), Value: 10000},
		&cli.IntFlag{Name: "batch-rows", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_BATCH_ROWS"), Value: 100000},
		&cli.IntFlag{Name: "batch-bytes", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_BATCH_BYTES"), Value: 8 << 20},
		&cli.IntFlag{Name: "max-record-bytes", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_MAX_RECORD_BYTES"), Value: defaultMaxRecordBytes},
		&cli.DurationFlag{Name: "batch-delay", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_BATCH_DELAY"), Value: time.Second},
		&cli.DurationFlag{Name: "retry-min", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_RETRY_MIN"), Value: time.Second},
		&cli.DurationFlag{Name: "retry-max", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_RETRY_MAX"), Value: 30 * time.Second},
		&cli.StringSliceFlag{Name: "metadata-key", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_METADATA_KEY")},
		&cli.StringSliceFlag{Name: "projection-path", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_PROJECTION_PATH")},
		&cli.StringFlag{Name: "projection-failure-policy", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_PROJECTION_FAILURE_POLICY"), Value: "continue-generic"},
		&cli.StringFlag{Name: "projection-replay-id", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_PROJECTION_REPLAY_ID")},
		&cli.StringSliceFlag{Name: "disable", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_DISABLE")},
		&cli.StringFlag{Name: "retention-mode", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_RETENTION_MODE"), Value: RetentionManaged},
		&cli.StringFlag{Name: "purge-mode", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_PURGE_MODE"), Value: PurgeManaged},
		&cli.DurationFlag{Name: "lifecycle-retention", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_LIFECYCLE_RETENTION")},
		&cli.DurationFlag{Name: "tombstone-retention", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_TOMBSTONE_RETENTION")},
		&cli.DurationFlag{Name: "generic-payload-retention", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_GENERIC_PAYLOAD_RETENTION")},
		&cli.DurationFlag{Name: "projection-retention", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_PROJECTION_RETENTION")},
		&cli.DurationFlag{Name: "projection-failure-retention", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_PROJECTION_FAILURE_RETENTION")},
		&cli.BoolFlag{Name: "tail-only-development", Sources: cli.EnvVars("MEMORY_SERVICE_CLICKHOUSE_TAIL_ONLY_DEVELOPMENT")},
	}
}
