package bdd

import (
	"path/filepath"
	"testing"

	"github.com/chirino/memory-service/internal/buildcaps"
	"github.com/chirino/memory-service/internal/config"
)

func TestFeaturesSQLiteOutbox(t *testing.T) {
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
	cfg.OutboxEnabled = true
	cfg.EncryptionKey = testEncryptionKey
	cfg.EncryptionDBDisabled = true
	cfg.EncryptionAttachmentsDisabled = true
	cfg.AdminUsers = bddAdminUsers()
	cfg.AuditorUsers = bddAuditorUsers()
	cfg.IndexerUsers = bddIndexerUsers()
	cfg.Listener.Port = 0
	cfg.Listener.EnableTLS = false

	featureFiles := []string{
		filepath.Join("testdata", "features", "sse-events-rest.feature"),
		filepath.Join("testdata", "features", "sse-events-replay-rest.feature"),
	}
	runBDDFeaturesWithScenarioSetupAndTags(t, "sqlite-outbox", featureFiles, "", "", &cfg, nil, nil, newSQLiteScenarioSetup(t, cfg), bddScenarioConcurrency(), sqliteTagFilter())
}
