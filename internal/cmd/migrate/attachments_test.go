package migrate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/url"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/dataencryption"
	"github.com/chirino/memory-service/internal/model"
	dekpkg "github.com/chirino/memory-service/internal/plugin/encrypt/dek"
	registryattach "github.com/chirino/memory-service/internal/registry/attach"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const attachmentMigrationTestKeyHex = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

func TestMigrateAttachmentObjectRewritesV2ToV3(t *testing.T) {
	plaintext := []byte("legacy v2 attachment payload")
	svc := newAttachmentMigrationEncryptionService(t)
	store := &fakeMigratorAttachmentStore{data: encryptLegacyV2Attachment(t, plaintext)}
	item := migrationTestAttachment(plaintext)
	stats := &attachmentMigrationStats{}

	err := migrateAttachmentObject(context.Background(), item, "stored", store, store, svc, migrateAttachmentsOptions{
		TempDir: t.TempDir(),
	}, stats)
	require.NoError(t, err)
	require.Equal(t, 1, stats.Migrated)
	require.True(t, store.replaced)

	header, hasMagic, err := dataencryption.ReadHeader(bytes.NewReader(store.data))
	require.NoError(t, err)
	require.True(t, hasMagic)
	require.Equal(t, uint32(dataencryption.VersionAttachmentStreamAESGCM), header.Version)

	reader, err := svc.DecryptStream(bytes.NewReader(store.data))
	require.NoError(t, err)
	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, plaintext, got)
}

func TestMigrateAttachmentObjectDryRunDoesNotRequireReplacer(t *testing.T) {
	plaintext := []byte("legacy v2 dry-run payload")
	svc := newAttachmentMigrationEncryptionService(t)
	store := &fakeMigratorAttachmentStore{data: encryptLegacyV2Attachment(t, plaintext)}
	item := migrationTestAttachment(plaintext)
	stats := &attachmentMigrationStats{}

	err := migrateAttachmentObject(context.Background(), item, "stored", store, nil, svc, migrateAttachmentsOptions{
		DryRun:  true,
		TempDir: t.TempDir(),
	}, stats)
	require.NoError(t, err)
	require.False(t, store.replaced)
	require.Equal(t, 1, stats.DryRunWouldMigrate)
}

func TestMigrateAttachmentObjectRejectsMissingMetadata(t *testing.T) {
	plaintext := []byte("legacy v2 missing metadata payload")
	svc := newAttachmentMigrationEncryptionService(t)
	store := &fakeMigratorAttachmentStore{data: encryptLegacyV2Attachment(t, plaintext)}
	item := migrationTestAttachment(plaintext)
	item.SHA256 = nil
	stats := &attachmentMigrationStats{}

	err := migrateAttachmentObject(context.Background(), item, "stored", store, store, svc, migrateAttachmentsOptions{
		TempDir: t.TempDir(),
	}, stats)
	require.ErrorContains(t, err, "missing valid size/SHA-256 metadata")
	require.False(t, store.replaced)
	require.Equal(t, 1, stats.MissingMetadata)
}

func newAttachmentMigrationEncryptionService(t *testing.T) *dataencryption.Service {
	t.Helper()
	cfg := &config.Config{
		EncryptionProviders:                 "dek",
		EncryptionKey:                       attachmentMigrationTestKeyHex,
		EncryptionLegacyStreamV2ReadEnabled: true,
	}
	svc, err := dataencryption.New(context.Background(), cfg)
	require.NoError(t, err)
	return svc
}

func encryptLegacyV2Attachment(t *testing.T, plaintext []byte) []byte {
	t.Helper()
	key, err := config.DecodeEncryptionKey(attachmentMigrationTestKeyHex)
	require.NoError(t, err)
	nonce, err := dekpkg.NewCTRNonce(key)
	require.NoError(t, err)
	var out bytes.Buffer
	require.NoError(t, dataencryption.WriteHeader(&out, dataencryption.Header{
		Version:    dataencryption.VersionAESCTR,
		ProviderID: "dek",
		Nonce:      nonce,
	}))
	writer, err := dekpkg.NewCTREncryptWriter(&out, key, nonce)
	require.NoError(t, err)
	_, err = writer.Write(plaintext)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return out.Bytes()
}

func migrationTestAttachment(plaintext []byte) registrystore.AdminAttachment {
	size := int64(len(plaintext))
	sum := fmt.Sprintf("%x", sha256.Sum256(plaintext))
	storageKey := "stored"
	return registrystore.AdminAttachment{
		Attachment: model.Attachment{
			ID:          uuid.New(),
			StorageKey:  &storageKey,
			ContentType: "application/octet-stream",
			Size:        &size,
			SHA256:      &sum,
		},
	}
}

type fakeMigratorAttachmentStore struct {
	data     []byte
	replaced bool
}

func (s *fakeMigratorAttachmentStore) Store(context.Context, io.Reader, int64, string) (*registryattach.FileStoreResult, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *fakeMigratorAttachmentStore) Retrieve(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.data)), nil
}

func (s *fakeMigratorAttachmentStore) Delete(context.Context, string) error {
	return nil
}

func (s *fakeMigratorAttachmentStore) GetSignedURL(context.Context, string, time.Duration, *registryattach.SignedURLOptions) (*url.URL, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *fakeMigratorAttachmentStore) Replace(_ context.Context, storageKey string, data io.Reader, contentType string) (*registryattach.FileStoreResult, error) {
	buf, err := io.ReadAll(data)
	if err != nil {
		return nil, err
	}
	s.data = buf
	s.replaced = true
	return &registryattach.FileStoreResult{StorageKey: storageKey, Size: int64(len(buf))}, nil
}
