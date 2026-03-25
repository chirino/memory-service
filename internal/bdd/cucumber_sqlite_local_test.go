//go:build sqlite_fts5

package bdd

import (
	"path/filepath"
	"testing"

	"github.com/chirino/memory-service/internal/config"
)

func TestFeaturesSQLiteLocal(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeTesting
	cfg.DatastoreType = "sqlite"
	cfg.CacheType = "local"
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
		filepath.Join("testdata", "features", "memory-cache-rest.feature"),
		filepath.Join("testdata", "features", "response-recorder-grpc.feature"),
	}
	runBDDFeaturesWithScenarioSetup(t, "sqlite-local", featureFiles, "", "", &cfg, nil, nil, newSQLiteScenarioSetup(t, cfg), bddScenarioConcurrency())
}
