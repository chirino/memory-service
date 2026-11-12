package store

import (
	"context"
	"errors"
	"time"

	"github.com/chirino/memory-service/internal/model"
)

var ErrEventSnapshotUnsupported = errors.New("event snapshot scan unsupported")

// EventSnapshotEntryCursor is the stable ascending position used while
// emitting the current state at the start of an admin event subscription.
type EventSnapshotEntryCursor struct {
	CreatedAt time.Time
	ID        string
}

type EventSnapshotConversationCursor struct {
	CreatedAt time.Time
	ID        string
}

// EventSnapshotStore exposes the global scans required by
// initial_state=current admin event subscriptions.
type EventSnapshotStore interface {
	AdminListEventSnapshotConversations(ctx context.Context, after *EventSnapshotConversationCursor, limit int) ([]ConversationSummary, *EventSnapshotConversationCursor, error)
	AdminListEventSnapshotEntries(ctx context.Context, after *EventSnapshotEntryCursor, limit int) ([]model.Entry, *EventSnapshotEntryCursor, error)
}

// EventSnapshotMembershipCursor orders group/user pairs without depending on mutable roles.
type EventSnapshotMembershipCursor struct {
	GroupID string
	UserID  string
}

// MembershipSnapshotStore provides bounded global scans and exact admin reads.
type MembershipSnapshotStore interface {
	SupportsAnalyticsMembershipSnapshots() bool
	AdminListEventSnapshotMemberships(context.Context, *EventSnapshotMembershipCursor, int) ([]model.ConversationMembership, *EventSnapshotMembershipCursor, error)
	AdminGetEventSnapshotMembership(context.Context, string, string) (*model.ConversationMembership, error)
}
