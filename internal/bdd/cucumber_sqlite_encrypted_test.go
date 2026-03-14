//go:build sqlite_fts5

package bdd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/chirino/memory-service/internal/cmd/serve"
	"github.com/chirino/memory-service/internal/config"
	"github.com/stretchr/testify/require"
)

func TestFeaturesSQLiteEncrypted(t *testing.T) {
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
	cfg.EncryptionKey = testEncryptionKey
	cfg.AdminUsers = "alice"
	cfg.AuditorUsers = "alice,charlie"
	cfg.IndexerUsers = "dave,alice"
	cfg.PrometheusURL = prom.Server.URL
	cfg.Listener.Port = 0
	cfg.Listener.EnableTLS = false
	ctx := config.WithContext(context.Background(), &cfg)

	srv, err := serve.StartServer(ctx, &cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	apiURL := fmt.Sprintf("http://localhost:%d", srv.Running.Port)
	grpcAddr := fmt.Sprintf("localhost:%d", srv.Running.Port)

	encryptedDir := filepath.Join("testdata", "features-encrypted")
	if _, err := os.Stat(encryptedDir); os.IsNotExist(err) {
		t.Skipf("Encrypted feature files directory not found: %s", encryptedDir)
	}
	featureFiles, err := filepath.Glob(filepath.Join(encryptedDir, "*.feature"))
	require.NoError(t, err)
	require.NotEmpty(t, featureFiles, "No encrypted feature files found in %s", encryptedDir)

	runBDDFeatures(t, "sqlite-encrypted", featureFiles, apiURL, grpcAddr, &cfg, &SQLiteTestDB{DBURL: dbURL}, map[string]interface{}{
		"mockPrometheus": prom,
		"grpcAddr":       grpcAddr,
	})
}
