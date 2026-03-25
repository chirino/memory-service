package bdd

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chirino/memory-service/internal/cmd/serve"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/cucumber/godog"
	"github.com/jackc/pgx/v5"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
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

func newPostgresScenarioSetup(t *testing.T, baseCfg config.Config) cucumber.ScenarioSetupFunc {
	t.Helper()

	return func(ctx context.Context, s *cucumber.TestScenario, sc *godog.Scenario) (func(context.Context) error, error) {
		_ = ctx
		_ = sc

		schema := "bdd_" + sanitizeScenarioName(s.ScenarioUID)
		if err := createPostgresSchema(context.Background(), baseCfg.DBURL, schema); err != nil {
			return nil, err
		}

		dbURL, err := postgresURLWithSearchPath(baseCfg.DBURL, schema)
		if err != nil {
			_ = dropPostgresSchema(context.Background(), baseCfg.DBURL, schema)
			return nil, err
		}

		prom := newMockPrometheus()
		cfg := baseCfg
		cfg.DBURL = dbURL
		cfg.PrometheusURL = prom.Server.URL

		cleanup, err := startScenarioServer(s, &cfg, &PostgresTestDB{DBURL: dbURL}, map[string]interface{}{
			"mockPrometheus": prom,
		})
		if err != nil {
			_ = dropPostgresSchema(context.Background(), baseCfg.DBURL, schema)
			prom.Server.Close()
			return nil, err
		}

		return func(context.Context) error {
			return errors.Join(
				cleanup(context.Background()),
				dropPostgresSchema(context.Background(), baseCfg.DBURL, schema),
				closePrometheusAndDir(prom, ""),
			)
		}, nil
	}
}

func newMongoScenarioSetup(t *testing.T, baseCfg config.Config) cucumber.ScenarioSetupFunc {
	t.Helper()

	return func(ctx context.Context, s *cucumber.TestScenario, sc *godog.Scenario) (func(context.Context) error, error) {
		_ = ctx
		_ = sc

		dbName := "memory_service_" + sanitizeScenarioName(s.ScenarioUID)
		dbURL, err := mongoURLWithDatabase(baseCfg.DBURL, dbName)
		if err != nil {
			return nil, err
		}

		prom := newMockPrometheus()
		cfg := baseCfg
		cfg.DBURL = dbURL
		cfg.PrometheusURL = prom.Server.URL
		if strings.TrimSpace(cfg.QdrantCollectionName) == "" {
			cfg.QdrantCollectionPrefix = "memory-service-bdd-" + sanitizeScenarioName(s.ScenarioUID)
		}

		cleanup, err := startScenarioServer(s, &cfg, &MongoTestDB{DBURL: dbURL}, map[string]interface{}{
			"mockPrometheus": prom,
		})
		if err != nil {
			prom.Server.Close()
			_ = dropMongoDatabase(context.Background(), dbURL)
			return nil, err
		}

		return func(context.Context) error {
			return errors.Join(
				cleanup(context.Background()),
				dropMongoDatabase(context.Background(), dbURL),
				closePrometheusAndDir(prom, ""),
			)
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

func sanitizeScenarioName(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "scenario"
	}
	return b.String()
}

func postgresURLWithSearchPath(baseURL, schema string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func createPostgresSchema(ctx context.Context, baseURL, schema string) error {
	conn, err := pgx.Connect(ctx, baseURL)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	_, err = conn.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+schema)
	return err
}

func dropPostgresSchema(ctx context.Context, baseURL, schema string) error {
	conn, err := pgx.Connect(ctx, baseURL)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	_, err = conn.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
	return err
}

func mongoURLWithDatabase(baseURL, dbName string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	parsed.Path = "/" + dbName
	return parsed.String(), nil
}

func dropMongoDatabase(ctx context.Context, dbURL string) error {
	client, err := mongo.Connect(options.Client().ApplyURI(dbURL))
	if err != nil {
		return err
	}
	defer client.Disconnect(ctx)

	return client.Database(config.MongoDatabaseName(dbURL)).Drop(ctx)
}
