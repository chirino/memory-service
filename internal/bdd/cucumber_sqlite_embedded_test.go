package bdd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/chirino/memory-service/internal/buildcaps"
	"github.com/chirino/memory-service/internal/cmd/serve"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/cucumber/godog"
)

func TestFeaturesSQLiteEmbedded(t *testing.T) {
	if !buildcaps.SQLite {
		requireCapabilities(t, "sqlite")
	}

	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeProd
	cfg.DatastoreType = "sqlite"
	cfg.EmbedType = "none"
	cfg.EncryptionProviders = "plain"
	cfg.EncryptionAllowPlain = true
	cfg.Listener.Port = 0
	cfg.Listener.EnablePlainText = true
	cfg.Listener.EnableTLS = false
	cfg.ManagementOnMainListener = true
	cfg.UnixSocketAuth = "local"
	cfg.LocalClientID = "local-agent"

	featureFiles := []string{
		filepath.Join("testdata", "features-sqlite", "embedded-local-grpc.feature"),
	}
	runBDDFeaturesWithScenarioSetup(t, "sqlite-embedded-local", featureFiles, "", "", &cfg, nil, nil, newSQLiteEmbeddedScenarioSetup(t, cfg), 1)
}

func newSQLiteEmbeddedScenarioSetup(t *testing.T, baseCfg config.Config) cucumber.ScenarioSetupFunc {
	t.Helper()

	return func(ctx context.Context, s *cucumber.TestScenario, sc *godog.Scenario) (func(context.Context) error, error) {
		_ = ctx
		_ = sc

		tempDir, err := os.MkdirTemp("", "memory-service-embedded-")
		if err != nil {
			return nil, err
		}

		cfg := baseCfg
		cfg.DBURL = filepath.Join(tempDir, "memory.db")
		cfg.Listener.UnixSocket = filepath.Join(tempDir, "memory.sock")
		ctx = config.WithContext(context.Background(), &cfg)
		srv, err := serve.StartServer(ctx, &cfg)
		if err != nil {
			_ = os.RemoveAll(tempDir)
			return nil, err
		}

		s.DB = &SQLiteTestDB{DBURL: cfg.DBURL}
		s.Extra = cucumberCloneExtras(s.Extra, map[string]interface{}{
			"grpcAddr": "unix://" + cfg.Listener.UnixSocket,
		})

		return func(context.Context) error {
			return errors.Join(srv.Shutdown(context.Background()), os.RemoveAll(tempDir))
		}, nil
	}
}
