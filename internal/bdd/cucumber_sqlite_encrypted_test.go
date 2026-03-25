//go:build sqlite_fts5

package bdd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chirino/memory-service/internal/config"
)

func TestFeaturesSQLiteEncrypted(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeTesting
	cfg.DatastoreType = "sqlite"
	cfg.CacheType = "none"
	cfg.AttachType = "fs"
	cfg.VectorType = "none"
	cfg.SearchSemanticEnabled = false
	cfg.EncryptionKey = testEncryptionKey
	cfg.AdminUsers = bddAdminUsers()
	cfg.AuditorUsers = bddAuditorUsers()
	cfg.IndexerUsers = bddIndexerUsers()
	cfg.Listener.Port = 0
	cfg.Listener.EnableTLS = false

	encryptedDir := filepath.Join("testdata", "features-encrypted")
	if _, err := os.Stat(encryptedDir); os.IsNotExist(err) {
		t.Skipf("Encrypted feature files directory not found: %s", encryptedDir)
	}
	featureFiles, err := filepath.Glob(filepath.Join(encryptedDir, "*.feature"))
	if err != nil {
		t.Fatalf("glob encrypted feature files: %v", err)
	}
	if len(featureFiles) == 0 {
		t.Fatalf("No encrypted feature files found in %s", encryptedDir)
	}

	runBDDFeaturesWithScenarioSetup(t, "sqlite-encrypted", featureFiles, "", "", &cfg, nil, nil, newSQLiteScenarioSetup(t, cfg), bddScenarioConcurrency())
}
