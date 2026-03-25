package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var ErrStaleOutboxCursor = errors.New("stale outbox cursor")
var ErrOutboxReplayUnsupported = errors.New("outbox replay unsupported")

// OutboxWrite is the normalized event payload persisted during the write path.
type OutboxWrite struct {
	Event     string
	Kind      string
	Data      json.RawMessage
	CreatedAt time.Time
}

// OutboxEvent is the durable replay envelope returned to stream consumers.
type OutboxEvent struct {
	Cursor    string
	Event     string
	Kind      string
	Data      json.RawMessage
	CreatedAt time.Time
}

// OutboxQuery is the input for durable replay reads.
type OutboxQuery struct {
	AfterCursor string
	Limit       int
	Kinds       []string
}

// OutboxPage is a replay page.
type OutboxPage struct {
	Events  []OutboxEvent
	HasMore bool
}

// EventOutboxStore is an optional capability implemented by datastores that
// support durable event persistence and replay.
type EventOutboxStore interface {
	AppendOutboxEvents(ctx context.Context, events []OutboxWrite) ([]OutboxEvent, error)
	ListOutboxEvents(ctx context.Context, query OutboxQuery) (*OutboxPage, error)
	EvictOutboxEventsBefore(ctx context.Context, before time.Time, limit int) (int64, error)
}

// OutboxEnabledProvider exposes whether outbox writes are enabled for the
// current store instance.
type OutboxEnabledProvider interface {
	OutboxEnabled() bool
}
