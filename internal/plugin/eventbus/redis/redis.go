//go:build !noredis || !noinfinispan

// Package redis implements a cross-node event bus backed by Redis Pub/Sub.
// It wraps a local bus for in-process fan-out and publishes user-targeted
// events to Redis channels for cross-node delivery. The "infinispan" event bus
// variant uses the same implementation since Infinispan speaks the RESP protocol.
package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
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

const (
	redisBroadcastChannel = "memory-service:events:broadcast"
	redisAdminChannel     = "memory-service:events:admin"
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
		client:     client,
		local:      localbus.New(bufSize),
		outbound:   make(chan registryeventbus.Event, outboundCap),
		cancel:     cancel,
		cfg:        cfg,
		userRefs:   make(map[string]int),
		refreshSub: make(chan struct{}, 1),
		subReady:   make(chan struct{}),
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

	subMu      sync.Mutex
	userRefs   map[string]int
	globalRefs int
	wantVer    int64
	activeVer  int64
	refreshSub chan struct{}
	breakSub   context.CancelFunc
	subReady   chan struct{}
}

// wireEvent is used for Redis serialization, including routing metadata that is
// excluded from the public Event JSON tags.
type wireEvent struct {
	Event               string    `json:"event"`
	Kind                string    `json:"kind"`
	Data                any       `json:"data"`
	ConversationGroupID uuid.UUID `json:"conversationGroupId,omitempty"`
	UserIDs             []string  `json:"userIds,omitempty"`
	Broadcast           bool      `json:"broadcast,omitempty"`
	AdminOnly           bool      `json:"adminOnly,omitempty"`
	Internal            bool      `json:"internal,omitempty"`
}

func toWire(e registryeventbus.Event) wireEvent {
	return wireEvent{
		Event:               e.Event,
		Kind:                e.Kind,
		Data:                e.Data,
		ConversationGroupID: e.ConversationGroupID,
		UserIDs:             e.UserIDs,
		Broadcast:           e.Broadcast,
		AdminOnly:           e.AdminOnly,
		Internal:            e.Internal,
	}
}

func fromWire(w wireEvent) registryeventbus.Event {
	return registryeventbus.Event{
		Event:               w.Event,
		Kind:                w.Kind,
		Data:                w.Data,
		ConversationGroupID: w.ConversationGroupID,
		UserIDs:             w.UserIDs,
		Broadcast:           w.Broadcast,
		AdminOnly:           w.AdminOnly,
		Internal:            w.Internal,
	}
}

