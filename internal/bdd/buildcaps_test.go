package bdd

import (
	"strings"
	"testing"

	"github.com/chirino/memory-service/internal/buildcaps"
)

func requireCapabilities(t *testing.T, missing ...string) {
	t.Helper()
	if len(missing) == 0 {
		return
	}
	t.Skipf("required build capabilities missing: %s", strings.Join(missing, ", "))
}

func sqliteTagFilter() string {
	if buildcaps.SQLiteFTS5 {
		return ""
	}
	return "~@requires-sqlite-fts5"
}
