//go:build !nopostgresql

package postgres

import (
	"context"
	"fmt"
	"time"

	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
)

type postgresEpisodicAdminStatsRow struct {
	MemoriesTotal               int64      `gorm:"column:memories_total"`
	MemoriesSoftDeleted         int64      `gorm:"column:memories_soft_deleted"`
	MemoriesOldestSoftDeletedAt *time.Time `gorm:"column:memories_oldest_soft_deleted_at"`
}

func (e *postgresEpisodicStore) AdminStatsSummary(ctx context.Context) (*registryepisodic.AdminStatsSummary, error) {
	var row postgresEpisodicAdminStatsRow
	if err := e.db.WithContext(ctx).Raw(`
		SELECT
			(SELECT COUNT(*) FROM memories WHERE deleted_at IS NULL) AS memories_total,
			(SELECT COUNT(*) FROM memories WHERE deleted_at IS NOT NULL) AS memories_soft_deleted,
			(SELECT MIN(deleted_at) FROM memories WHERE deleted_at IS NOT NULL) AS memories_oldest_soft_deleted_at
	`).Scan(&row).Error; err != nil {
		return nil, fmt.Errorf("episodic admin stats summary: %w", err)
	}

	return &registryepisodic.AdminStatsSummary{
		Memories: registryepisodic.AdminMemoryStats{
			Total:               row.MemoriesTotal,
			SoftDeleted:         row.MemoriesSoftDeleted,
			OldestSoftDeletedAt: row.MemoriesOldestSoftDeletedAt,
		},
	}, nil
}

var _ registryepisodic.AdminStatsSummaryProvider = (*postgresEpisodicStore)(nil)
