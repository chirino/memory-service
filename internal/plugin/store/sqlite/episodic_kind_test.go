//go:build !nosqlite

package sqlite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	coreepisodic "github.com/chirino/memory-service/internal/episodic"
	"github.com/chirino/memory-service/internal/model"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestMemoryKindFileImportIsIdempotentAndImmutable(t *testing.T) {
	store, ctx := newTestSQLiteEpisodicStore(t)
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "nested"), 0o700))
	manifestPath := filepath.Join(dir, "nested", "notes.yml")
	manifest := `kind: memory-kind
name: notes/v1
attributes:
  tenant: string
projectionRego: |
  package memories.attributes
  attributes := {"tenant": input.value.tenant}
writable: true
`
	require.NoError(t, os.WriteFile(manifestPath, []byte(manifest), 0o600))
	require.NoError(t, coreepisodic.ImportKindVersions(ctx, store, dir))
	require.NoError(t, coreepisodic.ImportKindVersions(ctx, store, dir), "identical startup import must be idempotent")

	conflicting := strings.Replace(manifest, `writable: true`, `writable: false`, 1)
	require.NoError(t, os.WriteFile(manifestPath, []byte(conflicting), 0o600))
	require.NoError(t, coreepisodic.ImportKindVersions(ctx, store, dir), "a conflict is logged, not fatal")

	require.NoError(t, store.InReadTx(ctx, func(txCtx context.Context) error {
		version, err := store.GetMemoryKindVersion(txCtx, "notes/v1")
		require.NoError(t, err)
		require.True(t, version.Writable, "conflicting file must not overwrite immutable database content")
		return nil
	}))
}

func TestMemoryKindFileImportIgnoresOtherYAMLDocumentsAndJSON(t *testing.T) {
	store, ctx := newTestSQLiteEpisodicStore(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "other.yaml"),
		[]byte("kind: another-policy-type\nname: ignored/v1\nunknownField: allowed\n"),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "missing-kind.yml"),
		[]byte("name: ignored-missing-kind/v1\n"),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "legacy.memory-kind.json"),
		[]byte(`{"kind":"memory-kind","name":"ignored-json/v1","attributes":{}}`),
		0o600,
	))
	require.NoError(t, coreepisodic.ImportKindVersions(ctx, store, dir))
	require.NoError(t, store.InReadTx(ctx, func(txCtx context.Context) error {
		for _, name := range []string{"ignored/v1", "ignored-missing-kind/v1", "ignored-json/v1"} {
			version, err := store.GetMemoryKindVersion(txCtx, name)
			require.NoError(t, err)
			require.Nil(t, version, name)
		}
		return nil
	}))
}

func TestMemoryKindImportPathSupportsFilesAndDirectories(t *testing.T) {
	store, ctx := newTestSQLiteEpisodicStore(t)
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "nested"), 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "nested", "directory-kind.yaml"),
		[]byte("kind: memory-kind\nname: directory-kind/v1\nattributes: {}\n"),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "ignored-extension.json"),
		[]byte(`{"kind":"memory-kind","name":"ignored-extension/v1","attributes":{}}`),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "ignored-kind.yml"),
		[]byte("kind: another-policy-type\nname: ignored-kind/v1\n"),
		0o600,
	))

	explicitFile := filepath.Join(t.TempDir(), "explicit,kind.policy")
	require.NoError(t, os.WriteFile(
		explicitFile,
		[]byte("kind: memory-kind\nname: explicit-kind/v1\nattributes: {}\n"),
		0o600,
	))
	quotedExplicitFile := fmt.Sprintf("%q", explicitFile)
	require.NoError(t, coreepisodic.ImportKindVersions(ctx, store, dir+","+quotedExplicitFile))

	require.NoError(t, store.InReadTx(ctx, func(txCtx context.Context) error {
		for _, name := range []string{"directory-kind/v1", "explicit-kind/v1"} {
			version, err := store.GetMemoryKindVersion(txCtx, name)
			require.NoError(t, err)
			require.NotNil(t, version, name)
		}
		for _, name := range []string{"ignored-extension/v1", "ignored-kind/v1"} {
			version, err := store.GetMemoryKindVersion(txCtx, name)
			require.NoError(t, err)
			require.Nil(t, version, name)
		}
		return nil
	}))
}

