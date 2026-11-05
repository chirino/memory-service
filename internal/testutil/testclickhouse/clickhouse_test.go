package testclickhouse

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

// Dependency updates bump the compose image; tests must run against the same server.
func TestImageMatchesCompose(t *testing.T) {
	root, err := repositoryRoot()
	require.NoError(t, err)
	compose, err := os.ReadFile(filepath.Join(root, "compose.yaml"))
	require.NoError(t, err)
	match := regexp.MustCompile(`image:\s*(clickhouse/clickhouse-server:\S+)`).FindSubmatch(compose)
	require.NotNil(t, match, "compose.yaml has no clickhouse/clickhouse-server image")
	require.Equal(t, string(match[1]), Image, "update testclickhouse.Image to match compose.yaml")
}
