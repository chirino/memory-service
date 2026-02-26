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
	mongoplugin "github.com/chirino/memory-service/internal/plugin/store/mongo"
	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/chirino/memory-service/internal/testutil/testmongo"
	"github.com/chirino/memory-service/internal/testutil/testqdrant"
	"github.com/chirino/memory-service/internal/testutil/testredis"
	"github.com/cucumber/godog"
	"github.com/stretchr/testify/require"

	// Import plugins to trigger init() registration
	_ "github.com/chirino/memory-service/internal/plugin/attach/mongostore"
	_ "github.com/chirino/memory-service/internal/plugin/cache/redis"
	_ "github.com/chirino/memory-service/internal/plugin/embed/disabled"
	_ "github.com/chirino/memory-service/internal/plugin/route/system"
	_ "github.com/chirino/memory-service/internal/plugin/vector/qdrant"
)

// mongoSkipFeatures lists feature files that cannot run on MongoDB.
var mongoSkipFeatures = map[string]bool{}

func TestFeaturesMongo(t *testing.T) {
	_ = mongoplugin.ForceImport

	mongoURL := testmongo.StartMongo(t)
	redisURL := testredis.StartRedis(t)
	qdrantHost := testqdrant.StartQdrant(t)
	prom := NewMockPrometheus(t)

	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeTesting
	cfg.DatastoreType = "mongo"
	cfg.AttachType = "mongo"
	cfg.DBURL = mongoURL
	cfg.CacheType = "redis"
	cfg.RedisURL = redisURL
	cfg.VectorType = "qdrant"
	cfg.QdrantHost = qdrantHost
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

	// Discover feature files: main features/ + features-qdrant/ + features-grpc/ + features-encrypted/
	resourcesDir := "testdata"
	featuresDir := filepath.Join(resourcesDir, "features")
	if _, err := os.Stat(featuresDir); os.IsNotExist(err) {
		t.Skipf("Feature files directory not found: %s", featuresDir)
	}

	featureFiles, err := filepath.Glob(filepath.Join(featuresDir, "*.feature"))
	require.NoError(t, err)

	// Add subdirectory features
	subdirPatterns := []string{"features-qdrant", "features-grpc", "features-encrypted"}
	for _, subdir := range subdirPatterns {
		subFiles, _ := filepath.Glob(filepath.Join(resourcesDir, subdir, "*.feature"))
		featureFiles = append(featureFiles, subFiles...)
	}
	require.NotEmpty(t, featureFiles, "No feature files found")

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
		if mongoSkipFeatures[name] {
			t.Run(name, func(t *testing.T) {
				t.Skipf("Skipped: requires Postgres-only features")
			})
			continue
		}
		t.Run(name, func(t *testing.T) {
			o := opts
			o.TestingT = t
			o.Paths = []string{featurePath}
			defer cucumber.ApplyReportOptions(&o, t.Name())()

			suite := cucumber.NewTestSuite()
			suite.APIURL = apiURL
			suite.TestingT = t
			suite.Context = &cfg
			suite.DB = &MongoTestDB{DBURL: mongoURL}
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
