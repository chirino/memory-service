package clickhouse

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/testutil/testclickhouse"
	"github.com/stretchr/testify/require"
)

// Integration tests share one ClickHouse container per package run. Set
// MEMORY_SERVICE_TEST_CLICKHOUSE_ADDRESS (and optionally
// MEMORY_SERVICE_TEST_CLICKHOUSE_HTTP_ADDRESS) to use an existing server that has
// deploy/clickhouse/init.sql applied instead.
var (
	integrationServerOnce sync.Once
	integrationServer     *testclickhouse.Server
	integrationServerErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if integrationServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_ = integrationServer.Terminate(ctx)
		cancel()
	}
	os.Exit(code)
}

// requireClickHouseIntegration starts the shared ClickHouse server on first use.
func requireClickHouseIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("MEMORY_SERVICE_TEST_CLICKHOUSE_ADDRESS") != "" {
		return
	}
	integrationServerOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		integrationServer, integrationServerErr = testclickhouse.Start(ctx)
	})
	require.NoError(t, integrationServerErr, "start ClickHouse test container")
}

func integrationNativeAddress() string {
	if address := os.Getenv("MEMORY_SERVICE_TEST_CLICKHOUSE_ADDRESS"); address != "" {
		return address
	}
	return integrationServer.NativeAddress
}

func integrationHTTPAddress() string {
	if address := os.Getenv("MEMORY_SERVICE_TEST_CLICKHOUSE_HTTP_ADDRESS"); address != "" {
		return address
	}
	if os.Getenv("MEMORY_SERVICE_TEST_CLICKHOUSE_ADDRESS") != "" {
		return "localhost:8123"
	}
	return integrationServer.HTTPAddress
}