func TestMemoryKindFileImportStrictlyDecodesMatchingYAML(t *testing.T) {
	store, ctx := newTestSQLiteEpisodicStore(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "invalid.yaml"),
		[]byte("kind: memory-kind\nname: invalid/v1\nattributes: {}\nunknownField: rejected\n"),
		0o600,
	))
	err := coreepisodic.ImportKindVersions(ctx, store, dir)
	require.ErrorContains(t, err, `unknown field "unknownField"`)
}

func TestDefaultCognitionManifestImports(t *testing.T) {
	store, ctx := newTestSQLiteEpisodicStore(t)
	dir := filepath.Join("..", "..", "..", "..", "deploy", "episodic-policies")
	require.NoError(t, coreepisodic.ImportKindVersions(ctx, store, dir))
	require.NoError(t, store.InReadTx(ctx, func(txCtx context.Context) error {
		version, err := store.GetMemoryKindVersion(txCtx, "cognition/v1")
		require.NoError(t, err)
		require.NotNil(t, version)
		return nil
	}))
}

func TestMigrationContinuationTaskIdentityIsIdempotent(t *testing.T) {
	store, ctx := newTestSQLiteEpisodicStore(t)
	body := map[string]interface{}{
		"migration_id": uuid.New().String(),
		"after_id":     uuid.New().String(),
		"taskName":     "migration:test:after:123:cursor",
	}
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		if err := store.CreateMemoryKindMigrationTask(txCtx, body); err != nil {
			return err
		}
		return store.CreateMemoryKindMigrationTask(txCtx, body)
	}))
	db, _, err := SharedDB(ctx)
	require.NoError(t, err)
	var count int64
	require.NoError(t, db.Table("tasks").Where("task_name = ?", body["taskName"]).Count(&count).Error)
	require.Equal(t, int64(1), count)
}

// newTestSQLiteEpisodicStore creates a fresh in-memory-backed SQLite episodic store.
// It returns the store and a background context.
func newTestSQLiteEpisodicStore(t *testing.T) (registryepisodic.EpisodicStore, context.Context) {
	t.Helper()
	cfg := &config.Config{
		DatastoreType:           "sqlite",
		DBURL:                   filepath.Join(t.TempDir(), "test.db"),
		DatastoreMigrateAtStart: true,
		EncryptionDBDisabled:    true,
	}
	ctx := config.WithContext(context.Background(), cfg)
	require.NoError(t, (&sqliteMigrator{}).Migrate(ctx))
	loader, err := registryepisodic.Select("sqlite")
	require.NoError(t, err)
	store, err := loader(ctx)
	require.NoError(t, err)
	return store, ctx
}

// --- Defect 1: SQLite migration store calls must be inside InReadTx/InWriteTx ---

// TestSQLiteMigrationStoreRequiresTxScope verifies that SQLite episodic kind store
// methods that use dbFor/writeDBFor panic when called outside a transaction scope.
// FindMemoriesToMigrateByKind and CountMemoriesPendingIndexByKind (the core migration
// scan methods) use dbFor and therefore panic — this validates that the task processor
// wraps these calls in InReadTx (defect 1 fix).
func TestSQLiteMigrationStoreRequiresTxScope(t *testing.T) {
	t.Parallel()
	store, ctx := newTestSQLiteEpisodicStore(t)

	// FindMemoriesToMigrateByKind uses dbFor — must panic outside tx scope.
	require.Panics(t, func() {
		_, _ = store.FindMemoriesToMigrateByKind(ctx, "default/v1", nil, time.Time{}, uuid.Nil, 10)
	})

	// CountMemoriesPendingIndexByKind uses dbFor — must panic outside tx scope.
	require.Panics(t, func() {
		_, _ = store.CountMemoriesPendingIndexByKind(ctx, "default/v1", nil)
	})

	// MigrateOneMemoryKindCAS uses writeDBFor — must panic outside tx scope.
	require.Panics(t, func() {
		_ = store.MigrateOneMemoryKindCAS(ctx, uuid.Nil, "default/v1", 0, nil, "events/v1")
	})
}

