//go:build !nopostgresql

// Package postgres implements a cross-node event bus using PostgreSQL LISTEN/NOTIFY.
// It wraps the local bus for per-node fan-out and uses user-scoped channels for
// cross-node delivery.
package postgres

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
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
	"github.com/jackc/pgx/v5/stdlib"
)

const (
	maxBatchSize       = 128
	maxPayload         = 7500 // pg_notify has an 8KB limit; leave margin
	outboundQueue      = 256
	pgBroadcastChannel = "memory_service_events_broadcast"
	pgAdminChannel     = "memory_service_events_admin"
)

func init() {
	registryeventbus.Register(registryeventbus.Plugin{
		Name:   "postgres",
		Loader: load,
	})
}

func load(ctx context.Context) (registryeventbus.EventBus, error) {
	cfg := config.FromContext(ctx)
	if cfg.DBURL == "" {
		return nil, fmt.Errorf("postgres eventbus requires DBURL to be set")
	}

	db, err := sql.Open("pgx", cfg.DBURL)
	if err != nil {
		return nil, fmt.Errorf("postgres eventbus: failed to open connection: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("postgres eventbus: failed to ping: %w", err)
	}

	bgCtx, cancel := context.WithCancel(context.Background())
	outboundCap := cfg.EventBusOutboundBuffer
	if outboundCap <= 0 {
		outboundCap = outboundQueue
	}
	batchSize := cfg.EventBusBatchSize
	if batchSize <= 0 {
		batchSize = maxBatchSize
	}
	bus := &postgresBus{
		db:         db,
		local:      localbus.New(cfg.SSESubscriberBufferSize),
		outbound:   make(chan registryeventbus.Event, outboundCap),
		cancel:     cancel,
		cfg:        cfg,
		batchSize:  batchSize,
		userRefs:   make(map[string]int),
		refreshSub: make(chan struct{}, 1),
	}

	bus.wg.Add(2)
	go bus.publishLoop(bgCtx)
	go bus.subscribeLoop(bgCtx)

	return bus, nil
}

type postgresBus struct {
	db       *sql.DB
	dbMu     sync.RWMutex
	local    *localbus.Bus
	outbound chan registryeventbus.Event
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	cfg      *config.Config

	healthMu  sync.Mutex
	degraded  bool
	batchSize int

	subMu      sync.Mutex
	userRefs   map[string]int
	globalRefs int
	refreshSub chan struct{}
}

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

// Publish queues an event for cross-node delivery via pg_notify.
func (p *postgresBus) Publish(ctx context.Context, event registryeventbus.Event) error {
	select {
	case p.outbound <- event:
	default:
		log.Warn("PostgreSQL eventbus outbound queue full, dropping event")
		p.markDegraded()
		if security.EventBusDroppedTotal != nil {
			security.EventBusDroppedTotal.Inc()
		}
	}
	return nil
}

// Subscribe delegates to the local bus and tracks user-scoped interest.
func (p *postgresBus) Subscribe(ctx context.Context, userID string) (<-chan registryeventbus.Event, error) {
	ch, err := p.local.Subscribe(ctx, userID)
	if err != nil {
		return nil, err
	}
	p.trackSubscription(userID)
	go func() {
		<-ctx.Done()
		p.untrackSubscription(userID)
	}()
	return ch, nil
}

// Close cancels background goroutines, waits for them to finish, and closes resources.
func (p *postgresBus) Close() error {
	p.cancel()
	p.wg.Wait()
	_ = p.local.Close()
	db := p.currentDB()
	if db == nil {
		return nil
	}
	return db.Close()
}

// publishLoop batches outbound events and sends them via pg_notify.
func (p *postgresBus) publishLoop(ctx context.Context) {
	defer p.wg.Done()
	batchSize := p.batchSize
	if batchSize <= 0 {
		batchSize = p.cfg.EventBusBatchSize
	}
	if batchSize <= 0 {
		batchSize = maxBatchSize
	}
	for {
		var batch []registryeventbus.Event
		select {
		case <-ctx.Done():
			return
		case e, ok := <-p.outbound:
			if !ok {
				return
			}
			batch = append(batch, e)
		}
		for len(batch) < batchSize {
			select {
			case e, ok := <-p.outbound:
				if !ok {
					goto send
				}
				batch = append(batch, e)
			default:
				goto send
			}
		}
	send:
		if err := p.publishBatch(ctx, batch); err != nil {
			log.Warn("PostgreSQL eventbus: publish failed", "err", err)
			p.markDegraded()
			if security.EventBusDroppedTotal != nil {
				security.EventBusDroppedTotal.Inc()
			}
			continue
		}
		if p.clearDegraded() {
			log.Info("PostgreSQL event bus: recovered, sending invalidate")
			if err := p.publishRecoveryInvalidate(ctx); err != nil {
				log.Warn("PostgreSQL eventbus: recovery invalidate failed", "err", err)
				p.markDegraded()
			}
		}
	}
}

func (p *postgresBus) publishBatch(ctx context.Context, batch []registryeventbus.Event) error {
	channelEvents := make(map[string][]registryeventbus.Event)
	for _, event := range batch {
		for _, channel := range postgresChannelsForEvent(event) {
			channelEvents[channel] = append(channelEvents[channel], postgresEventForChannel(event, channel))
		}
	}

	var publishErr error
	for channel, events := range channelEvents {
		var lines []string
		for _, e := range events {
			data, err := json.Marshal(toWire(e))
			if err != nil {
				log.Warn("Failed to marshal event for pg_notify", "err", err)
				continue
			}
			lines = append(lines, string(data))
		}

		var chunk []string
		var chunkSize int
		for _, line := range lines {
			if chunkSize+len(line)+1 > maxPayload && len(chunk) > 0 {
				if err := p.notify(ctx, channel, strings.Join(chunk, "\n")); err != nil {
					publishErr = err
				}
				chunk = chunk[:0]
				chunkSize = 0
			}
			chunk = append(chunk, line)
			chunkSize += len(line) + 1
		}
		if len(chunk) > 0 {
			if err := p.notify(ctx, channel, strings.Join(chunk, "\n")); err != nil {
				publishErr = err
			}
		}
	}
	return publishErr
}

func (p *postgresBus) notify(ctx context.Context, channel, payload string) error {
	db := p.currentDB()
	if db == nil {
		return fmt.Errorf("postgres event bus database is not available")
	}
	_, err := db.ExecContext(ctx, "SELECT pg_notify($1, $2)", channel, payload)
	return err
}

// subscribeLoop maintains LISTEN subscriptions and reconnects on failure or channel-set changes.
func (p *postgresBus) subscribeLoop(ctx context.Context) {
	defer p.wg.Done()
	for ctx.Err() == nil {
		channels := p.subscriptionChannels()
		p.drainRefresh()
		err := p.runSubscription(ctx, channels, func() {
			if p.clearDegraded() {
				log.Info("PostgreSQL event bus: subscription recovered, sending invalidate")
				if err := p.publishRecoveryInvalidate(ctx); err != nil {
					log.Warn("PostgreSQL eventbus: recovery invalidate failed", "err", err)
					p.markDegraded()
				}
			}
		})
		if ctx.Err() != nil {
			return
		}
		if err == errPostgresRefresh {
			continue
		}
		p.markDegraded()
		_ = p.local.Publish(ctx, registryeventbus.Event{
			Event:     "invalidate",
			Kind:      "stream",
			Data:      map[string]string{"reason": "pubsub recovery"},
			Broadcast: true,
		})
		log.Warn("PostgreSQL LISTEN lost, reconnecting", "err", err)
		time.Sleep(time.Second)
	}
}

var errPostgresRefresh = fmt.Errorf("postgres subscription refresh")

func (p *postgresBus) runSubscription(ctx context.Context, channels []string, onReady func()) error {
	db := p.currentDB()
	if db == nil {
		return fmt.Errorf("postgres event bus database is not available")
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Close()

	for _, channel := range channels {
		if _, err := conn.ExecContext(ctx, "LISTEN "+channel); err != nil {
			return fmt.Errorf("LISTEN %s failed: %w", channel, err)
		}
	}
	if onReady != nil {
		onReady()
	}

	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- conn.Raw(func(driverConn any) error {
			pgxConn := driverConn.(*stdlib.Conn).Conn()
			for {
				notification, err := pgxConn.WaitForNotification(subCtx)
				if err != nil {
					return err
				}
				for _, line := range strings.Split(notification.Payload, "\n") {
					line = strings.TrimSpace(line)
					if line == "" {
						continue
					}
					var w wireEvent
					if err := json.Unmarshal([]byte(line), &w); err != nil {
						log.Warn("Failed to unmarshal event from pg_notify", "err", err)
						continue
					}
					_ = p.local.Publish(ctx, fromWire(w))
				}
			}
		})
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-p.refreshSub:
		cancel()
		<-done
		return errPostgresRefresh
	}
}

func postgresChannelsForEvent(event registryeventbus.Event) []string {
	if event.Broadcast {
		return []string{pgBroadcastChannel}
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
		channels = append(channels, postgresUserChannel(userID))
	}
	if !event.Internal && !event.AdminOnly {
		channels = append(channels, pgAdminChannel)
	}
	if len(channels) == 0 {
		channels = append(channels, pgAdminChannel)
	}
	return channels
}

func postgresEventForChannel(event registryeventbus.Event, channel string) registryeventbus.Event {
	if channel == pgAdminChannel {
		copyEvent := event
		copyEvent.AdminOnly = true
		copyEvent.UserIDs = nil
		copyEvent.Broadcast = false
		return copyEvent
	}
	return event
}

func postgresUserChannel(userID string) string {
	sum := sha1.Sum([]byte(userID))
	return "memory_service_events_user_" + hex.EncodeToString(sum[:])
}

func (p *postgresBus) subscriptionChannels() []string {
	p.subMu.Lock()
	defer p.subMu.Unlock()

	channels := []string{pgBroadcastChannel}
	if p.globalRefs > 0 {
		channels = append(channels, pgAdminChannel)
	}
	for userID := range p.userRefs {
		channels = append(channels, postgresUserChannel(userID))
	}
	sort.Strings(channels)
	return channels
}

func (p *postgresBus) trackSubscription(userID string) {
	p.subMu.Lock()
	if userID == "" {
		p.globalRefs++
	} else {
		p.userRefs[userID]++
	}
	p.subMu.Unlock()
	p.requestRefresh()
}

func (p *postgresBus) untrackSubscription(userID string) {
	p.subMu.Lock()
	if userID == "" {
		if p.globalRefs > 0 {
			p.globalRefs--
		}
	} else if count := p.userRefs[userID]; count > 1 {
		p.userRefs[userID] = count - 1
	} else {
		delete(p.userRefs, userID)
	}
	p.subMu.Unlock()
	p.requestRefresh()
}

func (p *postgresBus) requestRefresh() {
	select {
	case p.refreshSub <- struct{}{}:
	default:
	}
}

func (p *postgresBus) drainRefresh() {
	for {
		select {
		case <-p.refreshSub:
		default:
			return
		}
	}
}

func (p *postgresBus) currentDB() *sql.DB {
	p.dbMu.RLock()
	defer p.dbMu.RUnlock()
	return p.db
}

func (p *postgresBus) setDB(db *sql.DB) {
	p.dbMu.Lock()
	defer p.dbMu.Unlock()
	p.db = db
}

func (p *postgresBus) markDegraded() {
	p.healthMu.Lock()
	defer p.healthMu.Unlock()
	p.degraded = true
}

func (p *postgresBus) clearDegraded() bool {
	p.healthMu.Lock()
	defer p.healthMu.Unlock()
	if !p.degraded {
		return false
	}
	p.degraded = false
	return true
}

func (p *postgresBus) isDegraded() bool {
	p.healthMu.Lock()
	defer p.healthMu.Unlock()
	return p.degraded
}

func (p *postgresBus) publishRecoveryInvalidate(ctx context.Context) error {
	return p.publishBatch(ctx, []registryeventbus.Event{{
		Event:     "invalidate",
		Kind:      "stream",
		Data:      map[string]string{"reason": "pubsub recovery"},
		Broadcast: true,
	}})
}
