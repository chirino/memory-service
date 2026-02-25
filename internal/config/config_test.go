package config

import (
	"os"
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
