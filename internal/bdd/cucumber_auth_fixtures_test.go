//go:build auth_testfixtures

package bdd

import (
	"path/filepath"
	"testing"

	"github.com/chirino/memory-service/internal/buildcaps"
	"github.com/chirino/memory-service/internal/config"
)

// TestFeaturesAuthFixturesSQLite is the focused auth_testfixtures BDD smoke suite.
// It keeps CI coverage on the raw-bearer + X-Client-ID cucumber fixture path without
// rerunning every datastore/cache/outbox variant under the fixture build tag.
func TestFeaturesAuthFixturesSQLite(t *testing.T) {
	if !buildcaps.SQLite {
		requireCapabilities(t, "sqlite")
	}

	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeTesting
	cfg.DatastoreType = "sqlite"
	cfg.CacheType = "none"
	cfg.AttachType = "fs"
	cfg.VectorType = "none"
	cfg.SearchSemanticEnabled = false
	cfg.EncryptionKey = testEncryptionKey
	cfg.EncryptionDBDisabled = true
	cfg.EncryptionAttachmentsDisabled = true
	cfg.AdminUsers = bddAdminUsers()
	cfg.AuditorUsers = bddAuditorUsers()
	cfg.IndexerUsers = bddIndexerUsers()
	cfg.Listener.Port = 0
	cfg.Listener.EnableTLS = false

	featureFiles := []string{
		filepath.Join("testdata", "features", "admin-memories-rest.feature"),
		filepath.Join("testdata", "features", "capabilities-rest.feature"),
		filepath.Join("testdata", "features", "multi-agent-memory-rest.feature"),
		filepath.Join("testdata", "features-grpc", "admin-memories-grpc.feature"),
	}
	runBDDFeaturesWithScenarioSetupAndTags(t, "auth-fixtures-sqlite", featureFiles, "", "", &cfg, nil, nil, newSQLiteScenarioSetup(t, cfg), bddScenarioConcurrency(), sqliteTagFilter())
}
