package filesystem

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	"github.com/stretchr/testify/require"
)

func TestAttachmentStoreRoundTrip(t *testing.T) {
	t.Parallel()

	rootDir := t.TempDir()
	store := &AttachmentStore{rootDir: rootDir}

	result, err := store.Store(context.Background(), strings.NewReader("hello sqlite"), 1024, "text/plain")
	require.NoError(t, err)
	require.NotEmpty(t, result.StorageKey)
	require.Equal(t, int64(len("hello sqlite")), result.Size)
	require.Len(t, result.SHA256, 64)

	path, err := store.storagePath(result.StorageKey)
	require.NoError(t, err)
	_, err = os.Stat(path)
	require.NoError(t, err)

	reader, err := store.Retrieve(context.Background(), result.StorageKey)
	require.NoError(t, err)
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, "hello sqlite", string(body))

	require.NoError(t, store.Delete(context.Background(), result.StorageKey))
	_, err = os.Stat(path)
	require.Error(t, err)
	require.True(t, os.IsNotExist(err))
}

func TestAttachmentStoreRejectsInvalidStorageKey(t *testing.T) {
	t.Parallel()

	store := &AttachmentStore{rootDir: t.TempDir()}
	_, err := store.Retrieve(context.Background(), "../bad")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid storage key")
}

func TestLoadDerivesRootDirFromSQLiteDB(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	cfg := &config.Config{
		DBURL: filepath.Join(tempDir, "memory.db") + "?cache=shared",
	}

	store, err := load(config.WithContext(context.Background(), cfg))
	require.NoError(t, err)

	fsStore, ok := store.(*AttachmentStore)
	require.True(t, ok)
	require.Equal(t, filepath.Join(tempDir, "memory.db.attachments"), fsStore.rootDir)

	info, err := os.Stat(fsStore.rootDir)
	require.NoError(t, err)
	require.True(t, info.IsDir())
}
