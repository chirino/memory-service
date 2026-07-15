package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/dataencryption"
	registryattach "github.com/chirino/memory-service/internal/registry/attach"
	"github.com/chirino/memory-service/internal/registry/encrypt"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/tempfiles"
	"github.com/urfave/cli/v3"
)

type attachmentMigrationStats struct {
	Scanned            int
	UniqueStorageKeys  int
	MissingStorageKey  int
	SkippedNonMSEH     int
	SkippedV3          int
	SkippedOther       int
	MissingMetadata    int
	HashMismatch       int
	Migrated           int
	DryRunWouldMigrate int
}

func attachmentsCommand() *cli.Command {
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
			Name:        "attachments-kind",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ATTACHMENTS_KIND"),
			Destination: &cfg.AttachType,
			Value:       cfg.AttachType,
			Usage:       "Attachment store (db|" + strings.Join(registryattach.Names(), "|") + ")",
		},
		&cli.StringFlag{
			Name:        "attachments-fs-dir",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ATTACHMENTS_FS_DIR"),
			Destination: &cfg.AttachFSDir,
			Usage:       "Filesystem directory for local attachment storage",
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
			Name:        "encryption-legacy-stream-v2-read-enabled",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ENCRYPTION_LEGACY_STREAM_V2_READ_ENABLED"),
			Destination: &cfg.EncryptionLegacyStreamV2ReadEnabled,
			Value:       cfg.EncryptionLegacyStreamV2ReadEnabled,
			Usage:       "Permit legacy MSEH v2 AES-CTR attachment stream reads during migration",
		},
		&cli.StringFlag{
			Name:        "temp-dir",
			Sources:     cli.EnvVars("MEMORY_SERVICE_TEMP_DIR"),
			Destination: &cfg.TempDir,
			Usage:       "Directory for migration temporary ciphertext files",
		},
		&cli.IntFlag{
			Name:  "to-stream-version",
			Value: int(dataencryption.VersionAttachmentStreamAESGCM),
			Usage: "Target attachment stream MSEH version",
		},
		&cli.BoolFlag{
			Name:  "dry-run",
			Usage: "Inventory attachment stream versions and metadata without writing",
		},
		&cli.IntFlag{
			Name:  "batch-size",
			Value: 500,
			Usage: "Attachment metadata page size",
		},
	}
	flags = append(flags, registryattach.PluginFlags(&cfg)...)
	flags = append(flags, encrypt.PluginFlags(&cfg)...)

	return &cli.Command{
		Name:  "attachments",
		Usage: "Migrate encrypted attachment streams",
		Flags: flags,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runAttachmentMigration(ctx, cmd, &cfg)
		},
	}
}

