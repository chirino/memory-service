//go:build sqlite_fts5

package bdd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chirino/memory-service/internal/cmd/serve"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/cucumber/godog"
	"github.com/stretchr/testify/require"
)

func TestFeaturesSQLite(t *testing.T) {
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
	cfg.EncryptionDBDisabled = true
	cfg.EncryptionAttachmentsDisabled = true
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

	featureFiles := collectSQLiteRESTFeatures(t)
	runBDDFeatures(t, "sqlite-rest", featureFiles, apiURL, grpcAddr, &cfg, &SQLiteTestDB{DBURL: dbURL}, map[string]interface{}{
		"mockPrometheus": prom,
		"grpcAddr":       grpcAddr,
	})
}

func collectSQLiteRESTFeatures(t *testing.T) []string {
	t.Helper()

	featuresDir := filepath.Join("testdata", "features")
	if _, err := os.Stat(featuresDir); os.IsNotExist(err) {
		t.Skipf("Feature files directory not found: %s", featuresDir)
	}

	all, err := filepath.Glob(filepath.Join(featuresDir, "*.feature"))
	require.NoError(t, err)

	var filtered []string
	for _, featurePath := range all {
		base := filepath.Base(featurePath)
		switch base {
		case "memory-cache-rest.feature", "response-recorder-grpc.feature":
			continue
		}
		if strings.Contains(base, "-grpc") {
			continue
		}
		filtered = append(filtered, featurePath)
	}
	require.NotEmpty(t, filtered, "No SQLite REST feature files found in %s", featuresDir)
	return filtered
}

func runBDDFeatures(t *testing.T, suiteName string, featureFiles []string, apiURL, grpcAddr string, cfg *config.Config, db cucumber.TestDB, extra map[string]interface{}) {
	t.Helper()

	opts := cucumber.DefaultOptions()
	opts.Concurrency = 1
	for _, arg := range os.Args[1:] {
		if arg == "-test.v=true" || arg == "-test.v" || arg == "-v" {
			opts.Format = "pretty"
		}
	}

	for _, featurePath := range featureFiles {
		name := strings.TrimSuffix(filepath.Base(featurePath), ".feature")
		t.Run(name, func(t *testing.T) {
			o := opts
			o.TestingT = t
			o.Paths = []string{featurePath}
			defer cucumber.ApplyReportOptions(&o, t.Name())()

			suite := cucumber.NewTestSuite()
			suite.APIURL = apiURL
			suite.TestingT = t
			suite.Context = cfg
			suite.DB = db
			for k, v := range extra {
				suite.Extra[k] = v
			}

			status := godog.TestSuite{
				Name:                suiteName + "-" + name,
				Options:             &o,
				ScenarioInitializer: suite.InitializeScenario,
			}.Run()
			if status != 0 {
				t.Fail()
			}
		})
	}
}
