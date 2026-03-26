//go:build !nopostgresql

package postgres

import (
	"context"
	"fmt"
	"time"

	registrystore "github.com/chirino/memory-service/internal/registry/store"
)

type postgresAdminStatsRow struct {
	ConversationGroupsTotal    int64      `gorm:"column:conversation_groups_total"`
	ConversationGroupsArchived int64      `gorm:"column:conversation_groups_archived"`
	ConversationGroupsOldestAt *time.Time `gorm:"column:conversation_groups_oldest_archived_at"`
	ConversationsTotal         int64      `gorm:"column:conversations_total"`
	EntriesTotal               int64      `gorm:"column:entries_total"`
	OutboxEventsTotal          int64      `gorm:"column:outbox_events_total"`
	OldestOutboxAt             *time.Time `gorm:"column:oldest_outbox_at"`
}

func (s *PostgresStore) AdminStatsSummary(ctx context.Context) (*registrystore.AdminStatsSummary, error) {
	db := s.dbFor(ctx)
	var row postgresAdminStatsRow
	if err := db.Raw(`
		SELECT
			(SELECT COUNT(*) FROM conversation_groups WHERE archived_at IS NULL) AS conversation_groups_total,
			(SELECT COUNT(*) FROM conversation_groups WHERE archived_at IS NOT NULL) AS conversation_groups_archived,
			(SELECT MIN(updated_at) FROM conversations WHERE archived_at IS NOT NULL) AS conversation_groups_oldest_archived_at,
			(SELECT COUNT(*) FROM conversations WHERE archived_at IS NULL) AS conversations_total,
			(SELECT COUNT(*) FROM entries) AS entries_total,
			(SELECT COUNT(*) FROM outbox_events) AS outbox_events_total,
			(SELECT MIN(created_at) FROM outbox_events) AS oldest_outbox_at
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

var _ registrystore.AdminStatsSummaryProvider = (*PostgresStore)(nil)
