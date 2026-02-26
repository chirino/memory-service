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
	"github.com/chirino/memory-service/internal/plugin/store/postgres"
	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/chirino/memory-service/internal/testutil/testinfinispan"
	"github.com/chirino/memory-service/internal/testutil/testpg"
	"github.com/cucumber/godog"
	"github.com/stretchr/testify/require"

	// Import plugins to trigger init() registration
	_ "github.com/chirino/memory-service/internal/plugin/attach/pgstore"
	_ "github.com/chirino/memory-service/internal/plugin/cache/infinispan"
	_ "github.com/chirino/memory-service/internal/plugin/embed/disabled"
	_ "github.com/chirino/memory-service/internal/plugin/route/system"
	_ "github.com/chirino/memory-service/internal/plugin/vector/pgvector"
)

func TestFeatures(t *testing.T) {
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

	// Discover feature files
	featuresDir := filepath.Join("..", "..", "memory-service", "src", "test", "resources", "features")
	if _, err := os.Stat(featuresDir); os.IsNotExist(err) {
		t.Skipf("Feature files directory not found: %s", featuresDir)
	}

	featureFiles, err := filepath.Glob(filepath.Join(featuresDir, "*.feature"))
	require.NoError(t, err)
	// Also discover feature files in subdirectories (features-grpc/, features-encrypted/, etc.)
	parentDir := filepath.Dir(featuresDir)
	subdirPatterns := []string{"features-grpc", "features-encrypted"}
	for _, subdir := range subdirPatterns {
		subFiles, _ := filepath.Glob(filepath.Join(parentDir, subdir, "*.feature"))
		featureFiles = append(featureFiles, subFiles...)
	}
	require.NotEmpty(t, featureFiles, "No feature files found in %s", featuresDir)

	// Configure godog options
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
			suite.Context = &cfg
			suite.DB = &PostgresTestDB{DBURL: dbURL}
			suite.Extra["mockPrometheus"] = prom
			suite.Extra["grpcAddr"] = grpcAddr

			status := godog.TestSuite{
				Name:                name,
				Options:             &o,
				ScenarioInitializer: suite.InitializeScenario,
			}.Run()
			if status != 0 {
				t.Fail()
			}
		})
	}
}
