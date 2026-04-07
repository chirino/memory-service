package bdd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/buildcaps"
	"github.com/chirino/memory-service/internal/cmd/serve"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/chirino/memory-service/internal/testutil/testpg"
	"github.com/cucumber/godog"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestFeaturesPgInfinispan(t *testing.T) {
	var missing []string
	if !buildcaps.PostgreSQL {
		missing = append(missing, "postgresql")
	}
	if !buildcaps.Infinispan {
		missing = append(missing, "infinispan")
	}
	if len(missing) > 0 {
		requireCapabilities(t, missing...)
	}

	dbURL := testpg.StartPostgres(t)
	infinispanURL := startInfinispanForVectorSearch(t)
	prom := NewMockPrometheus(t)

	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeTesting
	cfg.DBURL = dbURL
	cfg.CacheType = "local"
	cfg.VectorType = "infinispan"
	cfg.InfinispanVectorURL = infinispanURL
	cfg.InfinispanVectorUsername = "admin"
	cfg.InfinispanVectorPassword = "password"
	cfg.VectorMigrateAtStart = true
	cfg.EmbedType = "disabled"
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

	// Discover feature files: main REST features/ + features-encrypted/
	resourcesDir := "testdata"
	featureFiles := collectRESTFeatureFiles(t, resourcesDir, nil)

	// Add encrypted features
	encryptedFiles, _ := filepath.Glob(filepath.Join(resourcesDir, "features-encrypted", "*.feature"))
	featureFiles = append(featureFiles, encryptedFiles...)

	featureFiles = filterSerialFeatures(featureFiles, false)
	require.NotEmpty(t, featureFiles, "No feature files found")

	// Configure godog options
	opts := cucumber.DefaultOptions()
	opts.Concurrency = bddScenarioConcurrency()
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

// startInfinispanForVectorSearch starts Infinispan 16.1+ container for vector search testing.
// Uses a newer version than the RESP-focused testinfinispan utility.
func startInfinispanForVectorSearch(tb testing.TB) string {
	tb.Helper()

	ctx := context.Background()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "quay.io/infinispan/server:16.1",
			ExposedPorts: []string{"11222/tcp"},
			Env: map[string]string{
				"USER": "admin",
				"PASS": "password",
			},
			WaitingFor: wait.ForHTTP("/rest/v3/container/health/status").
				WithPort("11222/tcp").
				WithStartupTimeout(90 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		tb.Fatalf("start infinispan container: %v", err)
	}

	tb.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := container.Terminate(ctx); err != nil {
			tb.Errorf("terminate infinispan container: %v", err)
		}
	})

	host, err := container.Host(ctx)
	if err != nil {
		tb.Fatalf("get infinispan host: %v", err)
	}
	mappedPort, err := container.MappedPort(ctx, "11222")
	if err != nil {
		tb.Fatalf("get infinispan mapped port: %v", err)
	}

	return fmt.Sprintf("http://%s:%s", host, mappedPort.Port())
}
