//go:build !nopostgresql

package postgres

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	"github.com/chirino/memory-service/internal/testutil/testpg"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
)

func TestPostgresBusPublishesRecoveryInvalidateAfterPublishFailure(t *testing.T) {
	ctx := testPostgresBusContext(t)
	dsn := testpg.StartPostgres(t)

	busA := mustLoadPostgresBus(t, ctx, dsn)
	defer func() { require.NoError(t, busA.Close()) }()
	busB := mustLoadPostgresBus(t, ctx, dsn)
	defer func() { require.NoError(t, busB.Close()) }()

	subCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	peerEvents, err := busB.Subscribe(subCtx)
	require.NoError(t, err)

	require.NoError(t, busA.currentDB().Close())
	require.NoError(t, busA.Publish(context.Background(), registryeventbus.Event{
		Event: "created",
		Kind:  "conversation",
		Data:  map[string]any{"conversation": "local-only"},
	}))
	require.Eventually(t, busA.isDegraded, 5*time.Second, 50*time.Millisecond)

	replacement := openPostgresDB(t, dsn)
	busA.setDB(replacement)

	require.NoError(t, busA.Publish(context.Background(), registryeventbus.Event{
		Event: "updated",
		Kind:  "conversation",
		Data:  map[string]any{"conversation": "after-recovery"},
	}))

	invalidate := waitForPostgresEvent(t, peerEvents, 10*time.Second, func(event registryeventbus.Event) bool {
		return event.Kind == "stream" && event.Event == "invalidate"
	})
	require.Equal(t, "pubsub recovery", postgresEventReason(invalidate))
}

func TestPostgresBusPublishesRecoveryInvalidateAfterSubscriptionLoss(t *testing.T) {
	ctx := testPostgresBusContext(t)
	dsn := testpg.StartPostgres(t)

	busA := mustLoadPostgresBus(t, ctx, dsn)
	defer func() { require.NoError(t, busA.Close()) }()
	busB := mustLoadPostgresBus(t, ctx, dsn)
	defer func() { require.NoError(t, busB.Close()) }()

	subCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	localEvents, err := busB.Subscribe(subCtx)
	require.NoError(t, err)
	peerEvents, err := busA.Subscribe(subCtx)
	require.NoError(t, err)

	require.NoError(t, busB.currentDB().Close())
	localInvalidate := waitForPostgresEvent(t, localEvents, 10*time.Second, func(event registryeventbus.Event) bool {
		return event.Kind == "stream" && event.Event == "invalidate"
	})
	require.Equal(t, "pubsub recovery", postgresEventReason(localInvalidate))
	require.Eventually(t, busB.isDegraded, 5*time.Second, 50*time.Millisecond)

	replacement := openPostgresDB(t, dsn)
	busB.setDB(replacement)
	require.NoError(t, busB.Publish(context.Background(), registryeventbus.Event{
		Event: "updated",
		Kind:  "conversation",
		Data:  map[string]any{"conversation": "after-subscription-recovery"},
	}))

	peerInvalidate := waitForPostgresEvent(t, peerEvents, 10*time.Second, func(event registryeventbus.Event) bool {
		return event.Kind == "stream" && event.Event == "invalidate"
	})
	require.Equal(t, "pubsub recovery", postgresEventReason(peerInvalidate))
}

func testPostgresBusContext(t *testing.T) context.Context {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.EventBusBatchSize = 1
	cfg.SSESubscriberBufferSize = 8
	return config.WithContext(context.Background(), &cfg)
}

func mustLoadPostgresBus(t *testing.T, ctx context.Context, dsn string) *postgresBus {
	t.Helper()
	cfg := config.FromContext(ctx)
	cfg.DBURL = dsn
	bus, err := load(ctx)
	require.NoError(t, err)
	typed, ok := bus.(*postgresBus)
	require.True(t, ok)
	return typed
}

func openPostgresDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	require.NoError(t, db.PingContext(context.Background()))
	return db
}

func waitForPostgresEvent(t *testing.T, ch <-chan registryeventbus.Event, timeout time.Duration, match func(registryeventbus.Event) bool) registryeventbus.Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case event, ok := <-ch:
			require.True(t, ok, "event channel closed before matching event")
			if match(event) {
				return event
			}
		case <-deadline:
			t.Fatalf("timed out after %s waiting for event", timeout)
		}
	}
}

func postgresEventReason(event registryeventbus.Event) string {
	switch data := event.Data.(type) {
	case map[string]any:
		if reason, ok := data["reason"].(string); ok {
			return reason
		}
	case map[string]string:
		return data["reason"]
	}
	return ""
}
