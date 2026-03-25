package bdd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	mongoplugin "github.com/chirino/memory-service/internal/plugin/store/mongo"
	"github.com/chirino/memory-service/internal/testutil/testmongo"
	"github.com/chirino/memory-service/internal/testutil/testqdrant"
	"github.com/chirino/memory-service/internal/testutil/testredis"

	_ "github.com/chirino/memory-service/internal/plugin/attach/mongostore"
	_ "github.com/chirino/memory-service/internal/plugin/cache/redis"
	_ "github.com/chirino/memory-service/internal/plugin/embed/disabled"
	_ "github.com/chirino/memory-service/internal/plugin/route/system"
	_ "github.com/chirino/memory-service/internal/plugin/vector/qdrant"
)

func TestFeaturesMongoSerial(t *testing.T) {
	_ = mongoplugin.ForceImport

	mongoURL := testmongo.StartMongo(t)
	redisURL := testredis.StartRedis(t)
	qdrantHost := testqdrant.StartQdrant(t)

	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeTesting
	cfg.DatastoreType = "mongo"
	cfg.AttachType = "mongo"
	cfg.DBURL = mongoURL
	cfg.CacheType = "redis"
	cfg.RedisURL = redisURL
	cfg.VectorType = "qdrant"
	cfg.QdrantHost = qdrantHost
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
		t.Fatalf("glob mongo serial feature files: %v", err)
	}
	featureFiles = filterSerialFeatures(featureFiles, true)
	if len(featureFiles) == 0 {
		t.Fatalf("No serial Mongo feature files found")
	}

	runBDDFeaturesWithScenarioSetup(t, "mongo-serial", featureFiles, "", "", &cfg, nil, nil, newMongoScenarioSetup(t, cfg), bddScenarioConcurrency())
}
