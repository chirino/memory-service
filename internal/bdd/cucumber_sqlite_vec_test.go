package bdd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chirino/memory-service/internal/buildcaps"
	"github.com/chirino/memory-service/internal/config"
)

func TestFeaturesSQLiteVec(t *testing.T) {
	if !buildcaps.SQLite {
		requireCapabilities(t, "sqlite")
	}

	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeTesting
	cfg.DatastoreType = "sqlite"
	cfg.CacheType = "none"
	cfg.AttachType = "fs"
	cfg.VectorType = "sqlite"
	cfg.EmbedType = "local"
	cfg.SearchSemanticEnabled = true
	cfg.SearchFulltextEnabled = true
	cfg.EncryptionKey = testEncryptionKey
	cfg.EncryptionDBDisabled = true
	cfg.EncryptionAttachmentsDisabled = true
	cfg.AdminUsers = bddAdminUsers()
	cfg.AuditorUsers = bddAuditorUsers()
	cfg.IndexerUsers = bddIndexerUsers()
	cfg.Listener.Port = 0
	cfg.Listener.EnableTLS = false

	featuresDir := filepath.Join("testdata", "features-sqlite")
	if _, err := os.Stat(featuresDir); os.IsNotExist(err) {
		t.Skipf("SQLite vector feature files directory not found: %s", featuresDir)
	}
	featureFiles, err := filepath.Glob(filepath.Join(featuresDir, "*.feature"))
	if err != nil {
		t.Fatalf("glob sqlite vector feature files: %v", err)
	}
	if len(featureFiles) == 0 {
		t.Fatalf("No SQLite vector feature files found in %s", featuresDir)
	}

	runBDDFeaturesWithScenarioSetupAndTags(t, "sqlite-vec", featureFiles, "", "", &cfg, nil, nil, newSQLiteScenarioSetup(t, cfg), bddScenarioConcurrency(), sqliteTagFilter())
}
