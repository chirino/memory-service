package bdd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/chirino/memory-service/internal/cmd/serve"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/cucumber/godog"
)

func newSQLiteScenarioSetup(t *testing.T, baseCfg config.Config) cucumber.ScenarioSetupFunc {
	t.Helper()

	return func(ctx context.Context, s *cucumber.TestScenario, sc *godog.Scenario) (func(context.Context) error, error) {
		_ = ctx
		_ = sc

		tempDir, err := os.MkdirTemp("", "memory-service-sqlite-"+s.ScenarioUID+"-")
		if err != nil {
			return nil, err
		}
		prom := newMockPrometheus()

		cfg := baseCfg
		cfg.DBURL = filepath.Join(tempDir, "memory.db")
		cfg.PrometheusURL = prom.Server.URL

		cleanup, err := startScenarioServer(s, &cfg, &SQLiteTestDB{DBURL: cfg.DBURL}, map[string]interface{}{
			"mockPrometheus": prom,
		})
		if err != nil {
			prom.Server.Close()
			_ = os.RemoveAll(tempDir)
			return nil, err
		}

		return func(context.Context) error {
			return errors.Join(cleanup(context.Background()), closePrometheusAndDir(prom, tempDir))
		}, nil
	}
}

func startScenarioServer(s *cucumber.TestScenario, cfg *config.Config, db cucumber.TestDB, extra map[string]interface{}) (func(context.Context) error, error) {
	ctx := config.WithContext(context.Background(), cfg)
	srv, err := serve.StartServer(ctx, cfg)
	if err != nil {
		return nil, err
	}

	s.APIURL = fmt.Sprintf("http://localhost:%d", srv.Running.Port)
	s.DB = db
	s.Extra = cucumberCloneExtras(s.Extra, extra)
	s.Extra["grpcAddr"] = fmt.Sprintf("localhost:%d", srv.Running.Port)

	return func(context.Context) error {
		return srv.Shutdown(context.Background())
	}, nil
}

func cucumberCloneExtras(base map[string]interface{}, extra map[string]interface{}) map[string]interface{} {
	merged := make(map[string]interface{}, len(base)+len(extra)+1)
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range extra {
		merged[k] = v
	}
	return merged
}

func closePrometheusAndDir(prom *MockPrometheus, dir string) error {
	var errs []error
	if prom != nil && prom.Server != nil {
		prom.Server.Close()
	}
	if dir != "" {
		if err := os.RemoveAll(dir); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
