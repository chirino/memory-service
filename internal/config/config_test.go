package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolvedTempDir_DefaultsToOSTempDir(t *testing.T) {
	var cfg Config
	require.Equal(t, os.TempDir(), cfg.ResolvedTempDir())
}

func TestResolvedTempDir_UsesConfiguredValue(t *testing.T) {
	cfg := Config{TempDir: " /tmp/custom-dir "}
	require.Equal(t, "/tmp/custom-dir", cfg.ResolvedTempDir())
}

func TestSQLiteFilePath_PlainFilename(t *testing.T) {
	cfg := Config{DBURL: "/tmp/example.db?cache=shared"}
	path, err := cfg.SQLiteFilePath()
	require.NoError(t, err)
	require.Equal(t, filepath.Clean("/tmp/example.db"), path)
}

func TestSQLiteFilePath_FileURI(t *testing.T) {
	cfg := Config{DBURL: "file:test.db?cache=shared"}
	path, err := cfg.SQLiteFilePath()
	require.NoError(t, err)
	require.Equal(t, filepath.Clean("test.db"), path)
}

func TestSQLiteFilePath_FileURIAbsolute(t *testing.T) {
	cfg := Config{DBURL: "file:/tmp/example.db?cache=shared"}
	path, err := cfg.SQLiteFilePath()
	require.NoError(t, err)
	require.Equal(t, filepath.Clean("/tmp/example.db"), path)
}

func TestSQLiteFilePath_RejectsMemory(t *testing.T) {
	for _, dsn := range []string{
		":memory:",
		"file::memory:?cache=shared",
		"file:test.db?mode=memory&cache=shared",
	} {
		cfg := Config{DBURL: dsn}
		_, err := cfg.SQLiteFilePath()
		require.Error(t, err, dsn)
	}
}

func TestResolvedAttachmentsFSDir_UsesOverride(t *testing.T) {
	cfg := Config{AttachFSDir: " /tmp/attachments "}
	dir, err := cfg.ResolvedAttachmentsFSDir()
	require.NoError(t, err)
	require.Equal(t, "/tmp/attachments", dir)
}

func TestResolvedAttachmentsFSDir_DerivesFromSQLiteDB(t *testing.T) {
	cfg := Config{DBURL: "file:/tmp/example.db?cache=shared"}
	dir, err := cfg.ResolvedAttachmentsFSDir()
	require.NoError(t, err)
	require.Equal(t, filepath.Clean("/tmp/example.db.attachments"), dir)
}
