//go:build !nosqlite

package sqlite

import (
	"context"
	"fmt"
	"time"

	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
)

type sqliteEpisodicAdminStatsRow struct {
	MemoriesTotal    int64      `gorm:"column:memories_total"`
	MemoriesArchived int64      `gorm:"column:memories_archived"`
	MemoriesOldestAt *time.Time `gorm:"column:memories_oldest_archived_at"`
}

func (e *sqliteEpisodicStore) AdminStatsSummary(ctx context.Context) (*registryepisodic.AdminStatsSummary, error) {
	var row sqliteEpisodicAdminStatsRow
	if err := e.dbFor(ctx).Raw(`
		SELECT
			(SELECT COUNT(*) FROM memories WHERE archived_at IS NULL) AS memories_total,
			(SELECT COUNT(*) FROM memories WHERE archived_at IS NOT NULL) AS memories_archived,
			(SELECT archived_at FROM memories WHERE archived_at IS NOT NULL ORDER BY archived_at, archived_at_ns LIMIT 1) AS memories_oldest_archived_at,
			(SELECT archived_at_ns FROM memories WHERE archived_at IS NOT NULL ORDER BY archived_at, archived_at_ns LIMIT 1) AS memories_oldest_archived_at_ns
	`).Scan(&row).Error; err != nil {
		return nil, fmt.Errorf("episodic admin stats summary: %w", err)
	}

	return &registryepisodic.AdminStatsSummary{
		Memories: registryepisodic.AdminMemoryStats{
			Total:            row.MemoriesTotal,
			Archived:         row.MemoriesArchived,
			OldestArchivedAt: row.MemoriesOldestAt,
		},
	}, nil
}

var _ registryepisodic.AdminStatsSummaryProvider = (*sqliteEpisodicStore)(nil)
