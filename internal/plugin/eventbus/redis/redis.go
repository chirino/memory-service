//go:build !noredis || !noinfinispan

// Package redis implements a cross-node event bus backed by Redis Pub/Sub.
// It wraps a local bus for in-process fan-out and publishes batched events
// to Redis for cross-node delivery. The "infinispan" event bus variant uses
// the same implementation since Infinispan speaks the RESP protocol.
package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	localbus "github.com/chirino/memory-service/internal/plugin/eventbus/local"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	"github.com/chirino/memory-service/internal/security"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

func init() {
	registryeventbus.Register(registryeventbus.Plugin{
		Name:   "redis",
		Loader: loadRedis,
	})
	registryeventbus.Register(registryeventbus.Plugin{
		Name:   "infinispan",
		Loader: loadInfinispan,
	})
}

func loadRedis(ctx context.Context) (registryeventbus.EventBus, error) {
	cfg := config.FromContext(ctx)
	if cfg == nil || cfg.RedisURL == "" {
		return nil, fmt.Errorf("redis event bus requires RedisURL to be set")
	}

	opts, err := goredis.ParseURL(cfg.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("redis event bus: invalid RedisURL: %w", err)
	}
	return LoadFromOptions(ctx, opts)
}

func loadInfinispan(ctx context.Context) (registryeventbus.EventBus, error) {
	cfg := config.FromContext(ctx)
	if cfg == nil || cfg.InfinispanHost == "" {
		return nil, fmt.Errorf("infinispan event bus requires InfinispanHost to be set")
	}
	opts := &goredis.Options{
		Addr:     cfg.InfinispanHost,
		Username: cfg.InfinispanUsername,
		Password: cfg.InfinispanPassword,
		Protocol: 2, // Infinispan RESP endpoint requires RESP2
	}
	return LoadFromOptions(ctx, opts)
}

// LoadFromOptions creates a Redis/RESP-backed event bus from go-redis Options.
func LoadFromOptions(ctx context.Context, opts *goredis.Options) (registryeventbus.EventBus, error) {
	cfg := config.FromContext(ctx)

	client := goredis.NewClient(opts)

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis event bus: ping failed: %w", err)
	}

	bufSize := cfg.SSESubscriberBufferSize
	if bufSize <= 0 {
		bufSize = 64
	}

	outboundCap := cfg.EventBusOutboundBuffer
	if outboundCap <= 0 {
		outboundCap = 200
	}

	loopCtx, cancel := context.WithCancel(context.Background())

	r := &redisBus{
		client:   client,
		local:    localbus.New(bufSize),
		outbound: make(chan registryeventbus.Event, outboundCap),
		cancel:   cancel,
		cfg:      cfg,
	}

	r.wg.Add(2)
	go r.publishLoop(loopCtx)
	go r.subscribeLoop(loopCtx)

	return r, nil
}

type redisBus struct {
	client   *goredis.Client
	clientMu sync.RWMutex
	local    *localbus.Bus
	outbound chan registryeventbus.Event
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	cfg      *config.Config
	healthMu sync.Mutex
	degraded bool
}

// wireEvent is used for Redis serialization, including ConversationGroupID
// which is excluded from the standard Event JSON tags.
type wireEvent struct {
	Event               string    `json:"event"`
	Kind                string    `json:"kind"`
	Data                any       `json:"data"`
	ConversationGroupID uuid.UUID `json:"conversationGroupId,omitempty"`
	Internal            bool      `json:"internal,omitempty"`
}

func toWire(e registryeventbus.Event) wireEvent {
	return wireEvent{
		Event:               e.Event,
		Kind:                e.Kind,
		Data:                e.Data,
		ConversationGroupID: e.ConversationGroupID,
		Internal:            e.Internal,
	}
}

func fromWire(w wireEvent) registryeventbus.Event {
	return registryeventbus.Event{
		Event:               w.Event,
		Kind:                w.Kind,
		Data:                w.Data,
		ConversationGroupID: w.ConversationGroupID,
		Internal:            w.Internal,
	}
}

// Publish sends an event to all local subscribers and queues it for Redis publication.
func (r *redisBus) Publish(ctx context.Context, event registryeventbus.Event) error {
	// Always fan out locally first.
	_ = r.local.Publish(ctx, event)

	// Queue for cross-node publish (non-blocking).
	select {
	case r.outbound <- event:
	default:
		log.Warn("Redis event bus: outbound channel full, dropping event", "event", event.Event, "kind", event.Kind)
		r.markDegraded()
		if security.EventBusDroppedTotal != nil {
			security.EventBusDroppedTotal.Inc()
		}
	}
	return nil
}

// Subscribe returns a channel that receives events from the local bus.
func (r *redisBus) Subscribe(ctx context.Context) (<-chan registryeventbus.Event, error) {
	return r.local.Subscribe(ctx)
}

// Close shuts down background goroutines, the local bus, and the Redis client.
func (r *redisBus) Close() error {
	r.cancel()
	r.wg.Wait()
	_ = r.local.Close()
	client := r.currentClient()
	if client == nil {
		return nil
	}
	return client.Close()
}

const redisChannel = "memory-service:events"

