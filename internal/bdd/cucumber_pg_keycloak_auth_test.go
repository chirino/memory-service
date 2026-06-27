package bdd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chirino/memory-service/internal/buildcaps"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/chirino/memory-service/internal/testutil/testkeycloak"
	"github.com/chirino/memory-service/internal/testutil/testpg"
	"github.com/cucumber/godog"
)

// TestFeaturesPgKeycloakAuthClients exercises OIDC client allowlisting, API-key pairing,
// and user-scoped rejection scenarios using a real Keycloak instance.
// Each scenario starts a fresh in-process memory-service with the config it needs.
// This runner does NOT use the auth_testfixtures build tag — it runs in production auth mode.
func TestFeaturesPgKeycloakAuthClients(t *testing.T) {
	if !buildcaps.PostgreSQL {
		requireCapabilities(t, "postgresql")
	}

	dbURL := testpg.StartPostgres(t)
	keycloak := testkeycloak.StartKeycloak(t)

	featurePath := filepath.Join("testdata", "features-oidc", "auth-clients-rest.feature")
	if _, err := os.Stat(featurePath); os.IsNotExist(err) {
		t.Skipf("Feature file not found: %s", featurePath)
	}

	opts := cucumber.DefaultOptions()
	opts.Concurrency = 1 // scenarios must run sequentially (each restarts the server)
	for _, arg := range os.Args[1:] {
		if arg == "-test.v=true" || arg == "-test.v" || arg == "-v" {
			opts.Format = "pretty"
		}
	}

	name := "auth-clients-rest"
	t.Run(name, func(t *testing.T) {
		clearFeatureDB(t, &PostgresTestDB{DBURL: dbURL})

		o := opts
		o.TestingT = t
		o.Paths = []string{featurePath}
		defer cucumber.ApplyReportOptions(&o, t.Name())()

		// The suite has no persistent server: each scenario starts its own.
		suite := cucumber.NewTestSuite()
		suite.APIURL = "" // will be set per-scenario by auth mode steps
		suite.TestingT = t
		suite.Context = &config.Config{}
		suite.DB = &PostgresTestDB{DBURL: dbURL}
		// Provide Keycloak as OIDC token provider and the shared DB URL for per-scenario servers.
		suite.Extra[OIDCTokenProviderExtraKey] = keycloak
		suite.Extra[AuthModeDBURLKey] = dbURL

		status := godog.TestSuite{
			Name:                "pg-keycloak-" + name,
			Options:             &o,
			ScenarioInitializer: suite.InitializeScenario,
		}.Run()
		if status != 0 {
			t.Fail()
		}
	})
}
