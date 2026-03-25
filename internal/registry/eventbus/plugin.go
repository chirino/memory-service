// Package eventbus defines the EventBus plugin interface for real-time event fan-out.
package eventbus

import (
	"context"
	"fmt"
	"sync"

	"github.com/chirino/memory-service/internal/config"
	"github.com/google/uuid"
	"github.com/urfave/cli/v3"
)

// Event represents a notification published through the event bus.
// The JSON envelope sent to SSE clients contains Event, Kind, and Data.
// ConversationGroupID, UserIDs, Broadcast, and AdminOnly are routing metadata
// and are not serialized to public SSE clients.
type Event struct {
	Event               string    `json:"event"`            // action: created, updated, deleted, phase, evicted, invalidate, shutdown
	Kind                string    `json:"kind"`             // resource type: conversation, entry, response, membership, stream
	Data                any       `json:"data"`             // kind-specific payload
	OutboxCursor        string    `json:"cursor,omitempty"` // durable replay cursor when available
	ConversationGroupID uuid.UUID `json:"-"`                // used for access control filtering, not serialized
	UserIDs             []string  `json:"-"`                // explicit user delivery targets
	Broadcast           bool      `json:"-"`                // deliver to all user/admin subscribers
	AdminOnly           bool      `json:"-"`                // deliver only to admin/all subscribers
	Internal            bool      `json:"-"`                // internal control events (e.g. resync.required), never forwarded to clients
}

// EventBus is the interface for publishing and subscribing to events.
type EventBus interface {
	// Publish sends an event to all subscribers across all nodes.
	Publish(ctx context.Context, event Event) error

	// Subscribe returns a channel that receives events for the given user.
	// Pass an empty userID to subscribe to admin/all-events traffic only.
	// The channel is closed when the subscription is evicted (slow consumer)
	// or when the context is cancelled.
	Subscribe(ctx context.Context, userID string) (<-chan Event, error)

	// Close shuts down the event bus and releases resources.
	Close() error
}

// Loader creates an EventBus from the current context/config.
type Loader func(ctx context.Context) (EventBus, error)

// Plugin describes an event bus implementation that can be registered.
type Plugin struct {
	Name   string
	Loader Loader
	Flags  func(cfg *config.Config) []cli.Flag
	Apply  func(cfg *config.Config, cmd *cli.Command)
}

var (
	mu      sync.Mutex
	plugins []Plugin
)

// Register adds an event bus plugin (called from init() in implementations).
func Register(p Plugin) {
	mu.Lock()
	defer mu.Unlock()
	plugins = append(plugins, p)
}

// Names returns all registered plugin names.
func Names() []string {
	mu.Lock()
	defer mu.Unlock()
	names := make([]string, len(plugins))
	for i, p := range plugins {
		names[i] = p.Name
	}
	return names
}

// Select returns the loader for a named plugin.
func Select(name string) (Loader, error) {
	mu.Lock()
	defer mu.Unlock()
	for _, p := range plugins {
		if p.Name == name {
			return p.Loader, nil
		}
	}
	return nil, fmt.Errorf("unknown eventbus %q; valid: %v", name, Names())
}

// PluginFlags aggregates CLI flags from all registered plugins.
func PluginFlags(cfg *config.Config) []cli.Flag {
	mu.Lock()
	defer mu.Unlock()
	var flags []cli.Flag
	for _, p := range plugins {
		if p.Flags != nil {
			flags = append(flags, p.Flags(cfg)...)
		}
	}
	return flags
}

// ApplyAll calls Apply on all registered plugins.
func ApplyAll(cfg *config.Config, cmd *cli.Command) {
	mu.Lock()
	defer mu.Unlock()
	for _, p := range plugins {
		if p.Apply != nil {
			p.Apply(cfg, cmd)
		}
	}
}

// --- context helpers ---

type contextKey struct{}

// WithContext stores an EventBus in the context.
func WithContext(ctx context.Context, bus EventBus) context.Context {
	return context.WithValue(ctx, contextKey{}, bus)
}

// FromContext retrieves an EventBus from the context.
func FromContext(ctx context.Context) EventBus {
	bus, _ := ctx.Value(contextKey{}).(EventBus)
	return bus
}
