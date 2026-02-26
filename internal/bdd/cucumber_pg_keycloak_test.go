package bdd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/chirino/memory-service/internal/cmd/serve"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/plugin/store/postgres"
	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/chirino/memory-service/internal/testutil/testkeycloak"
	"github.com/chirino/memory-service/internal/testutil/testpg"
	"github.com/cucumber/godog"
	"github.com/stretchr/testify/require"

	// Import plugins to trigger init() registration.
	_ "github.com/chirino/memory-service/internal/plugin/attach/pgstore"
	_ "github.com/chirino/memory-service/internal/plugin/cache/noop"
	_ "github.com/chirino/memory-service/internal/plugin/embed/disabled"
	_ "github.com/chirino/memory-service/internal/plugin/route/system"
)

func TestFeaturesPgKeycloak(t *testing.T) {
	_ = postgres.ForceImport

	dbURL := testpg.StartPostgres(t)
	keycloak := testkeycloak.StartKeycloak(t)

	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeProd // to require validation of the tokens
	cfg.DBURL = dbURL
	cfg.CacheType = "none"
	cfg.AttachType = "db"
	cfg.SearchSemanticEnabled = false
	cfg.SearchFulltextEnabled = false
	cfg.OIDCIssuer = keycloak.IssuerURL
	cfg.OIDCDiscoveryURL = keycloak.DiscoveryURL
	cfg.AdminOIDCRole = "admin"
	cfg.AuditorOIDCRole = "auditor"
	cfg.IndexerOIDCRole = "indexer"
	cfg.Listener.Port = 0
	cfg.Listener.EnableTLS = false
	ctx := config.WithContext(context.Background(), &cfg)

	srv, err := serve.StartServer(ctx, &cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	apiURL := fmt.Sprintf("http://localhost:%d", srv.Running.Port)
	featurePath := filepath.Join("testdata", "features-oidc", "keycloak-oidc-rest.feature")
	if _, err := os.Stat(featurePath); os.IsNotExist(err) {
		t.Skipf("Feature file not found: %s", featurePath)
	}

	opts := cucumber.DefaultOptions()
	opts.Concurrency = 1
	for _, arg := range os.Args[1:] {
		if arg == "-test.v=true" || arg == "-test.v" || arg == "-v" {
			opts.Format = "pretty"
		}
	}

	t.Run("keycloak-oidc-rest", func(t *testing.T) {
		o := opts
		o.TestingT = t
		o.Paths = []string{featurePath}
		defer cucumber.ApplyReportOptions(&o, t.Name())()

		suite := cucumber.NewTestSuite()
		suite.APIURL = apiURL
		suite.TestingT = t
		suite.Context = &cfg
		suite.DB = &PostgresTestDB{DBURL: dbURL}
		suite.Extra[OIDCTokenProviderExtraKey] = keycloak

		status := godog.TestSuite{
			Name:                "keycloak-oidc-rest",
			Options:             &o,
			ScenarioInitializer: suite.InitializeScenario,
		}.Run()
		if status != 0 {
			t.Fail()
		}
	})
}
