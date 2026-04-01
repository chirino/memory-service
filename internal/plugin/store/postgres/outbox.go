//go:build !nopostgresql

package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/jackc/pglogrepl"
)

type postgresOutboxRow struct {
	TxSeq     int64     `gorm:"column:tx_seq;primaryKey;autoIncrement"`
	CommitLSN *string   `gorm:"column:commit_lsn;type:pg_lsn"`
	Event     string    `gorm:"column:event"`
	Kind      string    `gorm:"column:kind"`
	Data      string    `gorm:"column:data"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

func (postgresOutboxRow) TableName() string { return "outbox_events" }

func parsePostgresOutboxCursor(cursor string) (pglogrepl.LSN, int64, error) {
	if cursor == "" || cursor == "start" {
		return 0, 0, nil
	}
	lsnPart, txSeqPart, ok := strings.Cut(strings.TrimSpace(cursor), ":")
	if !ok {
		return 0, 0, registrystore.ErrStaleOutboxCursor
	}
	lsn, err := pglogrepl.ParseLSN(lsnPart)
	if err != nil {
		return 0, 0, registrystore.ErrStaleOutboxCursor
	}
	var txSeq int64
	_, err = fmt.Sscanf(txSeqPart, "%d", &txSeq)
	if err != nil || txSeq < 0 {
		return 0, 0, registrystore.ErrStaleOutboxCursor
	}
	return lsn, txSeq, nil
}

func formatPostgresOutboxCursor(lsn pglogrepl.LSN, txSeq int64) string {
	return fmt.Sprintf("%s:%d", lsn.String(), txSeq)
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
			Event:     row.Event,
			Kind:      row.Kind,
			Data:      []byte(row.Data),
			CreatedAt: row.CreatedAt,
		})
	}
	return out, nil
}

func (s *PostgresStore) ListOutboxEvents(ctx context.Context, query registrystore.OutboxQuery) (*registrystore.OutboxPage, error) {
	afterLSN, afterTxSeq, err := parsePostgresOutboxCursor(query.AfterCursor)
	if err != nil {
		return nil, err
	}
	db := s.dbFor(ctx)
	if query.AfterCursor != "" && query.AfterCursor != "start" {
		var marker postgresOutboxRow
		result := db.Where("commit_lsn = ?::pg_lsn AND tx_seq = ?", afterLSN.String(), afterTxSeq).Limit(1).Find(&marker)
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
	tx := db.Where("commit_lsn IS NOT NULL").Order("commit_lsn ASC, tx_seq ASC").Limit(limit + 1)
	if afterLSN > 0 || afterTxSeq > 0 {
		tx = tx.Where("(commit_lsn > ?::pg_lsn OR (commit_lsn = ?::pg_lsn AND tx_seq > ?))", afterLSN.String(), afterLSN.String(), afterTxSeq)
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
		if row.CommitLSN == nil {
			continue
		}
		commitLSN, err := pglogrepl.ParseLSN(*row.CommitLSN)
		if err != nil {
			return nil, fmt.Errorf("list outbox events: invalid commit_lsn %q for tx_seq %d: %w", *row.CommitLSN, row.TxSeq, err)
		}
		events = append(events, registrystore.OutboxEvent{
			Cursor:    formatPostgresOutboxCursor(commitLSN, row.TxSeq),
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
		Select("ctid").
		Where("created_at < ?", before).
		Order("created_at ASC, tx_seq ASC").
		Limit(limit)
	result := db.Where("ctid IN (?)", subquery).Delete(&postgresOutboxRow{})
	if result.Error != nil {
		return 0, fmt.Errorf("evict outbox events: %w", result.Error)
	}
	return result.RowsAffected, nil
}

var _ registrystore.EventOutboxStore = (*PostgresStore)(nil)