// TestSQLiteMigrationStoreWorksInsideTxScope ensures the operations succeed when
// correctly wrapped in InReadTx / InWriteTx (confirms the defect 1 fix compiles
// and runs end-to-end).
func TestSQLiteMigrationStoreWorksInsideTxScope(t *testing.T) {
	t.Parallel()
	store, ctx := newTestSQLiteEpisodicStore(t)

	// GetMemoryKindVersion inside read tx must not panic.
	var sv *model.MemoryKindVersion
	require.NoError(t, store.InReadTx(ctx, func(rCtx context.Context) error {
		var err error
		sv, err = store.GetMemoryKindVersion(rCtx, "default/v1")
		return err
	}))
	// default/v1 is the built-in default; we may or may not find it, but no panic.
	_ = sv

	// FindMemoriesToMigrateByKind inside read tx must not panic.
	var candidates []registryepisodic.MigrationCandidate
	require.NoError(t, store.InReadTx(ctx, func(rCtx context.Context) error {
		var err error
		candidates, err = store.FindMemoriesToMigrateByKind(rCtx, "default/v1", nil, time.Time{}, uuid.Nil, 10)
		return err
	}))
	require.NotNil(t, candidates)

	// CountMemoriesPendingIndexByKind inside read tx must not panic.
	var pendingCount int64
	require.NoError(t, store.InReadTx(ctx, func(rCtx context.Context) error {
		var err error
		pendingCount, err = store.CountMemoriesPendingIndexByKind(rCtx, "default/v1", nil)
		return err
	}))
	require.Equal(t, int64(0), pendingCount)
}

func TestSQLiteTombstoneInvalidatesInflightKindMigration(t *testing.T) {
	store, ctx := newTestSQLiteEpisodicStore(t)
	ns := []string{"user", "alice", "tombstone-race"}
	var memoryID uuid.UUID
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		if _, err := store.CreateMemoryKindVersion(txCtx, model.MemoryKindVersion{
			Name: "target/v1", AttributeTypes: map[string]string{}, Writable: true, CreatedAt: time.Now().UTC(),
		}); err != nil {
			return err
		}
		result, err := store.PutMemory(txCtx, registryepisodic.PutMemoryRequest{
			Namespace: ns, Key: "secret", Value: map[string]interface{}{"secret": "ciphertext-source"},
			PolicyAttributes: map[string]interface{}{"secret_projection": "plaintext"}, MemoryKind: "default/v1",
		})
		if err != nil {
			return err
		}
		memoryID = result.ID
		return store.ArchiveMemory(txCtx, ns, "secret", nil)
	}))

	// Simulate vector reconciliation completing so the evictor may tombstone it.
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		db, err := requireScope(txCtx, "prepare tombstone race")
		if err != nil {
			return err
		}
		return db.Exec("UPDATE memories SET indexed_at = ? WHERE id = ?", time.Now().UTC(), memoryID.String()).Error
	}))

	var candidate registryepisodic.MigrationCandidate
	require.NoError(t, store.InReadTx(ctx, func(txCtx context.Context) error {
		rows, err := store.FindMemoriesToMigrateByKind(txCtx, "default/v1", ns, time.Time{}, uuid.Nil, 10)
		if err != nil {
			return err
		}
		require.Len(t, rows, 1)
		candidate = rows[0]
		require.NotEmpty(t, candidate.ValueEncrypted)
		return nil
	}))

	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		lifecycle := store.(registryepisodic.BackgroundMemoryLifecycleStore)
		changes, err := lifecycle.TombstoneDeletedMemoriesWithChanges(txCtx, 10)
		require.Len(t, changes, 1)
		require.Equal(t, memoryID, changes[0].ID)
		require.Equal(t, "default/v1", changes[0].MemoryKind)
		require.Equal(t, candidate.Revision+1, changes[0].Revision)
		return err
	}))
	require.ErrorIs(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		return store.MigrateOneMemoryKindCAS(txCtx, candidate.ID, candidate.MemoryKind, candidate.Revision,
			map[string]interface{}{"secret_projection": "repopulated"}, "target/v1")
	}), registryepisodic.ErrMemoryRevisionConflict)

	require.NoError(t, store.InReadTx(ctx, func(txCtx context.Context) error {
		item, err := store.GetMemory(txCtx, ns, "secret", registryepisodic.ArchiveFilterOnly)
		require.NoError(t, err)
		require.Nil(t, item.Value)
		require.Empty(t, item.Attributes)
		require.Equal(t, "default/v1", item.MemoryKind)
		require.Equal(t, candidate.Revision+1, item.Revision)
		return nil
	}))
}

