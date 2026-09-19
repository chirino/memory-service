package clickhouse

import (
	"context"
	"time"
)

type Common struct {
	ExporterID    string
	BatchID       string
	EventID       string
	SourceCursor  string
	IngestVersion uint64
	ObservedAt    time.Time
	SchemaVersion uint16
}

type LifecycleRow struct {
	Common
	OccurredAt          time.Time
	ResourceKind        string
	AnalyticsResourceID string
	Action              string
	Change              string
	ConversationID      string
	ConversationGroupID string
	ContentType         string
	MemoryKind          string
	SnapshotAvailable   bool
	SummaryJSON         string
}

type ResourceRow struct {
	Common
	ResourceID          string
	ConversationID      string
	ConversationGroupID string
	ResourceType        string
	CreatedAt           time.Time
	UpdatedAt           time.Time
	IsArchived          bool
	IsDeleted           bool
	PayloadJSON         string
}

type PurgeRow struct {
	ExporterID          string
	BatchID             string
	PurgeID             string
	EventID             string
	ResourceKind        string
	AnalyticsResourceID string
	ConversationID      string
	ConversationGroupID string
	RequestedAt         time.Time
}

type Batch struct {
	ID                 string
	ExporterID         string
	FirstCursor        string
	LastCursor         string
	Version            uint64
	ObservedAt         time.Time
	Lifecycle          []LifecycleRow
	Resources          []ResourceRow
	Purges             []PurgeRow
	Projections        []ProjectionRow
	ProjectionFailures []ProjectionFailureRow
}

type Sink interface {
	EnsureSchema(ctx context.Context, mode string) error
	WriteBatch(ctx context.Context, batch Batch) error
	Close() error
}
