package tempfiles

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCreateAndDeleteOnClose(t *testing.T) {
	dir := t.TempDir()

	f, err := Create(dir, "tempfiles-test-*")
	require.NoError(t, err)

	_, err = f.WriteString("hello")
	require.NoError(t, err)
	_, err = f.Seek(0, io.SeekStart)
	require.NoError(t, err)

	path := f.Name()
	rel, err := filepath.Rel(dir, path)
	require.NoError(t, err)
	require.NotContains(t, rel, "..")

	rc := NewDeleteOnClose(f)
	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, "hello", string(data))
	require.NoError(t, rc.Close())

	_, err = os.Stat(path)
	require.Error(t, err)
	require.True(t, os.IsNotExist(err))
}