func TestSQLiteArchivedOnlySearchUsesLatestArchivedRevision(t *testing.T) {
	store, ctx := newTestSQLiteEpisodicStore(t)
	ns := []string{"user", "alice", "archive-selector"}
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		if _, err := store.PutMemory(txCtx, registryepisodic.PutMemoryRequest{
			Namespace: ns, Key: "same-key", Value: map[string]interface{}{"generation": "archived"},
			PolicyAttributes: map[string]interface{}{"generation": "archived"}, MemoryKind: "default/v1",
		}); err != nil {
			return err
		}
		if err := store.ArchiveMemory(txCtx, ns, "same-key", nil); err != nil {
			return err
		}
		_, err := store.PutMemory(txCtx, registryepisodic.PutMemoryRequest{
			Namespace: ns, Key: "same-key", Value: map[string]interface{}{"generation": "active"},
			PolicyAttributes: map[string]interface{}{"generation": "active"}, MemoryKind: "default/v1",
		})
		return err
	}))

	require.NoError(t, store.InReadTx(ctx, func(txCtx context.Context) error {
		for archiveMode, wantGeneration := range map[registryepisodic.ArchiveFilter]string{
			registryepisodic.ArchiveFilterExclude: "active",
			registryepisodic.ArchiveFilterInclude: "active",
			registryepisodic.ArchiveFilterOnly:    "archived",
		} {
			direct, err := store.GetMemory(txCtx, ns, "same-key", archiveMode)
			require.NoError(t, err)
			items, err := store.SearchMemories(txCtx, registryepisodic.MemorySearchQuery{
				NamespacePrefix: ns, Limit: 10, Archived: archiveMode,
			})
			require.NoError(t, err)
			require.Len(t, items, 1, archiveMode)
			require.Equal(t, direct.ID, items[0].ID, archiveMode)
			require.Equal(t, wantGeneration, items[0].Attributes["generation"], archiveMode)
			namespaces, err := store.ListNamespaces(txCtx, registryepisodic.ListNamespacesRequest{
				Prefix: []string{"user", "alice"}, Archived: archiveMode,
			})
			require.NoError(t, err)
			require.Equal(t, [][]string{ns}, namespaces, archiveMode)
		}
		return nil
	}))
}

// --- Defect 10: CountMemoriesPendingIndexByKind returns correct dynamic count ---

