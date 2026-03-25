//go:build !nosqlite

package sqlite

import (
	"context"
	"fmt"
	"strings"
	"time"

	registrystore "github.com/chirino/memory-service/internal/registry/store"
)

type sqliteAdminStatsRow struct {
	ConversationGroupsTotal               int64   `gorm:"column:conversation_groups_total"`
	ConversationGroupsSoftDeleted         int64   `gorm:"column:conversation_groups_soft_deleted"`
	ConversationGroupsOldestSoftDeletedAt *string `gorm:"column:conversation_groups_oldest_soft_deleted_at"`
	ConversationsTotal                    int64   `gorm:"column:conversations_total"`
	EntriesTotal                          int64   `gorm:"column:entries_total"`
	OutboxEventsTotal                     int64   `gorm:"column:outbox_events_total"`
	OldestOutboxAt                        *string `gorm:"column:oldest_outbox_at"`
}

func (s *SQLiteStore) AdminStatsSummary(ctx context.Context) (*registrystore.AdminStatsSummary, error) {
	db := s.dbFor(ctx)
	var row sqliteAdminStatsRow
	if err := db.Raw(`
		SELECT
			(SELECT COUNT(*) FROM conversation_groups WHERE deleted_at IS NULL) AS conversation_groups_total,
			(SELECT COUNT(*) FROM conversation_groups WHERE deleted_at IS NOT NULL) AS conversation_groups_soft_deleted,
			(SELECT MIN(updated_at) FROM conversations WHERE deleted_at IS NOT NULL) AS conversation_groups_oldest_soft_deleted_at,
			(SELECT COUNT(*) FROM conversations WHERE deleted_at IS NULL) AS conversations_total,
			(SELECT COUNT(*) FROM entries) AS entries_total,
			(SELECT COUNT(*) FROM outbox_events) AS outbox_events_total,
			(SELECT MIN(created_at) FROM outbox_events) AS oldest_outbox_at
	`).Scan(&row).Error; err != nil {
		return nil, fmt.Errorf("admin stats summary: %w", err)
	}

	oldestSoftDeletedAt, err := parseSQLiteSummaryTime(row.ConversationGroupsOldestSoftDeletedAt)
	if err != nil {
		return nil, fmt.Errorf("parse oldest soft-deleted conversation timestamp: %w", err)
	}
	oldestOutboxAt, err := parseSQLiteSummaryTime(row.OldestOutboxAt)
	if err != nil {
		return nil, fmt.Errorf("parse oldest outbox timestamp: %w", err)
	}

	return &registrystore.AdminStatsSummary{
		ConversationGroups: registrystore.AdminConversationGroupStats{
			Total:               row.ConversationGroupsTotal,
			SoftDeleted:         row.ConversationGroupsSoftDeleted,
			OldestSoftDeletedAt: oldestSoftDeletedAt,
		},
		Conversations: registrystore.AdminTotalStats{
			Total: row.ConversationsTotal,
		},
		Entries: registrystore.AdminTotalStats{
			Total: row.EntriesTotal,
		},
		OutboxEvents: &registrystore.AdminOutboxStats{
			Total:    row.OutboxEventsTotal,
			OldestAt: oldestOutboxAt,
		},
	}, nil
}

var _ registrystore.AdminStatsSummaryProvider = (*SQLiteStore)(nil)

func parseSQLiteSummaryTime(raw *string) (*time.Time, error) {
	if raw == nil {
		return nil, nil
	}
	value := strings.TrimSpace(*raw)
	if value == "" {
		return nil, nil
	}
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			t := parsed.UTC()
			return &t, nil
		}
	}
	return nil, fmt.Errorf("unsupported time format %q", value)
}
