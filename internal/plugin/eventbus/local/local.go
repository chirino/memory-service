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
	subs       map[*subscriber]struct{}
	closed     bool
	bufferSize int
}

type subscriber struct {
	ch  chan registryeventbus.Event
	ctx context.Context
}

// New creates a local event bus with the given per-subscriber buffer size.
func New(bufferSize int) *Bus {
	if bufferSize <= 0 {
		bufferSize = defaultBufferSize
	}
	return &Bus{
		subs:       make(map[*subscriber]struct{}),
		bufferSize: bufferSize,
	}
}

// Publish fans out an event to all local subscribers.
// If a subscriber's buffer is full, its channel is closed (slow-consumer eviction).
//
// Sends are performed under RLock so multiple publishers can fan out concurrently
// without blocking each other. The RLock also prevents the Subscribe cleanup
// goroutine from closing a channel while a send is in progress.
//
// Eviction (which mutates the subs map) is deferred: the RLock is released,
// a write Lock is acquired, and each evicted subscriber is removed only if it
// is still present in the map (guarding against double-close by the cleanup
// goroutine).
func (b *Bus) Publish(_ context.Context, event registryeventbus.Event) error {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return nil
	}

	if security.EventBusPublishedTotal != nil {
		security.EventBusPublishedTotal.Inc()
	}

	var evicted []*subscriber
	for s := range b.subs {
		if s.ctx.Err() != nil {
			evicted = append(evicted, s)
			continue
		}
		select {
		case s.ch <- event:
			// delivered
		default:
			// Buffer full — mark for eviction.
			evicted = append(evicted, s)
		}
	}
	b.mu.RUnlock()

	if len(evicted) > 0 {
		b.mu.Lock()
		for _, s := range evicted {
			if _, ok := b.subs[s]; ok {
				delete(b.subs, s)
				close(s.ch)
				if s.ctx.Err() == nil {
					// Only count as dropped/evicted if it wasn't a context cancellation.
					if security.EventBusDroppedTotal != nil {
						security.EventBusDroppedTotal.Inc()
					}
					if security.EventBusSubscriberEvictionsTotal != nil {
						security.EventBusSubscriberEvictionsTotal.Inc()
					}
				}
			}
		}
		b.mu.Unlock()
	}
	return nil
}

// Subscribe returns a channel that receives events. The channel is closed
// when the context is cancelled or when the subscriber is evicted for being slow.
func (b *Bus) Subscribe(ctx context.Context) (<-chan registryeventbus.Event, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		ch := make(chan registryeventbus.Event)
		close(ch)
		return ch, nil
	}
	s := &subscriber{
		ch:  make(chan registryeventbus.Event, b.bufferSize),
		ctx: ctx,
	}
	b.subs[s] = struct{}{}

	// Clean up when context is cancelled.
	go func() {
		<-ctx.Done()
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, ok := b.subs[s]; ok {
			delete(b.subs, s)
			close(s.ch)
		}
	}()

	return s.ch, nil
}

// Close shuts down the bus and closes all subscriber channels.
func (b *Bus) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	for s := range b.subs {
		close(s.ch)
		delete(b.subs, s)
	}
	return nil
}