func TestCountMemoriesPendingIndexByKindAfterMigration(t *testing.T) {
	t.Parallel()
	store, ctx := newTestSQLiteEpisodicStore(t)

	ns := []string{"users", "test-pending"}
	targetKind := "events/v1"

	// Seed a schema version.
	sv := model.MemoryKindVersion{
		Name:           targetKind,
		AttributeTypes: map[string]string{"score": "number"},
		Writable:       true,
		CreatedAt:      time.Now().UTC(),
	}
	require.NoError(t, store.InWriteTx(ctx, func(wCtx context.Context) error {
		_, err := store.CreateMemoryKindVersion(wCtx, sv)
		return err
	}))

	// Write a memory using the default kind.
	var memID uuid.UUID
	var rev int64
	require.NoError(t, store.InWriteTx(ctx, func(wCtx context.Context) error {
		res, err := store.PutMemory(wCtx, registryepisodic.PutMemoryRequest{
			Namespace:  ns,
			Key:        "key1",
			Value:      map[string]interface{}{"x": 1},
			MemoryKind: "default/v1",
		})
		if err != nil {
			return err
		}
		memID = res.ID
		rev = res.Revision
		return nil
	}))

	// Initially indexed_at is NULL (just written, not indexed yet).
	var count int64
	require.NoError(t, store.InReadTx(ctx, func(rCtx context.Context) error {
		var err error
		count, err = store.CountMemoriesPendingIndexByKind(rCtx, "default/v1", ns)
		return err
	}))
	require.Equal(t, int64(1), count, "row should be pending for default/v1 schema before migration")

	// Simulate MigrateOneMemoryKindCAS: moves the row from default/v1 to targetKind.
	require.NoError(t, store.InWriteTx(ctx, func(wCtx context.Context) error {
		return store.MigrateOneMemoryKindCAS(wCtx, memID, "default/v1", rev,
			map[string]interface{}{"score": 42.0}, targetKind)
	}))

	// After migration the row has targetKind and indexed_at IS NULL (cleared by CAS).
	require.NoError(t, store.InReadTx(ctx, func(rCtx context.Context) error {
		var err error
		count, err = store.CountMemoriesPendingIndexByKind(rCtx, targetKind, ns)
		return err
	}))
	require.Equal(t, int64(1), count, "migrated row should be pending for target kind")

	// default/v1 count should now be 0.
	require.NoError(t, store.InReadTx(ctx, func(rCtx context.Context) error {
		var err error
		count, err = store.CountMemoriesPendingIndexByKind(rCtx, "default/v1", ns)
		return err
	}))
	require.Equal(t, int64(0), count, "default/v1 count should be 0 after migration")

	// Simulate the indexer completing: migration is a lifecycle transition and
	// increments the revision before SetMemoryKindIndexedAtCAS runs.
	now := time.Now().UTC()
	require.NoError(t, store.InWriteTx(ctx, func(wCtx context.Context) error {
		return store.SetMemoryKindIndexedAtCAS(wCtx, memID, targetKind, rev+1, now)
	}))

	// After indexer: count for targetKind should be 0.
	require.NoError(t, store.InReadTx(ctx, func(rCtx context.Context) error {
		var err error
		count, err = store.CountMemoriesPendingIndexByKind(rCtx, targetKind, ns)
		return err
	}))
	require.Equal(t, int64(0), count, "count should be 0 after indexer completes")
}

func TestSQLiteMigrationCursorUsesCreatedAtBeforeUUID(t *testing.T) {
	t.Parallel()
	store, ctx := newTestSQLiteEpisodicStore(t)

	require.NoError(t, store.InWriteTx(ctx, func(wCtx context.Context) error {
		for _, key := range []string{"earlier-high-id", "later-low-id"} {
			if _, err := store.PutMemory(wCtx, registryepisodic.PutMemoryRequest{
				Namespace: []string{"users", "cursor-test"}, Key: key,
				Value: map[string]interface{}{"key": key}, MemoryKind: "default/v1",
			}); err != nil {
				return err
			}
		}
		db, err := requireScope(wCtx, "set deterministic migration cursor order")
		if err != nil {
			return err
		}
		if err := db.Exec("UPDATE memories SET id = ?, created_at = ? WHERE key = ?",
			"ffffffff-ffff-4fff-8fff-ffffffffffff", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "earlier-high-id").Error; err != nil {
			return err
		}
		return db.Exec("UPDATE memories SET id = ?, created_at = ? WHERE key = ?",
			"00000000-0000-4000-8000-000000000001", time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC), "later-low-id").Error
	}))

	var first, second []registryepisodic.MigrationCandidate
	require.NoError(t, store.InReadTx(ctx, func(rCtx context.Context) error {
		var err error
		first, err = store.FindMemoriesToMigrateByKind(rCtx, "default/v1", []string{"users", "cursor-test"}, time.Time{}, uuid.Nil, 1)
		return err
	}))
	require.Len(t, first, 1)
	require.Equal(t, "ffffffff-ffff-4fff-8fff-ffffffffffff", first[0].ID.String())
	require.NoError(t, store.InReadTx(ctx, func(rCtx context.Context) error {
		var err error
		second, err = store.FindMemoriesToMigrateByKind(rCtx, "default/v1", []string{"users", "cursor-test"}, first[0].CreatedAt, first[0].ID, 1)
		return err
	}))
	require.Len(t, second, 1, "a later row must not be skipped because its random UUID sorts lower")
	require.Equal(t, "00000000-0000-4000-8000-000000000001", second[0].ID.String())
}

