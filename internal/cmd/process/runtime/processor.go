package runtime

import (
	"context"
	"encoding/json"
	"time"
)

// EventEnvelope is the normalized event shape consumed by processors.
type EventEnvelope struct {
	Event  string
	Kind   string
	Data   json.RawMessage
	Cursor string
	Time   time.Time
}

// EventProcessor handles events and exposes a small durable checkpoint payload.
type EventProcessor interface {
	ContentType() string
	Load(state json.RawMessage) error
	Handle(ctx context.Context, event EventEnvelope) error
	Snapshot() (json.RawMessage, error)
	Flush(ctx context.Context) error
}

// EventStream opens a durable event stream.
type EventStream interface {
	Recv() (EventEnvelope, error)
}

// EventClient subscribes to Memory Service events.
type EventClient interface {
	Subscribe(ctx context.Context, req SubscribeRequest) (EventStream, error)
}

// SubscribeRequest is the runtime's transport-independent event request.
type SubscribeRequest struct {
	Kinds             []string
	Detail            string
	AfterCursor       string
	Scope             string
	Justification     string
	EntryChannels     []string
	EntryContentTypes []string
	EntryRoles        []string
}

// CheckpointClient persists one processor checkpoint.
type CheckpointClient interface {
	Get(ctx context.Context, clientID string) (Checkpoint, error)
	Put(ctx context.Context, clientID, contentType string, value json.RawMessage) (Checkpoint, error)
}

// Checkpoint is a stored processor checkpoint.
type Checkpoint struct {
	ClientID    string
	ContentType string
	Value       json.RawMessage
	UpdatedAt   time.Time
}