// publishLoop drains the outbound channel with batching.
func (r *redisBus) publishLoop(ctx context.Context) {
	defer r.wg.Done()

	batchSize := r.cfg.EventBusBatchSize
	if batchSize <= 0 {
		batchSize = 100
	}
	batch := make([]registryeventbus.Event, 0, batchSize)
	hadError := false

	for {
		// Block for first event.
		select {
		case <-ctx.Done():
			return
		case event, ok := <-r.outbound:
			if !ok {
				return
			}
			batch = append(batch, event)
		}

		// Non-blocking drain up to batch size.
		for len(batch) < batchSize {
			select {
			case event, ok := <-r.outbound:
				if !ok {
					goto publish
				}
				batch = append(batch, event)
			default:
				goto publish
			}
		}

	publish:
		err := r.publishBatch(ctx, batch)
		if err != nil {
			if !hadError {
				log.Warn("Redis event bus: publish failed", "err", err)
			}
			hadError = true
			r.markDegraded()
			if security.EventBusDroppedTotal != nil {
				security.EventBusDroppedTotal.Inc()
			}
		} else {
			if hadError {
				log.Info("Redis event bus: publish recovered")
				hadError = false
			}
			if r.clearDegraded() {
				if err := r.publishRecoveryInvalidate(ctx); err != nil {
					log.Warn("Redis event bus: recovery invalidate failed", "err", err)
					r.markDegraded()
				}
			}
		}
		batch = batch[:0]
	}
}

// publishBatch encodes and publishes a batch of events to Redis.
func (r *redisBus) publishBatch(ctx context.Context, events []registryeventbus.Event) error {
	if len(events) == 0 {
		return nil
	}
	payload := r.encodeEvents(events)
	client := r.currentClient()
	if client == nil {
		return fmt.Errorf("redis event bus client is not available")
	}
	return client.Publish(ctx, redisChannel, payload).Err()
}

// encodeEvents encodes events as newline-delimited JSON.
func (r *redisBus) encodeEvents(events []registryeventbus.Event) string {
	var sb strings.Builder
	for i, e := range events {
		data, err := json.Marshal(toWire(e))
		if err != nil {
			log.Warn("Redis event bus: failed to marshal event", "err", err)
			continue
		}
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.Write(data)
	}
	return sb.String()
}

// subscribeLoop reconnects to Redis Pub/Sub on failure.
func (r *redisBus) subscribeLoop(ctx context.Context) {
	defer r.wg.Done()

	for ctx.Err() == nil {
		err := r.runSubscription(ctx, func() {
			if r.clearDegraded() {
				log.Info("Redis event bus: subscription recovered, sending invalidate")
				if err := r.publishRecoveryInvalidate(ctx); err != nil {
					log.Warn("Redis event bus: recovery invalidate failed", "err", err)
					r.markDegraded()
				}
			}
		})
		if ctx.Err() != nil {
			return
		}
		// Subscription lost — notify local subscribers to invalidate.
		r.markDegraded()
		_ = r.local.Publish(ctx, registryeventbus.Event{
			Event: "invalidate",
			Kind:  "stream",
			Data:  map[string]string{"reason": "pubsub recovery"},
		})
		log.Warn("Redis subscription lost, reconnecting", "err", err)
		time.Sleep(time.Second)
	}
}

// runSubscription subscribes to the Redis channel and feeds events into the local bus.
func (r *redisBus) runSubscription(ctx context.Context, onReady func()) error {
	client := r.currentClient()
	if client == nil {
		return fmt.Errorf("redis event bus client is not available")
	}
	pubsub := client.Subscribe(ctx, redisChannel)
	defer pubsub.Close()
	if _, err := pubsub.Receive(ctx); err != nil {
		return err
	}
	if onReady != nil {
		onReady()
	}

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-ch:
			if !ok {
				return fmt.Errorf("redis subscription channel closed")
			}
			for _, line := range strings.Split(msg.Payload, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				var w wireEvent
				if err := json.Unmarshal([]byte(line), &w); err != nil {
					log.Warn("Failed to unmarshal event from Redis", "err", err)
					continue
				}
				_ = r.local.Publish(ctx, fromWire(w))
			}
		}
	}
}

func (r *redisBus) currentClient() *goredis.Client {
	r.clientMu.RLock()
	defer r.clientMu.RUnlock()
	return r.client
}

func (r *redisBus) setClient(client *goredis.Client) {
	r.clientMu.Lock()
	defer r.clientMu.Unlock()
	r.client = client
}

func (r *redisBus) markDegraded() {
	r.healthMu.Lock()
	defer r.healthMu.Unlock()
	r.degraded = true
}

func (r *redisBus) clearDegraded() bool {
	r.healthMu.Lock()
	defer r.healthMu.Unlock()
	if !r.degraded {
		return false
	}
	r.degraded = false
	return true
}

func (r *redisBus) isDegraded() bool {
	r.healthMu.Lock()
	defer r.healthMu.Unlock()
	return r.degraded
}

func (r *redisBus) publishRecoveryInvalidate(ctx context.Context) error {
	return r.publishBatch(ctx, []registryeventbus.Event{{
		Event: "invalidate",
		Kind:  "stream",
		Data:  map[string]string{"reason": "pubsub recovery"},
	}})
}
