//go:build !nopostgresql

package pgvector

import (
	"context"
	"fmt"

	internaltracing "github.com/chirino/memory-service/internal/tracing"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/plugin/opentelemetry/tracing"
)

func openGormDB(ctx context.Context, dsn string) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Discard,
	})
	if err != nil {
		return nil, err
	}
	tp := internaltracing.ProviderFromContextOrNoop(ctx)
	if err := db.Use(tracing.NewPlugin(
		tracing.WithTracerProvider(tp),
		tracing.WithoutMetrics(),
		tracing.WithoutQueryVariables(),
	)); err != nil {
		return nil, fmt.Errorf("pgvector: register otelgorm plugin: %w", err)
	}
	return db, nil
}
