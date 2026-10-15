package bdd

import (
	"path/filepath"
	"testing"

	"github.com/chirino/memory-service/internal/buildcaps"
	"github.com/chirino/memory-service/internal/config"
)

// TestFeaturesSQLiteGRPCContractRegressions keeps string-valued conversation
// IDs covered outside the broad PostgreSQL runner. Each scenario gets its own
// server and database, so these features remain safe at normal BDD concurrency.
func TestFeaturesSQLiteGRPCContractRegressions(t *testing.T) {
	if !buildcaps.SQLite {
		requireCapabilities(t, "sqlite")
	}

	cfg := defaultBDDConfig()
	cfg.Mode = config.ModeTesting
	cfg.DatastoreType = "sqlite"
	cfg.CacheType = "local"
	cfg.AttachType = "fs"
	cfg.VectorType = "none"
	cfg.SearchSemanticEnabled = false
	cfg.EncryptionDBDisabled = true
	cfg.EncryptionAttachmentsDisabled = true
	cfg.APIKeys = map[string]string{"test-agent-key": "test-agent-key"}
	cfg.AdminUsers = bddAdminUsers()
	cfg.AuditorUsers = bddAuditorUsers()
	cfg.IndexerUsers = bddIndexerUsers()
	cfg.Listener.Port = 0
	cfg.Listener.EnableTLS = false

	featureFiles := []string{
		filepath.Join("testdata", "features", "response-recorder-grpc.feature"),
		filepath.Join("testdata", "features-grpc", "conversations-grpc.feature"),
		filepath.Join("testdata", "features-grpc", "forking-grpc.feature"),
		filepath.Join("testdata", "features-grpc", "ownership-transfers-grpc.feature"),
		filepath.Join("testdata", "features-grpc", "sharing-grpc.feature"),
		filepath.Join("testdata", "features-grpc", "update-conversation-grpc.feature"),
	}
	runBDDFeaturesWithScenarioSetupAndTags(
		t, "sqlite-grpc-contract-regressions", featureFiles, "", "", &cfg, nil, nil,
		newSQLiteScenarioSetup(t, cfg), bddScenarioConcurrency(), sqliteTagFilter(),
	)
}
