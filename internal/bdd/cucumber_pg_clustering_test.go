package bdd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/cmd/serve"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/plugin/store/postgres"
	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/chirino/memory-service/internal/testutil/testpg"
	"github.com/cucumber/godog"
	"github.com/stretchr/testify/require"

	// Import plugins to trigger init() registration
	_ "github.com/chirino/memory-service/internal/plugin/attach/pgstore"
	_ "github.com/chirino/memory-service/internal/plugin/cache/local"
	_ "github.com/chirino/memory-service/internal/plugin/embed/local"
	_ "github.com/chirino/memory-service/internal/plugin/route/system"
	_ "github.com/chirino/memory-service/internal/plugin/vector/pgvector"
)

func TestFeaturesPgClustering(t *testing.T) {
	_ = postgres.ForceImport

	dbURL := testpg.StartPostgres(t)
	prom := NewMockPrometheus(t)

	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeTesting
	cfg.DBURL = dbURL
	cfg.CacheType = "local"
	cfg.VectorType = "pgvector"
	cfg.VectorMigrateAtStart = true
	cfg.EmbedType = "local"
	cfg.SearchSemanticEnabled = true
	cfg.SearchFulltextEnabled = true
	cfg.EncryptionKey = testEncryptionKey
	cfg.EncryptionDBDisabled = true
	cfg.EncryptionAttachmentsDisabled = true
	cfg.AdminUsers = bddAdminUsers()
	cfg.AuditorUsers = bddAuditorUsers()
	cfg.IndexerUsers = bddIndexerUsers()
	cfg.PrometheusURL = prom.Server.URL
	cfg.Listener.Port = 0
	cfg.Listener.EnableTLS = false

	// Enable clustering with fast interval for testing.
	cfg.KnowledgeClusteringEnabled = true
	cfg.KnowledgeClusteringInterval = 5 * time.Second
	cfg.KnowledgeClusteringEpsilon = 0.3
	cfg.KnowledgeClusteringMinPts = 1
	cfg.KnowledgeClusteringDecay = 24 * time.Hour

	ctx := config.WithContext(context.Background(), &cfg)

	srv, err := serve.StartServer(ctx, &cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	apiURL := fmt.Sprintf("http://localhost:%d", srv.Running.Port)

	featureFiles := []string{
		filepath.Join("testdata", "features", "clustering-rest.feature"),
	}

	opts := cucumber.DefaultOptions()
	opts.Concurrency = 1 // Sequential — clustering depends on timing
	for _, arg := range os.Args[1:] {
		if arg == "-test.v=true" || arg == "-test.v" || arg == "-v" {
			opts.Format = "pretty"
		}
	}

	for _, featurePath := range featureFiles {
		name := strings.TrimSuffix(filepath.Base(featurePath), ".feature")
		t.Run(name, func(t *testing.T) {
			clearFeatureDB(t, &PostgresTestDB{DBURL: dbURL})

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
