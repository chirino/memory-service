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
	Change string
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

// BackgroundErrorSource reports terminal asynchronous failures that must stop
// processing before any more events or checkpoints are committed.
type BackgroundErrorSource interface {
	BackgroundErrors() <-chan error
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
	InitialState      string
}

// CheckpointClient persists one processor checkpoint.
type CheckpointClient interface {
	Get(ctx context.Context, clientID string) (Checkpoint, error)
	Put(ctx context.Context, clientID, contentType string, value json.RawMessage) (Checkpoint, error)
}

// LeasedCheckpointClient adds compare-and-swap ownership for processors that
// require one active replica per checkpoint.
type LeasedCheckpointClient interface {
	CheckpointClient
	PutCAS(ctx context.Context, clientID, contentType string, value json.RawMessage, expectedRevision, leaseToken string) (Checkpoint, error)
	AcquireLease(ctx context.Context, clientID string, ttl time.Duration) (CheckpointLease, error)
	RenewLease(ctx context.Context, clientID, leaseToken string, ttl time.Duration) (CheckpointLease, error)
	ReleaseLease(ctx context.Context, clientID, leaseToken string) error
}

type CheckpointLease struct {
	Token      string
	Generation uint64
	ExpiresAt  time.Time
}

// LeaseGenerationConsumer receives the fencing generation before event
// processing starts.
type LeaseGenerationConsumer interface {
	SetLeaseGeneration(generation uint64) error
}

// BootstrapProcessor performs a restartable current-state scan after the
// checkpoint lease is acquired and before live replay begins. save persists
// the processor's current scan token using the active lease.
type BootstrapProcessor interface {
	Bootstrap(ctx context.Context, events EventClient, save func(context.Context) error) (afterCursor string, err error)
}

// Checkpoint is a stored processor checkpoint.
type Checkpoint struct {
	ClientID    string
	ContentType string
	Value       json.RawMessage
	Revision    string
	UpdatedAt   time.Time
}
