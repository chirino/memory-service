package testpg

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// StartPostgres starts a disposable Postgres container and returns its DSN.
func StartPostgres(tb testing.TB) string {
	tb.Helper()

	ctx := context.Background()
	container, err := postgres.Run(
		ctx,
		"pgvector/pgvector:pg18",
		postgres.WithDatabase("postgres"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForListeningPort("5432/tcp"),
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2),
			).WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		tb.Fatalf("start postgres container: %v", err)
	}

	tb.Cleanup(func() {
		if err := container.Terminate(ctx); err != nil {
			tb.Errorf("terminate postgres container: %v", err)
		}
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		tb.Fatalf("build postgres connection string: %v", err)
	}

	if dsn == "" {
		tb.Fatalf("empty postgres connection string")
	}
	if err := waitForReady(ctx, dsn); err != nil {
		tb.Fatalf("postgres is not ready for connections: %v", err)
	}

	return dsn
}

func waitForReady(ctx context.Context, dsn string) error {
	deadline := time.Now().Add(20 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		attemptCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		conn, err := pgx.Connect(attemptCtx, dsn)
		if err == nil {
			lastErr = conn.Ping(attemptCtx)
			_ = conn.Close(attemptCtx)
			cancel()
			if lastErr == nil {
				return nil
			}
		} else {
			lastErr = err
			cancel()
		}
		time.Sleep(250 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = context.DeadlineExceeded
	}
	return lastErr
}
