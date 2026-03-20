//go:build !noredis || !noinfinispan

package redis

import (
	"context"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	"github.com/chirino/memory-service/internal/testutil/testredis"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestRedisBusPublishesRecoveryInvalidateAfterPublishFailure(t *testing.T) {
	ctx := testRedisBusContext(t)
	redisURL := testredis.StartRedis(t)
	opts, err := redis.ParseURL(redisURL)
	require.NoError(t, err)

	busA := mustLoadRedisBus(t, ctx, opts)
	defer func() { require.NoError(t, busA.Close()) }()
	busB := mustLoadRedisBus(t, ctx, opts)
	defer func() { require.NoError(t, busB.Close()) }()

	subCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	peerEvents, err := busB.Subscribe(subCtx)
	require.NoError(t, err)

	require.NoError(t, busA.currentClient().Close())
	require.NoError(t, busA.Publish(context.Background(), registryeventbus.Event{
		Event: "created",
		Kind:  "conversation",
		Data:  map[string]any{"conversation": "local-only"},
	}))
	require.Eventually(t, busA.isDegraded, 5*time.Second, 50*time.Millisecond)

	replacement := redis.NewClient(opts)
	require.NoError(t, replacement.Ping(context.Background()).Err())
	busA.setClient(replacement)

	require.NoError(t, busA.Publish(context.Background(), registryeventbus.Event{
		Event: "updated",
		Kind:  "conversation",
		Data:  map[string]any{"conversation": "after-recovery"},
	}))

	invalidate := waitForRedisEvent(t, peerEvents, 10*time.Second, func(event registryeventbus.Event) bool {
		return event.Kind == "stream" && event.Event == "invalidate"
	})
	require.Equal(t, "pubsub recovery", redisEventReason(invalidate))
}

func TestRedisBusPublishesRecoveryInvalidateAfterSubscriptionLoss(t *testing.T) {
	ctx := testRedisBusContext(t)
	redisURL := testredis.StartRedis(t)
	opts, err := redis.ParseURL(redisURL)
	require.NoError(t, err)

	busA := mustLoadRedisBus(t, ctx, opts)
	defer func() { require.NoError(t, busA.Close()) }()
	busB := mustLoadRedisBus(t, ctx, opts)
	defer func() { require.NoError(t, busB.Close()) }()

	subCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	localEvents, err := busB.Subscribe(subCtx)
	require.NoError(t, err)
	peerEvents, err := busA.Subscribe(subCtx)
	require.NoError(t, err)

	require.NoError(t, busB.currentClient().Close())
	localInvalidate := waitForRedisEvent(t, localEvents, 10*time.Second, func(event registryeventbus.Event) bool {
		return event.Kind == "stream" && event.Event == "invalidate"
	})
	require.Equal(t, "pubsub recovery", redisEventReason(localInvalidate))
	require.Eventually(t, busB.isDegraded, 5*time.Second, 50*time.Millisecond)

	replacement := redis.NewClient(opts)
	require.NoError(t, replacement.Ping(context.Background()).Err())
	busB.setClient(replacement)
	require.NoError(t, busB.Publish(context.Background(), registryeventbus.Event{
		Event: "updated",
		Kind:  "conversation",
		Data:  map[string]any{"conversation": "after-subscription-recovery"},
	}))

	peerInvalidate := waitForRedisEvent(t, peerEvents, 10*time.Second, func(event registryeventbus.Event) bool {
		return event.Kind == "stream" && event.Event == "invalidate"
	})
	require.Equal(t, "pubsub recovery", redisEventReason(peerInvalidate))
}

func testRedisBusContext(t *testing.T) context.Context {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.EventBusBatchSize = 1
	cfg.SSESubscriberBufferSize = 8
	return config.WithContext(context.Background(), &cfg)
}

func mustLoadRedisBus(t *testing.T, ctx context.Context, opts *redis.Options) *redisBus {
	t.Helper()
	bus, err := LoadFromOptions(ctx, opts)
	require.NoError(t, err)
	typed, ok := bus.(*redisBus)
	require.True(t, ok)
	return typed
}

func waitForRedisEvent(t *testing.T, ch <-chan registryeventbus.Event, timeout time.Duration, match func(registryeventbus.Event) bool) registryeventbus.Event {
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

func redisEventReason(event registryeventbus.Event) string {
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