func runAttachmentMigration(ctx context.Context, cmd *cli.Command, cfg *config.Config) error {
	if cmd.Int("to-stream-version") != int(dataencryption.VersionAttachmentStreamAESGCM) {
		return fmt.Errorf("unsupported --to-stream-version=%d; only %d is supported", cmd.Int("to-stream-version"), dataencryption.VersionAttachmentStreamAESGCM)
	}
	if cmd.Int("batch-size") <= 0 {
		return fmt.Errorf("--batch-size must be greater than zero")
	}
	cfg.AttachTypeExplicit = cmd.IsSet("attachments-kind")
	if err := cfg.ApplyJavaCompatFromEnv(); err != nil {
		return err
	}
	registryattach.ApplyAll(cfg, cmd)
	encrypt.ApplyAll(cfg, cmd)

	ctx = config.WithContext(ctx, cfg)
	encSvc, err := dataencryption.New(ctx, cfg)
	if err != nil {
		return err
	}
	if !encSvc.IsPrimaryReal() || cfg.EncryptionAttachmentsDisabled {
		return fmt.Errorf("attachment stream migration requires an enabled real encryption provider")
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

	attachStoreName, err := config.ResolveAttachmentStoreName(cfg)
	if err != nil {
		return err
	}
	attachLoader, err := registryattach.Select(attachStoreName)
	if err != nil {
		return err
	}
	attachStore, err := attachLoader(ctx)
	if err != nil {
		return err
	}

	dryRun := cmd.Bool("dry-run")
	replacer, ok := attachStore.(registryattach.AtomicAttachmentReplacer)
	if !dryRun && !ok {
		return fmt.Errorf("attachment store %q does not support atomic replacement; refusing to mutate attachment content", attachStoreName)
	}

	stats, err := migrateAttachments(ctx, memStore, attachStore, replacer, encSvc, migrateAttachmentsOptions{
		DryRun:    dryRun,
		BatchSize: cmd.Int("batch-size"),
		TempDir:   cfg.ResolvedTempDir(),
	})
	if err != nil {
		return err
	}
	log.Info("Attachment migration complete",
		"scanned", stats.Scanned,
		"uniqueStorageKeys", stats.UniqueStorageKeys,
		"missingStorageKey", stats.MissingStorageKey,
		"skippedNonMSEH", stats.SkippedNonMSEH,
		"skippedV3", stats.SkippedV3,
		"skippedOther", stats.SkippedOther,
		"missingMetadata", stats.MissingMetadata,
		"hashMismatch", stats.HashMismatch,
		"dryRunWouldMigrate", stats.DryRunWouldMigrate,
		"migrated", stats.Migrated,
	)
	return nil
}

type migrateAttachmentsOptions struct {
	DryRun    bool
	BatchSize int
	TempDir   string
}

func migrateAttachments(ctx context.Context, memStore registrystore.MemoryStore, attachStore registryattach.AttachmentStore, replacer registryattach.AtomicAttachmentReplacer, encSvc *dataencryption.Service, opts migrateAttachmentsOptions) (*attachmentMigrationStats, error) {
	stats := &attachmentMigrationStats{}
	seen := map[string]struct{}{}
	var cursor *string
	for {
		page, next, err := memStore.AdminListAttachments(ctx, registrystore.AdminAttachmentQuery{AfterCursor: cursor, Limit: opts.BatchSize, Status: "all"})
		if err != nil {
			return stats, err
		}
		for _, item := range page {
			stats.Scanned++
			storageKey := ""
			if item.StorageKey != nil {
				storageKey = strings.TrimSpace(*item.StorageKey)
			}
			if storageKey == "" {
				stats.MissingStorageKey++
				continue
			}
			if _, ok := seen[storageKey]; ok {
				continue
			}
			seen[storageKey] = struct{}{}
			stats.UniqueStorageKeys++
			if err := migrateAttachmentObject(ctx, item, storageKey, attachStore, replacer, encSvc, opts, stats); err != nil {
				return stats, err
			}
		}
		if next == nil || len(page) == 0 {
			break
		}
		cursor = next
	}
	return stats, nil
}

func migrateAttachmentObject(ctx context.Context, item registrystore.AdminAttachment, storageKey string, attachStore registryattach.AttachmentStore, replacer registryattach.AtomicAttachmentReplacer, encSvc *dataencryption.Service, opts migrateAttachmentsOptions, stats *attachmentMigrationStats) error {
	version, err := inspectAttachmentStreamVersion(ctx, attachStore, storageKey)
	if err != nil {
		return err
	}
	switch version {
	case 0:
		stats.SkippedNonMSEH++
		return nil
	case dataencryption.VersionAESCTR:
		// migrate below
	case dataencryption.VersionAttachmentStreamAESGCM:
		stats.SkippedV3++
		return nil
	default:
		stats.SkippedOther++
		return nil
	}

	if item.SHA256 == nil || item.Size == nil || !validSHA256Hex(*item.SHA256) {
		stats.MissingMetadata++
		if opts.DryRun {
			return nil
		}
		return fmt.Errorf("attachment %s storage key %q is missing valid size/SHA-256 metadata", item.ID, storageKey)
	}
	if opts.DryRun {
		stats.DryRunWouldMigrate++
		return nil
	}
	if replacer == nil {
		return fmt.Errorf("attachment store does not support atomic replacement; refusing to mutate attachment content")
	}

	tmp, err := tempfiles.Create(opts.TempDir, "memory-service-attachment-v3-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()

	size, digest, err := rewriteAttachmentToV3Temp(ctx, attachStore, storageKey, encSvc, tmp)
	if err != nil {
		return err
	}
	if size != *item.Size || digest != strings.ToLower(*item.SHA256) {
		stats.HashMismatch++
		return fmt.Errorf("attachment %s storage key %q plaintext metadata mismatch: got size=%d sha256=%s expected size=%d sha256=%s", item.ID, storageKey, size, digest, *item.Size, strings.ToLower(*item.SHA256))
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek rewritten attachment temp file: %w", err)
	}
	if _, err := replacer.Replace(ctx, storageKey, tmp, item.ContentType); err != nil {
		return fmt.Errorf("replace attachment storage key %q: %w", storageKey, err)
	}
	stats.Migrated++
	return nil
}

func inspectAttachmentStreamVersion(ctx context.Context, attachStore registryattach.AttachmentStore, storageKey string) (uint32, error) {
	rc, err := attachStore.Retrieve(ctx, storageKey)
	if err != nil {
		return 0, fmt.Errorf("retrieve attachment storage key %q: %w", storageKey, err)
	}
	defer rc.Close()
	header, hasMagic, err := dataencryption.ReadHeader(rc)
	if err != nil {
		return 0, fmt.Errorf("read MSEH header for attachment storage key %q: %w", storageKey, err)
	}
	if !hasMagic || header == nil {
		return 0, nil
	}
	return header.Version, nil
}

func rewriteAttachmentToV3Temp(ctx context.Context, attachStore registryattach.AttachmentStore, storageKey string, encSvc *dataencryption.Service, dst io.Writer) (int64, string, error) {
	rc, err := attachStore.Retrieve(ctx, storageKey)
	if err != nil {
		return 0, "", fmt.Errorf("retrieve attachment storage key %q: %w", storageKey, err)
	}
	defer rc.Close()

	plain, err := encSvc.DecryptStream(rc)
	if err != nil {
		return 0, "", fmt.Errorf("decrypt attachment storage key %q: %w", storageKey, err)
	}
	hasher := sha256.New()
	counting := &hashingReader{src: plain, hasher: hasher}

	encrypted, err := encSvc.EncryptStream(dst)
	if err != nil {
		return 0, "", fmt.Errorf("start v3 encryption for attachment storage key %q: %w", storageKey, err)
	}
	if _, err := io.Copy(encrypted, counting); err != nil {
		_ = encrypted.Close()
		return 0, "", fmt.Errorf("rewrite attachment storage key %q: %w", storageKey, err)
	}
	if err := encrypted.Close(); err != nil {
		return 0, "", fmt.Errorf("finalize v3 encryption for attachment storage key %q: %w", storageKey, err)
	}
	return counting.count, hex.EncodeToString(hasher.Sum(nil)), nil
}

type hashingReader struct {
	src    io.Reader
	hasher hash.Hash
	count  int64
}

func (r *hashingReader) Read(p []byte) (int, error) {
	n, err := r.src.Read(p)
	if n > 0 {
		_, _ = r.hasher.Write(p[:n])
		r.count += int64(n)
	}
	return n, err
}

func validSHA256Hex(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}