func TestSQLiteCreateMigrationAndTaskRollsBackOnTaskFailure(t *testing.T) {
	t.Parallel()
	store, ctx := newTestSQLiteEpisodicStore(t)
	concrete := store.(*sqliteEpisodicStore)
	require.NoError(t, store.InWriteTx(ctx, func(wCtx context.Context) error {
		_, err := store.CreateMemoryKindVersion(wCtx, model.MemoryKindVersion{
			Name: "target/v1", AttributeTypes: map[string]string{}, Writable: true, CreatedAt: time.Now().UTC(),
		})
		return err
	}))

	require.NoError(t, concrete.db.Exec(`
		CREATE TRIGGER fail_initial_memory_kind_migration_task
		BEFORE INSERT ON tasks
		WHEN NEW.task_type = 'memory_kind_migration'
		BEGIN
			SELECT RAISE(ABORT, 'injected task insert failure');
		END`).Error)

	migrationID := uuid.New()
	err := store.InWriteTx(ctx, func(wCtx context.Context) error {
		_, err := store.CreateMemoryKindMigrationAndTask(wCtx, model.MemoryKindMigration{
			ID: migrationID, Source: "default/v1", Target: "target/v1",
			State: model.MigrationStateQueued, CreatedAt: time.Now().UTC(),
		})
		return err
	})
	require.ErrorContains(t, err, "injected task insert failure")

	var migration *model.MemoryKindMigration
	require.NoError(t, store.InReadTx(ctx, func(rCtx context.Context) error {
		var err error
		migration, err = store.GetMemoryKindMigration(rCtx, migrationID)
		return err
	}))
	require.Nil(t, migration, "migration resource must roll back with its failed initial task")
}

// --- Defect 4: migration verification sweep with tombstone-heavy pages ---

func TestMigrationVerificationSweepWithTombstones(t *testing.T) {
	t.Parallel()
	store, ctx := newTestSQLiteEpisodicStore(t)

	ns := []string{"users", "tombstone-test"}

	// Write and immediately archive (tombstone) > migrationBatchSize rows,
	// then write one replayable row after them.
	const tombstoneCount = 60 // > migrationBatchSize (50)
	var tombstoneIDs []uuid.UUID
	require.NoError(t, store.InWriteTx(ctx, func(wCtx context.Context) error {
		for i := 0; i < tombstoneCount; i++ {
			res, err := store.PutMemory(wCtx, registryepisodic.PutMemoryRequest{
				Namespace:  ns,
				Key:        "tombstone-" + uuid.New().String(),
				Value:      map[string]interface{}{"idx": i},
				MemoryKind: "default/v1",
			})
			if err != nil {
				return err
			}
			tombstoneIDs = append(tombstoneIDs, res.ID)
		}
		return nil
	}))

	// Archive all tombstone rows (makes them appear as tombstones in FindMemoriesToMigrateByKind).
	// We do a direct SQL UPDATE to force value_encrypted=NULL rather than
	// going through the public API, since archiving via PutMemory is a supersede op.
	require.NoError(t, store.InWriteTx(ctx, func(wCtx context.Context) error {
		db, err := requireScope(wCtx, "test tombstone")
		if err != nil {
			return err
		}
		for _, id := range tombstoneIDs {
			if err := db.Exec(
				"UPDATE memories SET value_encrypted = NULL, archived_at = ? WHERE id = ?",
				time.Now().UTC(), id.String(),
			).Error; err != nil {
				return err
			}
		}
		return nil
	}))

	// Now write one replayable (live) row.
	require.NoError(t, store.InWriteTx(ctx, func(wCtx context.Context) error {
		_, err := store.PutMemory(wCtx, registryepisodic.PutMemoryRequest{
			Namespace:  ns,
			Key:        "live-row",
			Value:      map[string]interface{}{"x": "replayable"},
			MemoryKind: "default/v1",
		})
		return err
	}))

	// Scan all pages from the start to verify we see the replayable row.
	afterID := uuid.Nil
	var afterCreatedAt time.Time
	var totalCandidates, totalTombstones, totalLive int
	for {
		var batch []registryepisodic.MigrationCandidate
		require.NoError(t, store.InReadTx(ctx, func(rCtx context.Context) error {
			var err error
			batch, err = store.FindMemoriesToMigrateByKind(rCtx, "default/v1", ns, afterCreatedAt, afterID, 50)
			return err
		}))
		if len(batch) == 0 {
			break
		}
		totalCandidates += len(batch)
		for _, c := range batch {
			if len(c.ValueEncrypted) == 0 {
				totalTombstones++
			} else {
				totalLive++
			}
		}
		afterID = batch[len(batch)-1].ID
		afterCreatedAt = batch[len(batch)-1].CreatedAt
	}

	require.Equal(t, tombstoneCount, totalTombstones, "tombstone count mismatch")
	require.Equal(t, 1, totalLive, "expected exactly one replayable live row")
	require.Equal(t, tombstoneCount+1, totalCandidates)
}

