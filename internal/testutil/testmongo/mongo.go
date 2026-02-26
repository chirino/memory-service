package testmongo

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go/modules/mongodb"
)

// StartMongo starts a disposable MongoDB container and returns its connection URI.
func StartMongo(tb testing.TB) string {
	tb.Helper()

	ctx := context.Background()
	container, err := mongodb.Run(ctx, "mongo:7")
	if err != nil {
		tb.Fatalf("start mongodb container: %v", err)
	}

	tb.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := container.Terminate(ctx); err != nil {
			tb.Errorf("terminate mongodb container: %v", err)
		}
	})

	uri, err := container.ConnectionString(ctx)
	if err != nil {
		tb.Fatalf("build mongodb connection string: %v", err)
	}

	return uri
}
