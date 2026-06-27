//go:build auth_testfixtures

package mcp

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/buildcaps"
	"github.com/chirino/memory-service/internal/cmd/serve"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/generated/apiclient"
	"github.com/stretchr/testify/require"
)

// setupTestServer creates a raw-bearer testing-mode server for fixture coverage.
// This function and all tests using it require the auth_testfixtures build tag.
func setupTestServer(t *testing.T) *mcpServer {
	t.Helper()
	if !buildcaps.SQLite {
		t.Skip("required build capabilities missing: sqlite")
	}

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
	cfg.EncryptionDBDisabled = true
	cfg.EncryptionAttachmentsDisabled = true
	cfg.AdminUsers = "alice,alice-*"
	cfg.AuditorUsers = "alice,alice-*"
	cfg.IndexerUsers = "alice,alice-*"
	cfg.Listener.Port = 0
	cfg.Listener.EnableTLS = false

	ctx := config.WithContext(context.Background(), &cfg)
	srv, err := serve.StartServer(ctx, &cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	apiURL := fmt.Sprintf("http://localhost:%d", srv.Running.Port)
	client, err := apiclient.NewClientWithResponses(
		apiURL,
		apiclient.WithHTTPClient(&http.Client{Timeout: 30 * time.Second}),
		apiclient.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
			req.Header.Set("Authorization", "Bearer alice")
			req.Header.Set("X-Client-ID", "test-client")
			return nil
		}),
	)
	require.NoError(t, err)

	return &mcpServer{client: client}
}

func TestSessionToolContractFixtures(t *testing.T) {
	runSessionToolContract(t, setupTestServer, true)
}
