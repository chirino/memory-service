//go:build !nopostgresql

package postgres

import (
	"context"
	"fmt"
	"strconv"
	"time"

	registrystore "github.com/chirino/memory-service/internal/registry/store"
)

type postgresOutboxRow struct {
	Seq       int64     `gorm:"column:seq;primaryKey;autoIncrement"`
	Event     string    `gorm:"column:event"`
	Kind      string    `gorm:"column:kind"`
	Data      string    `gorm:"column:data"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

func (postgresOutboxRow) TableName() string { return "outbox_events" }

func parsePostgresOutboxCursor(cursor string) (int64, error) {
	if cursor == "" || cursor == "start" {
		return 0, nil
	}
	seq, err := strconv.ParseInt(cursor, 10, 64)
	if err != nil || seq < 0 {
		return 0, registrystore.ErrStaleOutboxCursor
	}
	return seq, nil
}

func formatPostgresOutboxCursor(seq int64) string {
	return strconv.FormatInt(seq, 10)
}

func (s *PostgresStore) AppendOutboxEvents(ctx context.Context, events []registrystore.OutboxWrite) ([]registrystore.OutboxEvent, error) {
	if len(events) == 0 {
		return nil, nil
	}
	db, err := s.writeDBFor(ctx, "append outbox events")
	if err != nil {
		return nil, err
	}
	rows := make([]postgresOutboxRow, 0, len(events))
	for _, event := range events {
		createdAt := event.CreatedAt
		if createdAt.IsZero() {
			createdAt = time.Now().UTC()
		}
		rows = append(rows, postgresOutboxRow{
			Event:     event.Event,
			Kind:      event.Kind,
			Data:      string(event.Data),
			CreatedAt: createdAt,
		})
	}
	if err := db.Create(&rows).Error; err != nil {
		return nil, fmt.Errorf("append outbox events: %w", err)
	}
	out := make([]registrystore.OutboxEvent, 0, len(rows))
	for _, row := range rows {
		out = append(out, registrystore.OutboxEvent{
			Cursor:    formatPostgresOutboxCursor(row.Seq),
			Event:     row.Event,
			Kind:      row.Kind,
			Data:      []byte(row.Data),
			CreatedAt: row.CreatedAt,
		})
	}
	return out, nil
}

func (s *PostgresStore) ListOutboxEvents(ctx context.Context, query registrystore.OutboxQuery) (*registrystore.OutboxPage, error) {
	afterSeq, err := parsePostgresOutboxCursor(query.AfterCursor)
	if err != nil {
		return nil, err
	}
	db := s.dbFor(ctx)
	if query.AfterCursor != "" && query.AfterCursor != "start" {
		var marker postgresOutboxRow
		result := db.Where("seq = ?", afterSeq).Limit(1).Find(&marker)
		if result.Error != nil {
			return nil, fmt.Errorf("check outbox cursor: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return nil, registrystore.ErrStaleOutboxCursor
		}
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 1000
	}
	tx := db.Order("seq ASC").Limit(limit + 1)
	if afterSeq > 0 {
		tx = tx.Where("seq > ?", afterSeq)
	}
	if len(query.Kinds) > 0 {
		tx = tx.Where("kind IN ?", query.Kinds)
	}
	var rows []postgresOutboxRow
	if err := tx.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list outbox events: %w", err)
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	events := make([]registrystore.OutboxEvent, 0, len(rows))
	for _, row := range rows {
		events = append(events, registrystore.OutboxEvent{
			Cursor:    formatPostgresOutboxCursor(row.Seq),
			Event:     row.Event,
			Kind:      row.Kind,
			Data:      []byte(row.Data),
			CreatedAt: row.CreatedAt,
		})
	}
	return &registrystore.OutboxPage{Events: events, HasMore: hasMore}, nil
}

func (s *PostgresStore) EvictOutboxEventsBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	db, err := s.writeDBFor(ctx, "evict outbox events")
	if err != nil {
		return 0, err
	}
	if limit <= 0 {
		limit = 1000
	}
	subquery := db.Model(&postgresOutboxRow{}).
		Select("seq").
		Where("created_at < ?", before).
		Order("seq ASC").
		Limit(limit)
	result := db.Where("seq IN (?)", subquery).Delete(&postgresOutboxRow{})
	if result.Error != nil {
		return 0, fmt.Errorf("evict outbox events: %w", result.Error)
	}
	return result.RowsAffected, nil
}

var _ registrystore.EventOutboxStore = (*PostgresStore)(nil)
