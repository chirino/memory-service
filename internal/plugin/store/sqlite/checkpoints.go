package sqlite

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"gorm.io/gorm/clause"
)

type sqliteCheckpointRow struct {
	ClientID    string          `gorm:"column:client_id;primaryKey"`
	ContentType string          `gorm:"column:content_type"`
	Value       json.RawMessage `gorm:"column:value"`
	UpdatedAt   time.Time       `gorm:"column:updated_at"`
}

func (sqliteCheckpointRow) TableName() string { return "admin_checkpoints" }

func (s *SQLiteStore) AdminGetCheckpoint(ctx context.Context, clientID string) (*registrystore.ClientCheckpoint, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return nil, &registrystore.ValidationError{Field: "clientId", Message: "clientId is required"}
	}
	var row sqliteCheckpointRow
	result := s.dbFor(ctx).
		Where("client_id = ?", clientID).
		Limit(1).
		Find(&row)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, &registrystore.NotFoundError{Resource: "checkpoint", ID: clientID}
	}
	value, err := s.decrypt(row.Value)
	if err != nil {
		return nil, err
	}
	return &registrystore.ClientCheckpoint{
		ClientID:    row.ClientID,
		ContentType: row.ContentType,
		Value:       append(json.RawMessage(nil), value...),
		UpdatedAt:   row.UpdatedAt.UTC(),
	}, nil
}

func (s *SQLiteStore) AdminPutCheckpoint(ctx context.Context, checkpoint registrystore.ClientCheckpoint) (*registrystore.ClientCheckpoint, error) {
	clientID := strings.TrimSpace(checkpoint.ClientID)
	contentType := strings.TrimSpace(checkpoint.ContentType)
	value := append(json.RawMessage(nil), checkpoint.Value...)
	if clientID == "" {
		return nil, &registrystore.ValidationError{Field: "clientId", Message: "clientId is required"}
	}
	if contentType == "" {
		return nil, &registrystore.ValidationError{Field: "contentType", Message: "contentType is required"}
	}
	if !json.Valid(value) {
		return nil, &registrystore.ValidationError{Field: "value", Message: "value must be valid JSON"}
	}
	encryptedValue, err := s.encrypt(value)
	if err != nil {
		return nil, err
	}
	row := sqliteCheckpointRow{
		ClientID:    clientID,
		ContentType: contentType,
		Value:       encryptedValue,
		UpdatedAt:   time.Now().UTC(),
	}
	if err := s.writeDBFor(ctx, "sqlite store put checkpoint").Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "client_id"}},
		DoUpdates: clause.Assignments(map[string]any{"content_type": row.ContentType, "value": row.Value, "updated_at": row.UpdatedAt}),
	}).Create(&row).Error; err != nil {
		return nil, err
	}
	return &registrystore.ClientCheckpoint{
		ClientID:    row.ClientID,
		ContentType: row.ContentType,
		Value:       append(json.RawMessage(nil), value...),
		UpdatedAt:   row.UpdatedAt,
	}, nil
}

var _ registrystore.AdminCheckpointStore = (*SQLiteStore)(nil)
