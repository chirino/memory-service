package testredis

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// StartRedis starts a disposable Redis container and returns a redis:// URL.
func StartRedis(tb testing.TB) string {
	tb.Helper()

	ctx := context.Background()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "redis:7",
			ExposedPorts: []string{"6379/tcp"},
			WaitingFor:   wait.ForListeningPort("6379/tcp").WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		tb.Fatalf("start redis container: %v", err)
	}

	tb.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := container.Terminate(ctx); err != nil {
			tb.Errorf("terminate redis container: %v", err)
		}
	})

	host, err := container.Host(ctx)
	if err != nil {
		tb.Fatalf("get redis host: %v", err)
	}
	mappedPort, err := container.MappedPort(ctx, "6379")
	if err != nil {
		tb.Fatalf("get redis mapped port: %v", err)
	}

	return fmt.Sprintf("redis://%s:%s", host, mappedPort.Port())
}
