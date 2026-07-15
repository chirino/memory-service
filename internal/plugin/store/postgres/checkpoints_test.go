//go:build !nopostgresql

package postgres

import (
	"context"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/dataencryption"
	_ "github.com/chirino/memory-service/internal/plugin/encrypt/dek"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const checkpointTestKeyHex = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

func TestCheckpointValueEncryptionBindsClientID(t *testing.T) {
	store := &PostgresStore{enc: newCheckpointEncryptionService(t)}
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

func TestMemoryValueEncryptionBindsMemoryID(t *testing.T) {
	store := &postgresEpisodicStore{s: &PostgresStore{enc: newCheckpointEncryptionService(t)}}
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