// --- Item 8: SQLite CreateMemoryKindVersion idempotent/conflict behavior ---

func TestCreateMemoryKindVersionIdempotent(t *testing.T) {
	t.Parallel()
	store, ctx := newTestSQLiteEpisodicStore(t)

	sv := model.MemoryKindVersion{
		Name:           "test/v1",
		AttributeTypes: map[string]string{"score": "number"},
		Writable:       true,
		CreatedAt:      time.Now().UTC(),
	}

	// First create should succeed.
	var got1 *model.MemoryKindVersion
	require.NoError(t, store.InWriteTx(ctx, func(wCtx context.Context) error {
		var err error
		got1, err = store.CreateMemoryKindVersion(wCtx, sv)
		return err
	}))
	require.NotNil(t, got1)
	require.Equal(t, "test/v1", got1.Name)

	// Second create with identical content should return existing (idempotent).
	var got2 *model.MemoryKindVersion
	require.NoError(t, store.InWriteTx(ctx, func(wCtx context.Context) error {
		var err error
		got2, err = store.CreateMemoryKindVersion(wCtx, sv)
		return err
	}))
	require.NotNil(t, got2)
	require.Equal(t, "test/v1", got2.Name)

	// Third create with DIFFERENT content should return ErrMemoryKindVersionConflict.
	svDiff := model.MemoryKindVersion{
		Name:           "test/v1",
		AttributeTypes: map[string]string{"score": "string"}, // different type
		Writable:       true,
		CreatedAt:      time.Now().UTC(),
	}
	err := store.InWriteTx(ctx, func(wCtx context.Context) error {
		_, err := store.CreateMemoryKindVersion(wCtx, svDiff)
		return err
	})
	require.ErrorIs(t, err, registryepisodic.ErrMemoryKindVersionConflict)
}

// --- Bug 7: concurrent idempotent create ---

// TestCreateMemoryKindVersionConcurrentIdempotent verifies that concurrent identical
// creates of the same schema version are race-safe and produce idempotent behavior
// (both callers receive no error, and the version is present exactly once).
//
// This is an in-process concurrency test using real SQLite — it cannot simulate
// two truly separate processes, but it does exercise the INSERT OR IGNORE path
// under concurrent goroutines sharing the same db handle.
func TestCreateMemoryKindVersionConcurrentIdempotent(t *testing.T) {
	t.Parallel()
	store, ctx := newTestSQLiteEpisodicStore(t)

	sv := model.MemoryKindVersion{
		Name:           "concurrent/v1",
		AttributeTypes: map[string]string{"score": "number"},
		Writable:       true,
		CreatedAt:      time.Now().UTC(),
	}

	const goroutines = 10
	errs := make([]error, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			errs[i] = store.InWriteTx(ctx, func(wCtx context.Context) error {
				_, err := store.CreateMemoryKindVersion(wCtx, sv)
				return err
			})
		}()
	}
	wg.Wait()

	// All concurrent identical creates must succeed (idempotent).
	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: unexpected error: %v", i, err)
		}
	}

	// The version must exist exactly once.
	var sv2 *model.MemoryKindVersion
	require.NoError(t, store.InReadTx(ctx, func(rCtx context.Context) error {
		var err error
		sv2, err = store.GetMemoryKindVersion(rCtx, "concurrent/v1")
		return err
	}))
	require.NotNil(t, sv2)
	require.Equal(t, "concurrent/v1", sv2.Name)
}

// --- Defect 1: SQLite InWriteTx rolls back MigrateOneMemoryKindCAS + IncrementMigrated together ---

