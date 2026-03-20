//go:build !nopostgresql

// Package postgres implements a cross-node event bus using PostgreSQL LISTEN/NOTIFY.
// It wraps the local bus for per-node fan-out and uses pg_notify for cross-node delivery.
package postgres

import (
	"context"
	"database/sql"
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
	"github.com/jackc/pgx/v5/stdlib"
)

const (
	channel       = "memory_service_events"
	maxBatchSize  = 128
	maxPayload    = 7500 // pg_notify has an 8KB limit; leave margin
	outboundQueue = 256
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
		db:        db,
		local:     localbus.New(cfg.SSESubscriberBufferSize),
		outbound:  make(chan registryeventbus.Event, outboundCap),
		cancel:    cancel,
		cfg:       cfg,
		batchSize: batchSize,
	}

	bus.wg.Add(2)
	go bus.publishLoop(bgCtx)
	go bus.subscribeLoop(bgCtx)

	return bus, nil
}

type postgresBus struct {
	db        *sql.DB
	dbMu      sync.RWMutex
	local     *localbus.Bus
	outbound  chan registryeventbus.Event
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	cfg       *config.Config
	healthMu  sync.Mutex
	degraded  bool
	batchSize int
}

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

// Publish sends an event to local subscribers and queues it for cross-node delivery.
func (p *postgresBus) Publish(ctx context.Context, event registryeventbus.Event) error {
	// Deliver locally first.
	if err := p.local.Publish(ctx, event); err != nil {
		return err
	}
	// Queue for cross-node fan-out (non-blocking).
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

// Subscribe delegates to the local bus.
func (p *postgresBus) Subscribe(ctx context.Context) (<-chan registryeventbus.Event, error) {
	return p.local.Subscribe(ctx)
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
		// Block for the first event.
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
		// Non-blocking drain up to batch size.
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
	var lines []string
	for _, e := range batch {
		data, err := json.Marshal(toWire(e))
		if err != nil {
			log.Warn("Failed to marshal event for pg_notify", "err", err)
			continue
		}
		lines = append(lines, string(data))
	}

	// Split into chunks that fit within the payload limit.
	var chunk []string
	var chunkSize int
	var publishErr error
	for _, line := range lines {
		if chunkSize+len(line)+1 > maxPayload && len(chunk) > 0 {
			if err := p.notify(ctx, strings.Join(chunk, "\n")); err != nil {
				publishErr = err
			}
			chunk = chunk[:0]
			chunkSize = 0
		}
		chunk = append(chunk, line)
		chunkSize += len(line) + 1
	}
	if len(chunk) > 0 {
		if err := p.notify(ctx, strings.Join(chunk, "\n")); err != nil {
			publishErr = err
		}
	}
	return publishErr
}

func (p *postgresBus) notify(ctx context.Context, payload string) error {
	db := p.currentDB()
	if db == nil {
		return fmt.Errorf("postgres event bus database is not available")
	}
	_, err := db.ExecContext(ctx, "SELECT pg_notify($1, $2)", channel, payload)
	if err != nil {
		return err
	}
	return nil
}

// subscribeLoop maintains a LISTEN subscription and reconnects on failure.
func (p *postgresBus) subscribeLoop(ctx context.Context) {
	defer p.wg.Done()
	for ctx.Err() == nil {
		err := p.runSubscription(ctx, func() {
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
		// Subscription lost - publish invalidate to local bus so clients resync.
		p.markDegraded()
		_ = p.local.Publish(ctx, registryeventbus.Event{
			Event: "invalidate",
			Kind:  "stream",
			Data:  map[string]string{"reason": "pubsub recovery"},
		})
		log.Warn("PostgreSQL LISTEN lost, reconnecting", "err", err)
		time.Sleep(time.Second)
	}
}

func (p *postgresBus) runSubscription(ctx context.Context, onReady func()) error {
	db := p.currentDB()
	if db == nil {
		return fmt.Errorf("postgres event bus database is not available")
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "LISTEN "+channel); err != nil {
		return fmt.Errorf("LISTEN failed: %w", err)
	}
	if onReady != nil {
		onReady()
	}

	return conn.Raw(func(driverConn any) error {
		pgxConn := driverConn.(*stdlib.Conn).Conn()
		for {
			notification, err := pgxConn.WaitForNotification(ctx)
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
		Event: "invalidate",
		Kind:  "stream",
		Data:  map[string]string{"reason": "pubsub recovery"},
	}})
}
