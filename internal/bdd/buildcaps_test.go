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

// sqliteTagFilter excludes @requires-sqlite-fts5 scenarios when the binary was built
// without FTS5 and always excludes @requires-embedded scenarios from these runners.
func sqliteTagFilter() string {
	var filters []string
	if !buildcaps.SQLiteFTS5 {
		filters = append(filters, "~@requires-sqlite-fts5")
	}
	filters = append(filters, "~@requires-embedded")
	return strings.Join(filters, " && ")
}
