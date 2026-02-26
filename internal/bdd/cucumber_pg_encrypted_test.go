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
	"github.com/chirino/memory-service/internal/testutil/testpg"
	"github.com/cucumber/godog"
	"github.com/stretchr/testify/require"

	// Import plugins to trigger init() registration
	_ "github.com/chirino/memory-service/internal/plugin/attach/pgstore"
	_ "github.com/chirino/memory-service/internal/plugin/cache/noop"
	_ "github.com/chirino/memory-service/internal/plugin/embed/disabled"
	_ "github.com/chirino/memory-service/internal/plugin/route/system"
)

// testEncryptionKey is a 64-hex-char (32-byte) AES-256 key for testing.
const testEncryptionKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestFeaturesPgEncrypted(t *testing.T) {
	_ = postgres.ForceImport

	dbURL := testpg.StartPostgres(t)
	prom := NewMockPrometheus(t)

	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeTesting
	cfg.DBURL = dbURL
	cfg.CacheType = "none"
	cfg.AttachType = "db"
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

	// Run only features-encrypted/ feature files
	resourcesDir := "testdata"
	encryptedDir := filepath.Join(resourcesDir, "features-encrypted")
	if _, err := os.Stat(encryptedDir); os.IsNotExist(err) {
		t.Skipf("Encrypted feature files directory not found: %s", encryptedDir)
	}

	featureFiles, err := filepath.Glob(filepath.Join(encryptedDir, "*.feature"))
	require.NoError(t, err)
	require.NotEmpty(t, featureFiles, "No encrypted feature files found in %s", encryptedDir)

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
