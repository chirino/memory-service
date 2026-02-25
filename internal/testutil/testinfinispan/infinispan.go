package testinfinispan

import (
	"context"
	"fmt"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Infinispan holds connection details for a running Infinispan container.
type Infinispan struct {
	Host     string // host:port (e.g. "localhost:11222")
	Username string
	Password string
}

// StartInfinispan starts a disposable Infinispan container with RESP protocol enabled
// and returns its connection details.
func StartInfinispan(tb testing.TB) Infinispan {
	tb.Helper()

	const (
		username = "admin"
		password = "password"
	)

	ctx := context.Background()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "quay.io/infinispan/server:15.2",
			ExposedPorts: []string{"11222/tcp"},
			Env: map[string]string{
				"USER": username,
				"PASS": password,
			},
			WaitingFor: wait.ForAll(
				wait.ForListeningPort("11222/tcp"),
				wait.ForLog("Infinispan Server"),
				wait.ForLog("Started connector Resp"),
			).WithDeadline(90 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		tb.Fatalf("start infinispan container: %v", err)
	}

	tb.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := container.Terminate(ctx); err != nil {
			tb.Errorf("terminate infinispan container: %v", err)
		}
	})

	host, err := container.Host(ctx)
	if err != nil {
		tb.Fatalf("get infinispan host: %v", err)
	}
	mappedPort, err := container.MappedPort(ctx, "11222")
	if err != nil {
		tb.Fatalf("get infinispan mapped port: %v", err)
	}
	hostPort := fmt.Sprintf("%s:%s", host, mappedPort.Port())

	// The RESP endpoint may lag behind the HTTP endpoint. Retry ping via go-redis.
	if err := waitForRESP(ctx, hostPort, username, password); err != nil {
		tb.Fatalf("infinispan RESP not ready: %v", err)
	}

	return Infinispan{
		Host:     hostPort,
		Username: username,
		Password: password,
	}
}

func waitForRESP(ctx context.Context, hostPort, username, password string) error {
	// Infinispan's RESP endpoint does not support the RESP3 HELLO command,
	// so we must use Protocol 2 (RESP2) to avoid a handshake hang.
	client := goredis.NewClient(&goredis.Options{
		Addr:     hostPort,
		Username: username,
		Password: password,
		Protocol: 2,
	})
	defer client.Close()

	deadline := time.Now().Add(60 * time.Second)
	var lastErr error
	attempts := 0
	for time.Now().Before(deadline) {
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		lastErr = client.Ping(pingCtx).Err()
		cancel()
		attempts++
		if lastErr == nil {
			return nil
		}
		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("RESP ping timeout after %d attempts: %w", attempts, lastErr)
}
