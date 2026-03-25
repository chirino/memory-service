//go:build !nosqlite

package sqlite

import (
	"context"
	"fmt"
	"strings"
	"time"

	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
)

type sqliteEpisodicAdminStatsRow struct {
	MemoriesTotal               int64   `gorm:"column:memories_total"`
	MemoriesSoftDeleted         int64   `gorm:"column:memories_soft_deleted"`
	MemoriesOldestSoftDeletedAt *string `gorm:"column:memories_oldest_soft_deleted_at"`
}

func (e *sqliteEpisodicStore) AdminStatsSummary(ctx context.Context) (*registryepisodic.AdminStatsSummary, error) {
	var row sqliteEpisodicAdminStatsRow
	if err := e.dbFor(ctx).Raw(`
		SELECT
			(SELECT COUNT(*) FROM memories WHERE deleted_at IS NULL) AS memories_total,
			(SELECT COUNT(*) FROM memories WHERE deleted_at IS NOT NULL) AS memories_soft_deleted,
			(SELECT MIN(deleted_at) FROM memories WHERE deleted_at IS NOT NULL) AS memories_oldest_soft_deleted_at
	`).Scan(&row).Error; err != nil {
		return nil, fmt.Errorf("episodic admin stats summary: %w", err)
	}
	oldestSoftDeletedAt, err := parseSQLiteSummaryTimestamp(row.MemoriesOldestSoftDeletedAt)
	if err != nil {
		return nil, fmt.Errorf("parse oldest soft-deleted memory timestamp: %w", err)
	}

	return &registryepisodic.AdminStatsSummary{
		Memories: registryepisodic.AdminMemoryStats{
			Total:               row.MemoriesTotal,
			SoftDeleted:         row.MemoriesSoftDeleted,
			OldestSoftDeletedAt: oldestSoftDeletedAt,
		},
	}, nil
}

var _ registryepisodic.AdminStatsSummaryProvider = (*sqliteEpisodicStore)(nil)

func parseSQLiteSummaryTimestamp(raw *string) (*time.Time, error) {
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
