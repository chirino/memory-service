//go:build sqlite_fts5

package bdd

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/chirino/memory-service/internal/cmd/serve"
	"github.com/chirino/memory-service/internal/config"
	"github.com/stretchr/testify/require"
)

func TestFeaturesSQLiteOutbox(t *testing.T) {
	prom := NewMockPrometheus(t)
	dbURL := filepath.Join(t.TempDir(), "memory.db")

	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeTesting
	cfg.DatastoreType = "sqlite"
	cfg.DBURL = dbURL
	cfg.CacheType = "none"
	cfg.AttachType = "fs"
	cfg.VectorType = "none"
	cfg.SearchSemanticEnabled = false
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
	}
	runBDDFeatures(t, "sqlite-outbox", featureFiles, apiURL, grpcAddr, &cfg, &SQLiteTestDB{DBURL: dbURL}, map[string]interface{}{
		"mockPrometheus": prom,
		"grpcAddr":       grpcAddr,
	})
}
