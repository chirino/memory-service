//go:build !nopostgresql

package postgres

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	registrystore "github.com/chirino/memory-service/internal/registry/store"
)

const adminCheckpointValueFieldDomain = "admin-checkpoint.value"

type postgresCheckpointRow struct {
	ClientID        string          `gorm:"column:client_id;primaryKey"`
	ContentType     string          `gorm:"column:content_type"`
	Value           json.RawMessage `gorm:"column:value"`
	Revision        uint64          `gorm:"column:revision"`
	LeaseTokenHash  []byte          `gorm:"column:lease_token_hash"`
	LeaseGeneration uint64          `gorm:"column:lease_generation"`
	LeaseExpiresAt  *time.Time      `gorm:"column:lease_expires_at"`
	UpdatedAt       time.Time       `gorm:"column:updated_at"`
}

func (postgresCheckpointRow) TableName() string { return "admin_checkpoints" }

func (s *PostgresStore) AdminGetCheckpoint(ctx context.Context, clientID string) (*registrystore.ClientCheckpoint, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return nil, &registrystore.ValidationError{Field: "clientId", Message: "clientId is required"}
	}
	var row postgresCheckpointRow
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
	value, err := s.decryptCheckpointValue(clientID, row.Value)
	if err != nil {
		return nil, err
	}
	return &registrystore.ClientCheckpoint{
		ClientID:    row.ClientID,
		ContentType: row.ContentType,
		Value:       append(json.RawMessage(nil), value...),
		Revision:    registrystore.EncodeCheckpointRevision(row.Revision),
		UpdatedAt:   row.UpdatedAt.UTC(),
	}, nil
}

func (s *PostgresStore) AdminPutCheckpoint(ctx context.Context, checkpoint registrystore.ClientCheckpoint) (*registrystore.ClientCheckpoint, error) {
	return s.AdminPutCheckpointCAS(ctx, registrystore.CheckpointCASWrite{Checkpoint: checkpoint})
}

func (s *PostgresStore) AdminPutCheckpointCAS(ctx context.Context, write registrystore.CheckpointCASWrite) (*registrystore.ClientCheckpoint, error) {
	checkpoint := write.Checkpoint
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
	expectedRevision, err := registrystore.ParseCheckpointRevision(strings.TrimSpace(write.ExpectedRevision))
	if err != nil {
		return nil, err
	}
	tokenHash, err := registrystore.HashCheckpointLeaseToken(strings.TrimSpace(write.LeaseToken))
	if err != nil {
		return nil, err
	}
	var tokenValue []byte
	tokenPresent := strings.TrimSpace(write.LeaseToken) != ""
	if tokenPresent {
		tokenValue = tokenHash[:]
	}
	encryptedValue, err := s.encryptCheckpointValue(clientID, value)
	if err != nil {
		return nil, err
	}
	result := s.dbFor(ctx).Exec(`
		INSERT INTO admin_checkpoints (client_id, content_type, value, revision, updated_at)
		VALUES (?, ?, ?, 1, NOW())
		ON CONFLICT (client_id) DO UPDATE SET
			content_type = EXCLUDED.content_type,
			value = EXCLUDED.value,
			revision = admin_checkpoints.revision + 1,
			updated_at = NOW()
		WHERE (? = 0 OR admin_checkpoints.revision = ?)
		  AND (admin_checkpoints.lease_expires_at IS NULL
		       OR admin_checkpoints.lease_expires_at <= NOW()
		       OR (? AND admin_checkpoints.lease_token_hash = ?::bytea))`,
		clientID, contentType, encryptedValue,
		expectedRevision, expectedRevision, tokenPresent, tokenValue,
	)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, registrystore.NewCheckpointConflict()
	}
	stored, err := s.AdminGetCheckpoint(ctx, clientID)
	if err != nil {
		return nil, err
	}
	return stored, nil
}

func (s *PostgresStore) AdminAcquireCheckpointLease(ctx context.Context, clientID, token string, ttl time.Duration) (*registrystore.ClientCheckpointLease, error) {
	return s.postgresUpdateCheckpointLease(ctx, "acquire", clientID, token, ttl)
}

func (s *PostgresStore) AdminRenewCheckpointLease(ctx context.Context, clientID, token string, ttl time.Duration) (*registrystore.ClientCheckpointLease, error) {
	return s.postgresUpdateCheckpointLease(ctx, "renew", clientID, token, ttl)
}

func (s *PostgresStore) postgresUpdateCheckpointLease(ctx context.Context, operation, clientID, token string, ttl time.Duration) (*registrystore.ClientCheckpointLease, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return nil, &registrystore.ValidationError{Field: "clientId", Message: "clientId is required"}
	}
	if err := registrystore.ValidateCheckpointLeaseTTL(ttl); err != nil {
		return nil, err
	}
	hash, err := registrystore.HashCheckpointLeaseToken(strings.TrimSpace(token))
	if err != nil {
		return nil, err
	}
	var generation uint64
	var expiresAt time.Time
	var rowErr error
	seconds := int64(ttl / time.Second)
	if operation == "acquire" {
		rowErr = s.dbFor(ctx).Raw(`
			UPDATE admin_checkpoints
			SET lease_token_hash = ?, lease_generation = lease_generation + 1,
			    lease_expires_at = NOW() + (? * INTERVAL '1 second')
			WHERE client_id = ? AND (lease_expires_at IS NULL OR lease_expires_at <= NOW())
			RETURNING lease_generation, lease_expires_at`, hash[:], seconds, clientID).Row().Scan(&generation, &expiresAt)
	} else {
		rowErr = s.dbFor(ctx).Raw(`
			UPDATE admin_checkpoints
			SET lease_expires_at = NOW() + (? * INTERVAL '1 second')
			WHERE client_id = ? AND lease_token_hash = ? AND lease_expires_at > NOW()
			RETURNING lease_generation, lease_expires_at`, seconds, clientID, hash[:]).Row().Scan(&generation, &expiresAt)
	}
	if rowErr != nil {
		if strings.Contains(strings.ToLower(rowErr.Error()), "no rows") {
			return nil, registrystore.NewCheckpointConflict()
		}
		return nil, rowErr
	}
	return &registrystore.ClientCheckpointLease{ClientID: clientID, Token: token, Generation: generation, ExpiresAt: expiresAt.UTC()}, nil
}

func (s *PostgresStore) AdminReleaseCheckpointLease(ctx context.Context, clientID, token string) error {
	hash, err := registrystore.HashCheckpointLeaseToken(strings.TrimSpace(token))
	if err != nil {
		return err
	}
	result := s.dbFor(ctx).Exec(`UPDATE admin_checkpoints SET lease_token_hash = NULL, lease_expires_at = NULL WHERE client_id = ? AND lease_token_hash = ?`, strings.TrimSpace(clientID), hash[:])
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return registrystore.NewCheckpointConflict()
	}
	return nil
}

func (s *PostgresStore) encryptCheckpointValue(clientID string, value []byte) ([]byte, error) {
	if s.enc == nil || value == nil {
		return value, nil
	}
	return s.enc.EncryptField(value, adminCheckpointValueFieldDomain, clientID)
}

func (s *PostgresStore) decryptCheckpointValue(clientID string, value []byte) ([]byte, error) {
	if s.enc == nil || value == nil {
		return value, nil
	}
	return s.enc.DecryptField(value, adminCheckpointValueFieldDomain, clientID)
}

var _ registrystore.AdminCheckpointStore = (*PostgresStore)(nil)
var _ registrystore.AdminCheckpointLeaseStore = (*PostgresStore)(nil)
