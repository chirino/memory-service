//go:build auth_testfixtures

package bdd

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/chirino/memory-service/internal/buildcaps"
	"github.com/chirino/memory-service/internal/cmd/serve"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/testutil/testmongo"
	"github.com/chirino/memory-service/internal/testutil/testpg"
	"github.com/stretchr/testify/require"
)

// adminMemoriesFeatureFiles are the raw-bearer admin memory features. They are kept out of
// the broad PostgreSQL and MongoDB runners (authModeFeatureFiles) and run here instead.
func adminMemoriesFeatureFiles() []string {
	return []string{
		filepath.Join("testdata", "features", "admin-memories-rest.feature"),
		filepath.Join("testdata", "features-grpc", "admin-memories-grpc.feature"),
	}
}

// TestFeaturesAuthFixturesPostgres runs the admin memory features against PostgreSQL.
func TestFeaturesAuthFixturesPostgres(t *testing.T) {
	if !buildcaps.PostgreSQL {
		requireCapabilities(t, "postgresql")
	}

	dbURL := testpg.StartPostgres(t)

	cfg := defaultBDDConfig()
	cfg.Mode = config.ModeTesting
	cfg.DBURL = dbURL
	cfg.CacheType = "none"
	cfg.VectorType = "none"
	cfg.SearchSemanticEnabled = false
	cfg.EncryptionDBDisabled = true
	cfg.EncryptionAttachmentsDisabled = true
	cfg.AdminUsers = bddAdminUsers()
	cfg.AuditorUsers = bddAuditorUsers()
	cfg.IndexerUsers = bddIndexerUsers()
	cfg.Listener.Port = 0
	cfg.Listener.EnableTLS = false
	ctx := config.WithContext(context.Background(), &cfg)

	srv, err := serve.StartServer(ctx, &cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	apiURL := fmt.Sprintf("http://localhost:%d", srv.Running.Port)
	grpcAddr := fmt.Sprintf("localhost:%d", srv.Running.Port)
	runBDDFeatures(t, "auth-fixtures-postgres", adminMemoriesFeatureFiles(), apiURL, grpcAddr, &cfg, &PostgresTestDB{DBURL: dbURL}, map[string]interface{}{
		"grpcAddr": grpcAddr,
	})
}

// TestFeaturesAuthFixturesMongo runs the admin memory features against MongoDB.
func TestFeaturesAuthFixturesMongo(t *testing.T) {
	if !buildcaps.MongoDB {
		requireCapabilities(t, "mongo")
	}

	mongoURL := testmongo.StartMongo(t)

	cfg := defaultBDDConfig()
	cfg.Mode = config.ModeTesting
	cfg.DatastoreType = "mongo"
	cfg.AttachType = "fs"
	cfg.AttachFSDir = t.TempDir()
	cfg.DBURL = mongoURL
	cfg.CacheType = "none"
	cfg.VectorType = "none"
	cfg.SearchSemanticEnabled = false
	cfg.EncryptionDBDisabled = true
	cfg.EncryptionAttachmentsDisabled = true
	cfg.AdminUsers = bddAdminUsers()
	cfg.AuditorUsers = bddAuditorUsers()
	cfg.IndexerUsers = bddIndexerUsers()
	cfg.Listener.Port = 0
	cfg.Listener.EnableTLS = false
	ctx := config.WithContext(context.Background(), &cfg)

	srv, err := serve.StartServer(ctx, &cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	apiURL := fmt.Sprintf("http://localhost:%d", srv.Running.Port)
	grpcAddr := fmt.Sprintf("localhost:%d", srv.Running.Port)
	runBDDFeatures(t, "auth-fixtures-mongo", adminMemoriesFeatureFiles(), apiURL, grpcAddr, &cfg, &MongoTestDB{DBURL: mongoURL}, map[string]interface{}{
		"grpcAddr": grpcAddr,
	})
}
