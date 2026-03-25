package bdd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/chirino/memory-service/internal/cmd/serve"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/plugin/store/postgres"
	"github.com/chirino/memory-service/internal/testutil/testinfinispan"
	"github.com/chirino/memory-service/internal/testutil/testpg"
	"github.com/stretchr/testify/require"

	_ "github.com/chirino/memory-service/internal/plugin/attach/pgstore"
	_ "github.com/chirino/memory-service/internal/plugin/cache/infinispan"
	_ "github.com/chirino/memory-service/internal/plugin/embed/disabled"
	_ "github.com/chirino/memory-service/internal/plugin/route/system"
	_ "github.com/chirino/memory-service/internal/plugin/vector/pgvector"
)

func TestFeaturesSerial(t *testing.T) {
	_ = postgres.ForceImport

	dbURL := testpg.StartPostgres(t)
	prom := NewMockPrometheus(t)
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
	require.NotEmpty(t, featureFiles, "No serial feature files found in %s", featuresDir)

	runBDDFeaturesWithConcurrency(t, "serial", featureFiles, apiURL, grpcAddr, &cfg, &PostgresTestDB{DBURL: dbURL}, map[string]interface{}{
		"mockPrometheus": prom,
		"grpcAddr":       grpcAddr,
	}, 1)
}
