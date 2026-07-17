package bdd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chirino/memory-service/internal/buildcaps"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/chirino/memory-service/internal/testutil/testpg"
	"github.com/cucumber/godog"
)

// TestFeaturesPgAPIKeys exercises no-OIDC production-mode API-key authentication:
// API-key-only admin access, user-scoped API rejection,
// and gRPC API-key auth scenarios.
// This runner uses NO auth_testfixtures tag and runs in production (ModeProd) auth mode.
func TestFeaturesPgAPIKeys(t *testing.T) {
	if !buildcaps.PostgreSQL {
		requireCapabilities(t, "postgresql")
	}

	dbURL := testpg.StartPostgres(t)

	opts := cucumber.DefaultOptions()
	opts.Concurrency = 1 // scenarios must run sequentially (each restarts the server)
	for _, arg := range os.Args[1:] {
		if arg == "-test.v=true" || arg == "-test.v" || arg == "-v" {
			opts.Format = "pretty"
		}
	}

	for _, name := range []string{"auth-api-keys-rest", "auth-api-keys-grpc", "trusted-user-id-rest", "trusted-user-id-grpc"} {
		featurePath := filepath.Join("testdata", "features", name+".feature")
		if _, err := os.Stat(featurePath); os.IsNotExist(err) {
			t.Skipf("Feature file not found: %s", featurePath)
		}

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
			// Provide the shared DB URL for per-scenario servers. No OIDC provider for this runner.
			suite.Extra[AuthModeDBURLKey] = dbURL

			status := godog.TestSuite{
				Name:                "pg-" + name,
				Options:             &o,
				ScenarioInitializer: suite.InitializeScenario,
			}.Run()
			if status != 0 {
				t.Fail()
			}
		})
	}
}