// Publish queues an event for Redis publication.
func (r *redisBus) Publish(ctx context.Context, event registryeventbus.Event) error {
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

// Subscribe returns a channel that receives routed events.
// Pass an empty userID for admin/all-events traffic only.
func (r *redisBus) Subscribe(ctx context.Context, userID string) (<-chan registryeventbus.Event, error) {
	ch, err := r.local.Subscribe(ctx, userID)
	if err != nil {
		return nil, err
	}
	version := r.trackSubscription(userID)
	if err := r.waitForSubscription(ctx, version); err != nil {
		return nil, err
	}
	go func() {
		<-ctx.Done()
		r.untrackSubscription(userID)
	}()
	return ch, nil
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
		select {
		case <-ctx.Done():
			return
		case event, ok := <-r.outbound:
			if !ok {
				return
			}
			batch = append(batch, event)
		}

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

func (r *redisBus) publishBatch(ctx context.Context, events []registryeventbus.Event) error {
	channelEvents := make(map[string][]registryeventbus.Event)
	for _, event := range events {
		for _, channel := range redisChannelsForEvent(event) {
			channelEvents[channel] = append(channelEvents[channel], eventForChannel(event, channel))
		}
	}

	var publishErr error
	for channel, bucket := range channelEvents {
		if len(bucket) == 0 {
			continue
		}
		payload := r.encodeEvents(bucket)
		client := r.currentClient()
		if client == nil {
			publishErr = fmt.Errorf("redis event bus client is not available")
			continue
		}
		if err := client.Publish(ctx, channel, payload).Err(); err != nil {
			publishErr = err
		}
	}
	return publishErr
}

func (r *redisBus) encodeEvents(events []registryeventbus.Event) string {
	var sb strings.Builder
	written := 0
	for _, e := range events {
		data, err := json.Marshal(toWire(e))
		if err != nil {
			log.Warn("Redis event bus: failed to marshal event", "err", err)
			continue
		}
		if written > 0 {
			sb.WriteByte('\n')
		}
		sb.Write(data)
		written++
	}
	return sb.String()
}

// subscribeLoop reconnects to Redis Pub/Sub on failure or channel-set changes.
func (r *redisBus) subscribeLoop(ctx context.Context) {
	defer r.wg.Done()

	for ctx.Err() == nil {
		channels := r.subscriptionChannels()
		r.drainRefresh()
		version := r.subscriptionVersion()
		err := r.runSubscription(ctx, channels, func() {
			r.markSubscriptionActive(version)
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
		if err == errRedisRefresh {
			continue
		}
		r.markDegraded()
		_ = r.local.Publish(ctx, registryeventbus.Event{
			Event:     "invalidate",
			Kind:      "stream",
			Data:      map[string]string{"reason": "pubsub recovery"},
			Broadcast: true,
		})
		log.Warn("Redis subscription lost, reconnecting", "err", err)
		time.Sleep(time.Second)
	}
}

var errRedisRefresh = fmt.Errorf("redis subscription refresh")

func (r *redisBus) runSubscription(ctx context.Context, channels []string, onReady func()) error {
	client := r.currentClient()
	if client == nil {
		return fmt.Errorf("redis event bus client is not available")
	}
	pubsub := client.Subscribe(ctx, channels...)
	defer pubsub.Close()

	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()
	if _, err := pubsub.Receive(subCtx); err != nil {
		return err
	}

	r.clientMu.Lock()
	r.breakSub = subCancel
	select {
	case <-r.subReady:
		r.subReady = make(chan struct{})
	default:
	}
	ready := r.subReady
	r.clientMu.Unlock()
	defer func() {
		r.clientMu.Lock()
		r.breakSub = nil
		r.clientMu.Unlock()
	}()
	close(ready)

	if onReady != nil {
		onReady()
	}

	ch := pubsub.Channel()
	for {
		select {
		case <-subCtx.Done():
			return subCtx.Err()
		case <-r.refreshSub:
			return errRedisRefresh
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

func redisChannelsForEvent(event registryeventbus.Event) []string {
	if event.Broadcast {
		return []string{redisBroadcastChannel}
	}

	channels := make([]string, 0, len(event.UserIDs)+1)
	seen := make(map[string]struct{}, len(event.UserIDs))
	for _, userID := range event.UserIDs {
		if userID == "" {
			continue
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		channels = append(channels, redisUserChannel(userID))
	}
	if !event.Internal && !event.AdminOnly {
		channels = append(channels, redisAdminChannel)
	}
	if len(channels) == 0 {
		channels = append(channels, redisAdminChannel)
	}
	return channels
}

func eventForChannel(event registryeventbus.Event, channel string) registryeventbus.Event {
	if channel == redisAdminChannel {
		copyEvent := event
		copyEvent.AdminOnly = true
		copyEvent.UserIDs = nil
		copyEvent.Broadcast = false
		return copyEvent
	}
	return event
}

func redisUserChannel(userID string) string {
	return "memory-service:events:user:" + userID
}

func (r *redisBus) subscriptionChannels() []string {
	r.subMu.Lock()
	defer r.subMu.Unlock()

	channels := []string{redisBroadcastChannel}
	if r.globalRefs > 0 {
		channels = append(channels, redisAdminChannel)
	}
	for userID := range r.userRefs {
		channels = append(channels, redisUserChannel(userID))
	}
	sort.Strings(channels)
	return channels
}

func (r *redisBus) trackSubscription(userID string) int64 {
	r.subMu.Lock()
	if userID == "" {
		r.globalRefs++
	} else {
		r.userRefs[userID]++
	}
	r.wantVer++
	version := r.wantVer
	r.subMu.Unlock()
	r.requestRefresh()
	return version
}

func (r *redisBus) untrackSubscription(userID string) {
	r.subMu.Lock()
	if userID == "" {
		if r.globalRefs > 0 {
			r.globalRefs--
		}
	} else if count := r.userRefs[userID]; count > 1 {
		r.userRefs[userID] = count - 1
	} else {
		delete(r.userRefs, userID)
	}
	r.wantVer++
	r.subMu.Unlock()
	r.requestRefresh()
}

func (r *redisBus) requestRefresh() {
	select {
	case r.refreshSub <- struct{}{}:
	default:
	}
}

func (r *redisBus) drainRefresh() {
	for {
		select {
		case <-r.refreshSub:
		default:
			return
		}
	}
}

func (r *redisBus) subscriptionVersion() int64 {
	r.subMu.Lock()
	defer r.subMu.Unlock()
	return r.wantVer
}

func (r *redisBus) markSubscriptionActive(version int64) {
	r.subMu.Lock()
	defer r.subMu.Unlock()
	if version > r.activeVer {
		r.activeVer = version
	}
}

func (r *redisBus) waitForSubscription(ctx context.Context, version int64) error {
	for {
		r.subMu.Lock()
		active := r.activeVer
		r.subMu.Unlock()
		if active >= version {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
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

// breakSubscription waits for the subscription to be active, then cancels its
// context, causing subscribeLoop to detect subscription loss. Used by tests.
func (r *redisBus) breakSubscription() {
	r.clientMu.RLock()
	ready := r.subReady
	r.clientMu.RUnlock()
	<-ready

	r.clientMu.RLock()
	cancel := r.breakSub
	r.clientMu.RUnlock()
	if cancel != nil {
		cancel()
	}
}

func (r *redisBus) publishRecoveryInvalidate(ctx context.Context) error {
	return r.publishBatch(ctx, []registryeventbus.Event{{
		Event:     "invalidate",
		Kind:      "stream",
		Data:      map[string]string{"reason": "pubsub recovery"},
		Broadcast: true,
	}})
}
