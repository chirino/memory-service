package testqdrant

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// StartQdrant starts a disposable Qdrant container and returns the gRPC host:port.
func StartQdrant(tb testing.TB) string {
	tb.Helper()

	ctx := context.Background()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "qdrant/qdrant:latest",
			ExposedPorts: []string{"6334/tcp"},
			WaitingFor:   wait.ForListeningPort("6334/tcp").WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		tb.Fatalf("start qdrant container: %v", err)
	}

	tb.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := container.Terminate(ctx); err != nil {
			tb.Errorf("terminate qdrant container: %v", err)
		}
	})

	host, err := container.Host(ctx)
	if err != nil {
		tb.Fatalf("get qdrant host: %v", err)
	}
	mappedPort, err := container.MappedPort(ctx, "6334")
	if err != nil {
		tb.Fatalf("get qdrant mapped port: %v", err)
	}

	return fmt.Sprintf("%s:%s", host, mappedPort.Port())
}
