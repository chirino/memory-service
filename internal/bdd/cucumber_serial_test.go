package bdd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/plugin/store/postgres"
	"github.com/chirino/memory-service/internal/testutil/testinfinispan"
	"github.com/chirino/memory-service/internal/testutil/testpg"

	_ "github.com/chirino/memory-service/internal/plugin/attach/pgstore"
	_ "github.com/chirino/memory-service/internal/plugin/cache/infinispan"
	_ "github.com/chirino/memory-service/internal/plugin/embed/disabled"
	_ "github.com/chirino/memory-service/internal/plugin/route/system"
	_ "github.com/chirino/memory-service/internal/plugin/vector/pgvector"
)

func TestFeaturesSerial(t *testing.T) {
	_ = postgres.ForceImport

	dbURL := testpg.StartPostgres(t)
	infinispan := testinfinispan.StartInfinispan(t)

	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeTesting
	cfg.DBURL = dbURL
	cfg.CacheType = "infinispan"
	cfg.InfinispanHost = infinispan.Host
	cfg.InfinispanUsername = infinispan.Username
	cfg.InfinispanPassword = infinispan.Password
	cfg.EncryptionKey = testEncryptionKey
	cfg.EncryptionDBDisabled = true
	cfg.EncryptionAttachmentsDisabled = true
	cfg.AdminUsers = bddAdminUsers()
	cfg.AuditorUsers = bddAuditorUsers()
	cfg.IndexerUsers = bddIndexerUsers()
	cfg.Listener.Port = 0
	cfg.Listener.EnableTLS = false

	featuresDir := filepath.Join("testdata", "features")
	if _, err := os.Stat(featuresDir); os.IsNotExist(err) {
		t.Skipf("Feature files directory not found: %s", featuresDir)
	}

	featureFiles, err := filepath.Glob(filepath.Join(featuresDir, "*.feature"))
	if err != nil {
		t.Fatalf("glob postgres serial feature files: %v", err)
	}
	featureFiles = filterSerialFeatures(featureFiles, true)
	if len(featureFiles) == 0 {
		t.Fatalf("No serial feature files found in %s", featuresDir)
	}

	runBDDFeaturesWithScenarioSetup(t, "serial", featureFiles, "", "", &cfg, nil, nil, newPostgresScenarioSetup(t, cfg), bddScenarioConcurrency())
}
