//go:build !nosqlite

package sqlite

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sqliteSchemaPath = "db/schema.sql"

// TestSQLiteSchemaMemoryKindNotNull verifies that the fresh-only kind invariant
// is encoded in the SQLite schema: the memories.memory_kind column is NOT NULL,
// and the memory_vectors.memory_kind column is NOT NULL.
func TestSQLiteSchemaMemoryKindNotNull(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile(sqliteSchemaPath)
	require.NoError(t, err, "reading schema.sql")
	schema := string(src)

	// memories.memory_kind must be NOT NULL.
	assert.Contains(t, schema, "memory_kind TEXT NOT NULL",
		"memories.memory_kind must be NOT NULL in SQLite schema")

	// memory_vectors.memory_kind must be NOT NULL.
	assert.Contains(t, schema, "memory_kind TEXT NOT NULL,  -- canonical schema name at time of indexing",
		"memory vectors must require a non-defaulted kind")
	assert.Contains(t, schema, "memory_revision INTEGER NOT NULL CHECK (memory_revision > 0)",
		"memory vectors must require an unambiguous positive revision")
}

// TestSQLiteSchemaKindVersionsDefinedBeforeMemoriesForeignKey verifies that the
// referenced versions table is defined before the memories table. The migrator
// seeds the Go-embedded default/v1 definition after executing this DDL.
func TestSQLiteSchemaKindVersionsDefinedBeforeMemoriesForeignKey(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile(sqliteSchemaPath)
	require.NoError(t, err, "reading schema.sql")
	schema := string(src)

	versionsIdx := strings.Index(schema, "CREATE TABLE IF NOT EXISTS memory_kind_versions")
	memoriesIdx := strings.Index(schema, "CREATE TABLE IF NOT EXISTS memories (")

	require.True(t, versionsIdx >= 0, "memory_kind_versions table must be present in schema.sql")
	require.True(t, memoriesIdx >= 0, "CREATE TABLE memories must be present in schema.sql")

	assert.Less(t, versionsIdx, memoriesIdx,
		"memory_kind_versions must be defined before the memories foreign key")
}

// TestSQLiteSchemaMemoryKindIndex verifies the composite index exists to support
// migration scans and kind-filtered reads without a full table scan.
func TestSQLiteSchemaMemoryKindIndex(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile(sqliteSchemaPath)
	require.NoError(t, err)
	assert.Contains(t, string(src), "ON memories (memory_kind, created_at, created_at_ns, id)",
		"migration cursor index must match (memory_kind, created_at, created_at_ns, id) ordering")
}

func TestSQLiteMigrationScanPlanUsesCursorIndexWithoutTemporarySort(t *testing.T) {
	store, ctx := newTestSQLiteEpisodicStore(t)
	concrete := store.(*sqliteEpisodicStore)
	type planRow struct {
		Detail string `gorm:"column:detail"`
	}
	var plan []planRow
	require.NoError(t, store.InReadTx(ctx, func(txCtx context.Context) error {
		return concrete.dbFor(txCtx).Raw(`
			EXPLAIN QUERY PLAN
			SELECT id, created_at, created_at_ns FROM memories
			WHERE memory_kind = ? AND ((created_at,created_at_ns) > (?, ?) OR ((created_at,created_at_ns) = (?, ?) AND id > ?))
			ORDER BY created_at ASC, created_at_ns ASC, id ASC LIMIT ?`,
			"default/v1", time.Unix(0, 0), time.Unix(0, 0), "00000000-0000-0000-0000-000000000000", 50,
		).Scan(&plan).Error
	}))

	details := make([]string, 0, len(plan))
	for _, row := range plan {
		details = append(details, row.Detail)
	}
	joined := strings.Join(details, "\n")
	assert.Contains(t, joined, "memories_kind_cursor_idx", joined)
	assert.NotContains(t, joined, "USE TEMP B-TREE", joined)
}
