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
}

type FenceRow struct {
	Common
	ResourceKind        string
	ResourceID          string
	ConversationID      string
	ConversationGroupID string
}

const (
	purgeResource = "resource"
	purgeGroup    = "conversation_group"
)

type PurgeRow struct {
	ExporterID          string
	BatchID             string
	PurgeID             string
	EventID             string
	ResourceKind        string
	AnalyticsResourceID string
	ConversationID      string
	ConversationGroupID string
	Scope               string
	CutoffIngestVersion uint64
	RequestedAt         time.Time
}

type MembershipRow struct {
	Common
	ResourceID          string
	ConversationGroupID string
	UserID              string
	AccessLevel         string
	CreatedAt           *time.Time
	IsDeleted           bool
}

type Batch struct {
	ID                 string
	ExporterID         string
	FirstCursor        string
	LastCursor         string
	ObservedAt         time.Time
	Lifecycle          []LifecycleRow
	Fences             []FenceRow
	Purges             []PurgeRow
	Memberships        []MembershipRow
	Projections        []ProjectionRow
	ProjectionFailures []ProjectionFailureRow
}

type Sink interface {
	EnsureSchema(context.Context, string) error
	ResolvePurge(context.Context, PurgeRow) (PurgeRow, error)
	WriteBatch(context.Context, Batch) error
	Close() error
}
