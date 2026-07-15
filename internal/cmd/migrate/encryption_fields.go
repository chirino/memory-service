package migrate

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/dataencryption"
	"github.com/chirino/memory-service/internal/registry/encrypt"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/urfave/cli/v3"
)

func encryptionFieldsCommand() *cli.Command {
	cfg := config.DefaultConfig()
	flags := []cli.Flag{
		&cli.StringFlag{
			Name:        "db-url",
			Sources:     cli.EnvVars("MEMORY_SERVICE_DB_URL"),
			Destination: &cfg.DBURL,
			Usage:       "Database connection URL",
			Required:    true,
		},
		&cli.StringFlag{
			Name:        "db-kind",
			Sources:     cli.EnvVars("MEMORY_SERVICE_DB_KIND"),
			Destination: &cfg.DatastoreType,
			Value:       cfg.DatastoreType,
			Usage:       "Store backend (postgres|sqlite|mongo)",
		},
		&cli.StringFlag{
			Name:        "encryption-kind",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ENCRYPTION_KIND"),
			Destination: &cfg.EncryptionProviders,
			Value:       cfg.EncryptionProviders,
			Usage:       "Comma-separated ordered list of encryption providers (" + strings.Join(encrypt.Names(), "|") + ")",
		},
		&cli.StringFlag{
			Name:        "encryption-dek-key",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ENCRYPTION_DEK_KEY", "MEMORY_SERVICE_ENCRYPTION_KEY"),
			Destination: &cfg.EncryptionKey,
			Usage:       "Comma-separated AES keys for the 'dek' provider (hex or base64, 32 bytes)",
		},
		&cli.BoolFlag{
			Name:        "encryption-legacy-plain-read-enabled",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ENCRYPTION_LEGACY_PLAIN_READ_ENABLED"),
			Destination: &cfg.EncryptionLegacyPlainReadEnabled,
			Usage:       "Permit headerless legacy plaintext field reads when plain is registered as a fallback provider",
		},
		&cli.BoolFlag{
			Name:        "encryption-legacy-byte-v1-read-enabled",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ENCRYPTION_LEGACY_BYTE_V1_READ_ENABLED"),
			Destination: &cfg.EncryptionLegacyByteV1ReadEnabled,
			Value:       cfg.EncryptionLegacyByteV1ReadEnabled,
			Usage:       "Permit legacy MSEH v1 byte-encrypted field reads during migration",
		},
		&cli.IntFlag{
			Name:  "to-version",
			Value: int(dataencryption.VersionFieldAESGCM),
			Usage: "Target persisted field MSEH version",
		},
		&cli.BoolFlag{
			Name:  "dry-run",
			Usage: "Inventory field encryption versions without writing",
		},
		&cli.IntFlag{
			Name:  "batch-size",
			Value: 500,
			Usage: "Encrypted field page size",
		},
	}
	flags = append(flags, encrypt.PluginFlags(&cfg)...)

	return &cli.Command{
		Name:  "encryption-fields",
		Usage: "Migrate persisted encrypted fields to MSEH v4",
		Flags: flags,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runFieldEncryptionMigration(ctx, cmd, &cfg)
		},
	}
}

func runFieldEncryptionMigration(ctx context.Context, cmd *cli.Command, cfg *config.Config) error {
	if cmd.Int("to-version") != int(dataencryption.VersionFieldAESGCM) {
		return fmt.Errorf("unsupported --to-version=%d; only %d is supported", cmd.Int("to-version"), dataencryption.VersionFieldAESGCM)
	}
	if cmd.Int("batch-size") <= 0 {
		return fmt.Errorf("--batch-size must be greater than zero")
	}
	if err := cfg.ApplyJavaCompatFromEnv(); err != nil {
		return err
	}
	encrypt.ApplyAll(cfg, cmd)

	ctx = config.WithContext(ctx, cfg)
	encSvc, err := dataencryption.New(ctx, cfg)
	if err != nil {
		return err
	}
	if !encSvc.PrimarySupportsFieldEncryption() {
		return fmt.Errorf("field encryption migration requires a primary provider that supports MSEH v4 field encryption")
	}
	ctx = dataencryption.WithContext(ctx, encSvc)

	storeLoader, err := registrystore.Select(cfg.DatastoreType)
	if err != nil {
		return err
	}
	memStore, err := storeLoader(ctx)
	if err != nil {
		return err
	}
	migrator, ok := memStore.(registrystore.FieldEncryptionMigrator)
	if !ok {
		return fmt.Errorf("store %q does not support field encryption migration", cfg.DatastoreType)
	}
	stats, err := migrator.MigrateEncryptedFields(ctx, registrystore.FieldEncryptionMigrationOptions{
		DryRun:    cmd.Bool("dry-run"),
		BatchSize: cmd.Int("batch-size"),
	})
	if err != nil {
		return err
	}
	logFieldMigrationStats(stats)
	if !cmd.Bool("dry-run") {
		verifyStats, err := migrator.MigrateEncryptedFields(ctx, registrystore.FieldEncryptionMigrationOptions{
			DryRun:    true,
			BatchSize: cmd.Int("batch-size"),
		})
		if err != nil {
			return fmt.Errorf("field encryption migration verification failed: %w", err)
		}
		if remaining := fieldMigrationRemainingLegacyValues(verifyStats); remaining > 0 {
			return fmt.Errorf("field encryption migration left %d legacy/headerless values; rerun the command", remaining)
		}
	}
	return nil
}

func logFieldMigrationStats(stats *registrystore.FieldEncryptionMigrationStats) {
	if stats == nil || len(stats.Domains) == 0 {
		log.Info("Field encryption migration complete")
		return
	}
	domains := make([]string, 0, len(stats.Domains))
	for domain := range stats.Domains {
		domains = append(domains, domain)
	}
	sort.Strings(domains)
	for _, domain := range domains {
		ds := stats.Domains[domain]
		log.Info("Field encryption migration domain complete",
			"domain", domain,
			"scanned", ds.Scanned,
			"alreadyV4", ds.AlreadyV4,
			"legacyValues", ds.LegacyValues,
			"headerlessValues", ds.HeaderlessValues,
			"dryRunWouldMigrate", ds.DryRunWouldMigrate,
			"migrated", ds.Migrated,
			"concurrentSkipped", ds.ConcurrentSkipped,
		)
	}
}

func fieldMigrationRemainingLegacyValues(stats *registrystore.FieldEncryptionMigrationStats) int {
	if stats == nil {
		return 0
	}
	remaining := 0
	for _, ds := range stats.Domains {
		if ds == nil {
			continue
		}
		remaining += ds.DryRunWouldMigrate
	}
	return remaining
}
