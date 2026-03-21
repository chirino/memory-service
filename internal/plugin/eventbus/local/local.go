// Package local implements an in-process event bus using bounded channels.
// It is the default event bus and also serves as the per-node fan-out layer
// for cross-node implementations (Redis, PostgreSQL).
package local

import (
	"context"
	"sync"

	"github.com/chirino/memory-service/internal/config"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	"github.com/chirino/memory-service/internal/security"
)

const defaultBufferSize = 64

func init() {
	registryeventbus.Register(registryeventbus.Plugin{
		Name:   "local",
		Loader: load,
	})
}

func load(ctx context.Context) (registryeventbus.EventBus, error) {
	cfg := config.FromContext(ctx)
	bufSize := defaultBufferSize
	if cfg != nil && cfg.SSESubscriberBufferSize > 0 {
		bufSize = cfg.SSESubscriberBufferSize
	}
	return New(bufSize), nil
}

// Bus is an in-process event bus with bounded subscriber channels.
type Bus struct {
	mu         sync.RWMutex
	globalSubs map[*subscriber]struct{}
	userSubs   map[string]map[*subscriber]struct{}
	closed     bool
	bufferSize int
}

type subscriber struct {
	ch     chan registryeventbus.Event
	ctx    context.Context
	userID string
}

// New creates a local event bus with the given per-subscriber buffer size.
func New(bufferSize int) *Bus {
	if bufferSize <= 0 {
		bufferSize = defaultBufferSize
	}
	return &Bus{
		globalSubs: make(map[*subscriber]struct{}),
		userSubs:   make(map[string]map[*subscriber]struct{}),
		bufferSize: bufferSize,
	}
}

// Publish fans out an event to matching local subscribers.
// If a subscriber's buffer is full, its channel is closed (slow-consumer eviction).
func (b *Bus) Publish(_ context.Context, event registryeventbus.Event) error {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return nil
	}

	if security.EventBusPublishedTotal != nil {
		security.EventBusPublishedTotal.Inc()
	}

	recipients := make(map[*subscriber]struct{})
	for s := range b.globalSubs {
		recipients[s] = struct{}{}
	}
	switch {
	case event.Broadcast:
		for _, subs := range b.userSubs {
			for s := range subs {
				recipients[s] = struct{}{}
			}
		}
	case event.AdminOnly:
		// already handled by globalSubs
	default:
		for _, userID := range event.UserIDs {
			for s := range b.userSubs[userID] {
				recipients[s] = struct{}{}
			}
		}
	}

	var evicted []*subscriber
	for s := range recipients {
		if s.ctx.Err() != nil {
			evicted = append(evicted, s)
			continue
		}
		select {
		case s.ch <- event:
		default:
			evicted = append(evicted, s)
		}
	}
	b.mu.RUnlock()

	if len(evicted) > 0 {
		b.mu.Lock()
		for _, s := range evicted {
			if b.removeLocked(s) && s.ctx.Err() == nil {
				if security.EventBusDroppedTotal != nil {
					security.EventBusDroppedTotal.Inc()
				}
				if security.EventBusSubscriberEvictionsTotal != nil {
					security.EventBusSubscriberEvictionsTotal.Inc()
				}
			}
		}
		b.mu.Unlock()
	}
	return nil
}

// Subscribe returns a channel that receives matching events.
// Pass an empty userID to receive admin/all-events traffic only.
func (b *Bus) Subscribe(ctx context.Context, userID string) (<-chan registryeventbus.Event, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		ch := make(chan registryeventbus.Event)
		close(ch)
		return ch, nil
	}
	s := &subscriber{
		ch:     make(chan registryeventbus.Event, b.bufferSize),
		ctx:    ctx,
		userID: userID,
	}
	if userID == "" {
		b.globalSubs[s] = struct{}{}
	} else {
		if b.userSubs[userID] == nil {
			b.userSubs[userID] = make(map[*subscriber]struct{})
		}
		b.userSubs[userID][s] = struct{}{}
	}

	go func() {
		<-ctx.Done()
		b.mu.Lock()
		defer b.mu.Unlock()
		b.removeLocked(s)
	}()

	return s.ch, nil
}

func (b *Bus) removeLocked(s *subscriber) bool {
	if s.userID == "" {
		if _, ok := b.globalSubs[s]; !ok {
			return false
		}
		delete(b.globalSubs, s)
		close(s.ch)
		return true
	}

	subs := b.userSubs[s.userID]
	if _, ok := subs[s]; !ok {
		return false
	}
	delete(subs, s)
	if len(subs) == 0 {
		delete(b.userSubs, s.userID)
	}
	close(s.ch)
	return true
}

// Close shuts down the bus and closes all subscriber channels.
func (b *Bus) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	for s := range b.globalSubs {
		close(s.ch)
		delete(b.globalSubs, s)
	}
	for userID, subs := range b.userSubs {
		for s := range subs {
			close(s.ch)
			delete(subs, s)
		}
		delete(b.userSubs, userID)
	}
	return nil
}
