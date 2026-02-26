package pgstore

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/url"
	"os"
	"time"

	"github.com/chirino/memory-service/internal/config"
	registryattach "github.com/chirino/memory-service/internal/registry/attach"
	"github.com/chirino/memory-service/internal/tempfiles"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func init() {
	registryattach.Register(registryattach.Plugin{
		Name:   "postgres",
		Loader: load,
	})
}

func load(ctx context.Context) (registryattach.AttachmentStore, error) {
	cfg := config.FromContext(ctx)
	if cfg == nil {
		return nil, fmt.Errorf("pgstore: missing config in context")
	}
	db, err := gorm.Open(postgres.Open(cfg.DBURL), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		return nil, fmt.Errorf("pgstore: %w", err)
	}
	// Migrate legacy chunk table for backward-compat reads of old Go-stored attachments.
	if err := db.AutoMigrate(&fileChunkRecord{}); err != nil {
		return nil, fmt.Errorf("pgstore: auto-migrate attachment_file_chunks: %w", err)
	}
	return &PgAttachmentStore{db: db, tempDir: cfg.ResolvedTempDir()}, nil
}

type PgAttachmentStore struct {
	db      *gorm.DB
	tempDir string
}

// fileChunkRecord is kept only for backward-compat reads of attachments written by older Go versions.
type fileChunkRecord struct {
	StorageKey string    `gorm:"column:storage_key;type:uuid;primaryKey"`
	Seq        int       `gorm:"column:seq;primaryKey"`
	Data       []byte    `gorm:"column:data;type:bytea;not null"`
	CreatedAt  time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (fileChunkRecord) TableName() string { return "attachment_file_chunks" }

// Store buffers the upload to a temp file then writes it to a PostgreSQL LargeObject,
// matching Java's DatabaseFileStore behaviour. Returns the numeric OID as the storage key.
func (s *PgAttachmentStore) Store(ctx context.Context, data io.Reader, maxSize int64, contentType string) (*registryattach.FileStoreResult, error) {
	const chunkSize = 8192

	// Buffer to a temp file first (enforces maxSize, computes hash).
	tmp, err := tempfiles.Create(s.tempDir, "memory-service-pg-upload-*")
	if err != nil {
		return nil, fmt.Errorf("pgstore: create temp file: %w", err)
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	hasher := sha256.New()
	limited := io.LimitReader(data, maxSize+1)
	total, err := io.Copy(io.MultiWriter(tmp, hasher), limited)
	if err != nil {
		return nil, fmt.Errorf("pgstore: buffer upload: %w", err)
	}
	if total > maxSize {
		return nil, fmt.Errorf("file exceeds maximum size of %d bytes", maxSize)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("pgstore: rewind temp file: %w", err)
	}

	// Write temp file into a PostgreSQL LargeObject within a single transaction.
	var oid int64
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Create a new LargeObject and get its OID.
		if err := tx.Raw("SELECT lo_create(0)").Scan(&oid).Error; err != nil {
			return fmt.Errorf("pgstore: lo_create: %w", err)
		}

		// Write data in chunks using lo_put(loid, offset, data).
		buf := make([]byte, chunkSize)
		offset := int64(0)
		for {
			n, readErr := tmp.Read(buf)
			if n > 0 {
				chunk := buf[:n]
				if err := tx.Exec("SELECT lo_put(?, ?, ?)", oid, offset, chunk).Error; err != nil {
					return fmt.Errorf("pgstore: lo_put at offset %d: %w", offset, err)
				}
				offset += int64(n)
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				return fmt.Errorf("pgstore: read upload buffer: %w", readErr)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &registryattach.FileStoreResult{
		StorageKey: fmt.Sprintf("%d", oid),
		Size:       total,
		SHA256:     fmt.Sprintf("%x", hasher.Sum(nil)),
	}, nil
}

// Retrieve fetches attachment data. Numeric storage keys are Java/Go LargeObjects stored via
// lo_put; UUID keys fall back to the legacy attachment_file_chunks table written by older Go code.
func (s *PgAttachmentStore) Retrieve(ctx context.Context, storageKey string) (io.ReadCloser, error) {
	// Determine storage type by key format: numeric = LargeObject, UUID = legacy chunks.
	isLargeObject := true
	for _, c := range storageKey {
		if c == '-' {
			isLargeObject = false
			break
		}
	}
	if isLargeObject {
		return s.retrieveLargeObject(ctx, storageKey)
	}
	return s.retrieveChunks(ctx, storageKey)
}

// retrieveLargeObject reads from pg_largeobject using the numeric OID storage key.
func (s *PgAttachmentStore) retrieveLargeObject(ctx context.Context, storageKey string) (io.ReadCloser, error) {
	tmp, err := tempfiles.Create(s.tempDir, "memory-service-pg-lo-*")
	if err != nil {
		return nil, fmt.Errorf("pgstore: create temp file: %w", err)
	}
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}

	rows, err := s.db.WithContext(ctx).Raw(
		"SELECT data FROM pg_largeobject WHERE loid = ? ORDER BY pageno ASC", storageKey,
	).Rows()
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("attachment not found: %s", storageKey)
	}
	defer rows.Close()

	found := false
	for rows.Next() {
		found = true
		var data []byte
		if err := rows.Scan(&data); err != nil {
			cleanup()
			return nil, fmt.Errorf("pgstore: decode large object page: %w", err)
		}
		if _, err := tmp.Write(data); err != nil {
			cleanup()
			return nil, fmt.Errorf("pgstore: spool large object page: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		cleanup()
		return nil, fmt.Errorf("pgstore: iterate large object pages: %w", err)
	}
	if !found {
		cleanup()
		return nil, fmt.Errorf("attachment not found: %s", storageKey)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, fmt.Errorf("pgstore: rewind temp file: %w", err)
	}
	return tempfiles.NewDeleteOnClose(tmp), nil
}

// retrieveChunks reads legacy Go-written attachments from attachment_file_chunks (UUID key).
func (s *PgAttachmentStore) retrieveChunks(ctx context.Context, storageKey string) (io.ReadCloser, error) {
	tmp, err := tempfiles.Create(s.tempDir, "memory-service-pg-attachment-*")
	if err != nil {
		return nil, fmt.Errorf("pgstore: create temp file: %w", err)
	}
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}

	rows, err := s.db.WithContext(ctx).
		Model(&fileChunkRecord{}).
		Where("storage_key = ?", storageKey).
		Order("seq ASC").
		Rows()
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("attachment not found: %s", storageKey)
	}
	defer rows.Close()

	found := false
	for rows.Next() {
		found = true
		var rec fileChunkRecord
		if err := s.db.ScanRows(rows, &rec); err != nil {
			cleanup()
			return nil, fmt.Errorf("pgstore: decode chunk: %w", err)
		}
		if _, err := tmp.Write(rec.Data); err != nil {
			cleanup()
			return nil, fmt.Errorf("pgstore: spool chunk: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		cleanup()
		return nil, fmt.Errorf("pgstore: iterate chunks: %w", err)
	}
	if !found {
		cleanup()
		return nil, fmt.Errorf("attachment not found: %s", storageKey)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, fmt.Errorf("pgstore: rewind temp file: %w", err)
	}
	return tempfiles.NewDeleteOnClose(tmp), nil
}

func (s *PgAttachmentStore) Delete(ctx context.Context, storageKey string) error {
	// UUID key = legacy chunk table.
	for _, c := range storageKey {
		if c == '-' {
			return s.db.WithContext(ctx).Where("storage_key = ?", storageKey).Delete(&fileChunkRecord{}).Error
		}
	}
	// Numeric key = LargeObject.
	return s.db.WithContext(ctx).Exec("SELECT lo_unlink(?)", storageKey).Error
}

func (s *PgAttachmentStore) GetSignedURL(_ context.Context, _ string, _ time.Duration) (*url.URL, error) {
	return nil, fmt.Errorf("signed URLs not supported for postgres attachment store")
}
