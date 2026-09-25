//go:build !nosqlite

package sqlite

import (
	"context"
	"encoding/base64"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/dataencryption"
	_ "github.com/chirino/memory-service/internal/plugin/encrypt/dek"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const checkpointTestKeyHex = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

func TestCheckpointValueEncryptionBindsClientID(t *testing.T) {
	store := &SQLiteStore{enc: newCheckpointEncryptionService(t)}
	value := []byte(`{"cursor":"abc"}`)

	ciphertext, err := store.encryptCheckpointValue("client-a", value)
	require.NoError(t, err)
	require.True(t, dataencryption.HasMagic(ciphertext))

	got, err := store.decryptCheckpointValue("client-a", ciphertext)
	require.NoError(t, err)
	require.Equal(t, value, got)

	_, err = store.decryptCheckpointValue("client-b", ciphertext)
	require.Error(t, err)
}

func TestCheckpointLeaseFencesWritersAndUsesCAS(t *testing.T) {
	cfg := &config.Config{DatastoreType: "sqlite", DBURL: filepath.Join(t.TempDir(), "checkpoints.db"), DatastoreMigrateAtStart: true, EncryptionDBDisabled: true}
	ctx := config.WithContext(context.Background(), cfg)
	require.NoError(t, (&sqliteMigrator{}).Migrate(ctx))
	handle, err := getSharedHandle(ctx)
	require.NoError(t, err)
	store := &SQLiteStore{handle: handle, db: handle.db, cfg: cfg}

	initial, err := checkpointWrite(ctx, store, func(txCtx context.Context) (*registrystore.ClientCheckpoint, error) {
		return store.AdminPutCheckpoint(txCtx, registrystore.ClientCheckpoint{ClientID: "exporter", ContentType: "application/json", Value: []byte(`{"cursor":"one"}`)})
	})
	require.NoError(t, err)
	require.NotEmpty(t, initial.Revision)
	tokenA := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	tokenBBytes := make([]byte, 32)
	tokenBBytes[0] = 1
	tokenB := base64.RawURLEncoding.EncodeToString(tokenBBytes)

	lease, err := checkpointWrite(ctx, store, func(txCtx context.Context) (*registrystore.ClientCheckpointLease, error) {
		return store.AdminAcquireCheckpointLease(txCtx, "exporter", tokenA, 30*time.Second)
	})
	require.NoError(t, err)
	require.Equal(t, uint64(1), lease.Generation)
	_, err = checkpointWrite(ctx, store, func(txCtx context.Context) (*registrystore.ClientCheckpointLease, error) {
		return store.AdminAcquireCheckpointLease(txCtx, "exporter", tokenB, 30*time.Second)
	})
	requireCheckpointConflict(t, err)

	_, err = checkpointWrite(ctx, store, func(txCtx context.Context) (*registrystore.ClientCheckpoint, error) {
		return store.AdminPutCheckpoint(txCtx, registrystore.ClientCheckpoint{ClientID: "exporter", ContentType: "application/json", Value: []byte(`{"cursor":"legacy"}`)})
	})
	requireCheckpointConflict(t, err)
	_, err = checkpointWrite(ctx, store, func(txCtx context.Context) (*registrystore.ClientCheckpoint, error) {
		return store.AdminPutCheckpointCAS(txCtx, registrystore.CheckpointCASWrite{
			Checkpoint:       registrystore.ClientCheckpoint{ClientID: "exporter", ContentType: "application/json", Value: []byte(`{"cursor":"wrong"}`)},
			ExpectedRevision: registrystore.EncodeCheckpointRevision(99), LeaseToken: tokenA,
		})
	})
	requireCheckpointConflict(t, err)

	updated, err := checkpointWrite(ctx, store, func(txCtx context.Context) (*registrystore.ClientCheckpoint, error) {
		return store.AdminPutCheckpointCAS(txCtx, registrystore.CheckpointCASWrite{
			Checkpoint:       registrystore.ClientCheckpoint{ClientID: "exporter", ContentType: "application/json", Value: []byte(`{"cursor":"two"}`)},
			ExpectedRevision: initial.Revision, LeaseToken: tokenA,
		})
	})
	require.NoError(t, err)
	require.NotEqual(t, initial.Revision, updated.Revision)
	renewed, err := checkpointWrite(ctx, store, func(txCtx context.Context) (*registrystore.ClientCheckpointLease, error) {
		return store.AdminRenewCheckpointLease(txCtx, "exporter", tokenA, 30*time.Second)
	})
	require.NoError(t, err)
	require.Equal(t, lease.Generation, renewed.Generation)
	_, err = checkpointWrite(ctx, store, func(txCtx context.Context) (*registrystore.ClientCheckpointLease, error) {
		return store.AdminRenewCheckpointLease(txCtx, "exporter", tokenB, 30*time.Second)
	})
	requireCheckpointConflict(t, err)
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		return store.AdminReleaseCheckpointLease(txCtx, "exporter", tokenA)
	}))

	legacy, err := checkpointWrite(ctx, store, func(txCtx context.Context) (*registrystore.ClientCheckpoint, error) {
		return store.AdminPutCheckpoint(txCtx, registrystore.ClientCheckpoint{ClientID: "exporter", ContentType: "application/json", Value: []byte(`{"cursor":"three"}`)})
	})
	require.NoError(t, err)
	require.NotEqual(t, updated.Revision, legacy.Revision)

	require.NoError(t, handle.db.Exec(`UPDATE admin_checkpoints SET lease_expires_at = datetime('now', '-1 second'), lease_token_hash = ? WHERE client_id = ?`, []byte("expired"), "exporter").Error)
	newLease, err := checkpointWrite(ctx, store, func(txCtx context.Context) (*registrystore.ClientCheckpointLease, error) {
		return store.AdminAcquireCheckpointLease(txCtx, "exporter", tokenB, 30*time.Second)
	})
	require.NoError(t, err)
	require.Equal(t, uint64(2), newLease.Generation)
}

func checkpointWrite[T any](ctx context.Context, store *SQLiteStore, operation func(context.Context) (T, error)) (T, error) {
	var value T
	err := store.InWriteTx(ctx, func(txCtx context.Context) error {
		var err error
		value, err = operation(txCtx)
		return err
	})
	return value, err
}

func requireCheckpointConflict(t *testing.T, err error) {
	t.Helper()
	var conflict *registrystore.ConflictError
	require.True(t, errors.As(err, &conflict), "expected checkpoint conflict, got %v", err)
}

func TestMemoryValueEncryptionBindsMemoryID(t *testing.T) {
	store := &sqliteEpisodicStore{s: &SQLiteStore{enc: newCheckpointEncryptionService(t)}}
	memoryID := uuid.New()
	value := []byte(`{"memory":"abc"}`)

	ciphertext, err := store.encryptMemoryValue(memoryID, value)
	require.NoError(t, err)
	require.True(t, dataencryption.HasMagic(ciphertext))

	got, err := store.decryptMemoryValue(memoryID, ciphertext)
	require.NoError(t, err)
	require.Equal(t, value, got)

	_, err = store.decryptMemoryValue(uuid.New(), ciphertext)
	require.Error(t, err)
}

func newCheckpointEncryptionService(t *testing.T) *dataencryption.Service {
	t.Helper()
	svc, err := dataencryption.New(context.Background(), &config.Config{
		EncryptionProviders: "dek",
		EncryptionKey:       checkpointTestKeyHex,
	})
	require.NoError(t, err)
	return svc
}
