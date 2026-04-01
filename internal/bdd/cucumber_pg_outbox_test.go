package bdd

import (
	"context"
	"fmt"
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

func TestFeaturesPgOutbox(t *testing.T) {
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
	cfg.OutboxEnabled = true
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

	featureFiles := []string{
		filepath.Join("testdata", "features", "sse-events-rest.feature"),
		filepath.Join("testdata", "features", "sse-events-replay-rest.feature"),
		filepath.Join("testdata", "features-pg-outbox", "sse-events-pg-outbox-rest.feature"),
		filepath.Join("testdata", "features-pg-outbox", "sse-events-pg-outbox-replay-rest.feature"),
		filepath.Join("testdata", "features-grpc", "sse-events-grpc.feature"),
	}
	runBDDFeatures(t, "pg-outbox", featureFiles, apiURL, grpcAddr, &cfg, &PostgresTestDB{DBURL: dbURL}, map[string]interface{}{
		"mockPrometheus": prom,
		"grpcAddr":       grpcAddr,
	})
}
