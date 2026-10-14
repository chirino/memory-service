//go:build !nosqlite

package sqlite

import (
	"context"
	"fmt"
	"time"

	registrystore "github.com/chirino/memory-service/internal/registry/store"
)

type sqliteAdminStatsRow struct {
	ConversationGroupsTotal    int64      `gorm:"column:conversation_groups_total"`
	ConversationGroupsArchived int64      `gorm:"column:conversation_groups_archived"`
	ConversationGroupsOldestAt *time.Time `gorm:"column:conversation_groups_oldest_archived_at"`
	ConversationsTotal         int64      `gorm:"column:conversations_total"`
	EntriesTotal               int64      `gorm:"column:entries_total"`
	OutboxEventsTotal          int64      `gorm:"column:outbox_events_total"`
	OldestOutboxAt             *time.Time `gorm:"column:oldest_outbox_at"`
}

func (s *SQLiteStore) AdminStatsSummary(ctx context.Context) (*registrystore.AdminStatsSummary, error) {
	db := s.dbFor(ctx)
	var row sqliteAdminStatsRow
	if err := db.Raw(`
		SELECT
			(SELECT COUNT(*) FROM conversation_groups WHERE archived_at IS NULL) AS conversation_groups_total,
			(SELECT COUNT(*) FROM conversation_groups WHERE archived_at IS NOT NULL) AS conversation_groups_archived,
			(SELECT updated_at FROM conversations WHERE archived_at IS NOT NULL ORDER BY updated_at, updated_at_ns LIMIT 1) AS conversation_groups_oldest_archived_at,
			(SELECT updated_at_ns FROM conversations WHERE archived_at IS NOT NULL ORDER BY updated_at, updated_at_ns LIMIT 1) AS conversation_groups_oldest_archived_at_ns,
			(SELECT COUNT(*) FROM conversations WHERE archived_at IS NULL) AS conversations_total,
			(SELECT COUNT(*) FROM entries) AS entries_total,
			(SELECT COUNT(*) FROM outbox_events) AS outbox_events_total,
			(SELECT created_at FROM outbox_events ORDER BY created_at, created_at_ns LIMIT 1) AS oldest_outbox_at,
			(SELECT created_at_ns FROM outbox_events ORDER BY created_at, created_at_ns LIMIT 1) AS oldest_outbox_at_ns
	`).Scan(&row).Error; err != nil {
		return nil, fmt.Errorf("admin stats summary: %w", err)
	}

	return &registrystore.AdminStatsSummary{
		ConversationGroups: registrystore.AdminConversationGroupStats{
			Total:            row.ConversationGroupsTotal,
			Archived:         row.ConversationGroupsArchived,
			OldestArchivedAt: row.ConversationGroupsOldestAt,
		},
		Conversations: registrystore.AdminTotalStats{
			Total: row.ConversationsTotal,
		},
		Entries: registrystore.AdminTotalStats{
			Total: row.EntriesTotal,
		},
		OutboxEvents: &registrystore.AdminOutboxStats{
			Total:    row.OutboxEventsTotal,
			OldestAt: row.OldestOutboxAt,
		},
	}, nil
}

var _ registrystore.AdminStatsSummaryProvider = (*SQLiteStore)(nil)
