package bdd

import (
	"path/filepath"
	"testing"

	"github.com/chirino/memory-service/internal/buildcaps"
	"github.com/chirino/memory-service/internal/config"
)

func TestFeaturesSQLite(t *testing.T) {
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

	featureFiles := collectSQLiteRESTFeatures(t)
	runBDDFeaturesWithScenarioSetupAndTags(t, "sqlite-rest", featureFiles, "", "", &cfg, nil, nil, newSQLiteScenarioSetup(t, cfg), bddScenarioConcurrency(), sqliteTagFilter())
}

func collectSQLiteRESTFeatures(t *testing.T) []string {
	t.Helper()

	featureFiles := collectRESTFeatureFiles(t, "testdata", map[string]bool{
		"memory-cache-rest.feature":      true,
		"sse-events-rest.feature":        true,
		"sse-events-replay-rest.feature": true,
	})
	featureFiles = append(featureFiles, filepath.Join("testdata", "features-sqlite", "mcp-sqlite.feature"))
	return featureFiles
}