// TestSQLiteMigrateCASPlusIncrementRollsBackTogether is a real SQLite integration test
// proving that MigrateOneMemoryKindCAS and UpdateMemoryKindMigrationIncrementMigrated,
// when called inside a single InWriteTx whose callback returns an error, are both rolled
// back atomically: the primary memory row reverts to its original kind/revision and the
// migration migrated_count stays at zero.
//
// This directly tests the transaction scope fix (defect 1): both store methods use
// writeDBFor which resolves the write-scoped *gorm.DB from the context.  A real SQLite
// transaction guarantees the rollback; the test would fail if either method used the
// bare db handle instead of the scoped one.
func TestSQLiteMigrateCASPlusIncrementRollsBackTogether(t *testing.T) {
	t.Parallel()
	store, ctx := newTestSQLiteEpisodicStore(t)

	ns := []string{"users", "rollback-test"}
	targetKind := "events/v1"

	// Seed the target schema version.
	require.NoError(t, store.InWriteTx(ctx, func(wCtx context.Context) error {
		_, err := store.CreateMemoryKindVersion(wCtx, model.MemoryKindVersion{
			Name:           targetKind,
			AttributeTypes: map[string]string{"score": "number"},
			Writable:       true,
			CreatedAt:      time.Now().UTC(),
		})
		return err
	}))

	// Write a memory with default/v1 kind.
	var memID uuid.UUID
	var memRev int64
	require.NoError(t, store.InWriteTx(ctx, func(wCtx context.Context) error {
		res, err := store.PutMemory(wCtx, registryepisodic.PutMemoryRequest{
			Namespace:  ns,
			Key:        "key-rollback",
			Value:      map[string]interface{}{"x": 1},
			MemoryKind: "default/v1",
		})
		if err != nil {
			return err
		}
		memID = res.ID
		memRev = res.Revision
		return nil
	}))

	// Seed a migration record so UpdateMemoryKindMigrationIncrementMigrated has a row to update.
	migID := uuid.New()
	require.NoError(t, store.InWriteTx(ctx, func(wCtx context.Context) error {
		_, err := store.CreateMemoryKindMigration(wCtx, model.MemoryKindMigration{
			ID:        migID,
			Source:    "default/v1",
			Target:    targetKind,
			State:     model.MigrationStateRunning,
			CreatedAt: time.Now().UTC(),
		})
		return err
	}))

	// Execute both writes inside a single InWriteTx and then force a rollback by
	// returning a sentinel error after both writes have been issued.
	forcedErr := fmt.Errorf("forced rollback")
	err := store.InWriteTx(ctx, func(wCtx context.Context) error {
		// Step 1: CAS-migrate the memory row from default/v1 → events/v1.
		if err := store.MigrateOneMemoryKindCAS(wCtx, memID, "default/v1", memRev,
			map[string]interface{}{"score": 42.0}, targetKind); err != nil {
			return err
		}
		// Step 2: increment migrated_count on the migration record.
		if err := store.UpdateMemoryKindMigrationIncrementMigrated(wCtx, migID); err != nil {
			return err
		}
		// Both writes executed successfully inside this transaction scope —
		// now force a rollback by returning an error.
		return forcedErr
	})
	// The InWriteTx must surface the forced error.
	require.ErrorIs(t, err, forcedErr, "InWriteTx must return the callback error")

	// After rollback: primary memory kind must still be default/v1.
	// We verify by scanning FindMemoriesToMigrateByKind — the row must still appear
	// as a source candidate for default/v1 migration.
	var candidates []registryepisodic.MigrationCandidate
	require.NoError(t, store.InReadTx(ctx, func(rCtx context.Context) error {
		var err error
		candidates, err = store.FindMemoriesToMigrateByKind(rCtx, "default/v1", ns, time.Time{}, uuid.Nil, 10)
		return err
	}))
	require.Len(t, candidates, 1, "rolled-back memory must still appear as default/v1 source candidate")
	require.Equal(t, memID, candidates[0].ID, "candidate must be the seeded memory")
	require.Equal(t, "default/v1", candidates[0].MemoryKind,
		"memory_kind must still be default/v1 after rollback")

	// After rollback: migration migrated_count must still be zero.
	var m *model.MemoryKindMigration
	require.NoError(t, store.InReadTx(ctx, func(rCtx context.Context) error {
		var err error
		m, err = store.GetMemoryKindMigration(rCtx, migID)
		return err
	}))
	require.NotNil(t, m)
	require.Equal(t, int64(0), m.MigratedCount,
		"migrated_count must be zero after InWriteTx rollback")
}
