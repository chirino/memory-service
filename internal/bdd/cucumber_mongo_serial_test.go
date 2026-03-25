package bdd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/chirino/memory-service/internal/cmd/serve"
	"github.com/chirino/memory-service/internal/config"
	mongoplugin "github.com/chirino/memory-service/internal/plugin/store/mongo"
	"github.com/chirino/memory-service/internal/testutil/testmongo"
	"github.com/chirino/memory-service/internal/testutil/testqdrant"
	"github.com/chirino/memory-service/internal/testutil/testredis"
	"github.com/stretchr/testify/require"

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
	prom := NewMockPrometheus(t)

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
	cfg.PrometheusURL = prom.Server.URL
	cfg.Listener.Port = 0
	cfg.Listener.EnableTLS = false
	ctx := config.WithContext(context.Background(), &cfg)

	srv, err := serve.StartServer(ctx, &cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	apiURL := fmt.Sprintf("http://localhost:%d", srv.Running.Port)
	grpcAddr := fmt.Sprintf("localhost:%d", srv.Running.Port)

	featuresDir := filepath.Join("testdata", "features")
	if _, err := os.Stat(featuresDir); os.IsNotExist(err) {
		t.Skipf("Feature files directory not found: %s", featuresDir)
	}

	featureFiles, err := filepath.Glob(filepath.Join(featuresDir, "*.feature"))
	require.NoError(t, err)
	featureFiles = filterSerialFeatures(featureFiles, true)
	require.NotEmpty(t, featureFiles, "No serial Mongo feature files found")

	runBDDFeaturesWithConcurrency(t, "mongo-serial", featureFiles, apiURL, grpcAddr, &cfg, &MongoTestDB{DBURL: mongoURL}, map[string]interface{}{
		"mockPrometheus": prom,
		"grpcAddr":       grpcAddr,
	}, 1)
}
