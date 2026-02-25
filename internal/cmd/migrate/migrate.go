package migrate

import (
	"context"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	registrymigrate "github.com/chirino/memory-service/internal/registry/migrate"
	"github.com/urfave/cli/v3"

	// Import plugins to trigger init() registration of their migrators.
	// Store plugins register their own migrators alongside their primary interface.
	_ "github.com/chirino/memory-service/internal/plugin/store/mongo"
	_ "github.com/chirino/memory-service/internal/plugin/store/postgres"
	_ "github.com/chirino/memory-service/internal/plugin/vector/pgvector"
	_ "github.com/chirino/memory-service/internal/plugin/vector/qdrant"
)

// Command returns the migrate sub-command.
func Command() *cli.Command {
	return &cli.Command{
		Name:  "migrate",
		Usage: "Run database migrations",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "db-url",
				Sources:  cli.EnvVars("MEMORY_SERVICE_DB_URL"),
				Usage:    "Database connection URL",
				Required: true,
			},
			&cli.StringFlag{
				Name:    "db-kind",
				Sources: cli.EnvVars("MEMORY_SERVICE_DB_KIND"),
				Usage:   "Store backend (postgres|mongo)",
				Value:   "postgres",
			},
			&cli.StringFlag{
				Name:    "vector-qdrant-host",
				Sources: cli.EnvVars("MEMORY_SERVICE_VECTOR_QDRANT_HOST", "MEMORY_SERVICE_QDRANT_HOST"),
				Usage:   "Qdrant host:port",
				Value:   "localhost:6334",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg := config.DefaultConfig()
			cfg.DBURL = cmd.String("db-url")
			cfg.DatastoreType = cmd.String("db-kind")
			cfg.QdrantHost = cmd.String("vector-qdrant-host")
			if err := cfg.ApplyJavaCompatFromEnv(); err != nil {
				return err
			}
			ctx = config.WithContext(ctx, &cfg)

			log.Info("Running migrations...")
			if err := registrymigrate.RunAll(ctx); err != nil {
				return err
			}
			log.Info("All migrations completed successfully")
			return nil
		},
	}
}
