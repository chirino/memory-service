package bdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/cucumber/godog"
)

func runBDDFeatures(t *testing.T, suiteName string, featureFiles []string, apiURL, grpcAddr string, cfg *config.Config, db cucumber.TestDB, extra map[string]interface{}) {
	runBDDFeaturesWithConcurrency(t, suiteName, featureFiles, apiURL, grpcAddr, cfg, db, extra, bddScenarioConcurrency())
}

func runBDDFeaturesWithConcurrency(t *testing.T, suiteName string, featureFiles []string, apiURL, grpcAddr string, cfg *config.Config, db cucumber.TestDB, extra map[string]interface{}, concurrency int) {
	t.Helper()

	opts := cucumber.DefaultOptions()
	opts.Concurrency = concurrency
	for _, arg := range os.Args[1:] {
		if arg == "-test.v=true" || arg == "-test.v" || arg == "-v" {
			opts.Format = "pretty"
		}
	}

	for _, featurePath := range featureFiles {
		name := strings.TrimSuffix(filepath.Base(featurePath), ".feature")
		t.Run(name, func(t *testing.T) {
			clearFeatureDB(t, db)

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
