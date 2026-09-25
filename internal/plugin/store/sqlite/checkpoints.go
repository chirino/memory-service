package sqlite

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	registrystore "github.com/chirino/memory-service/internal/registry/store"
)

const adminCheckpointValueFieldDomain = "admin-checkpoint.value"

type sqliteCheckpointRow struct {
	ClientID        string          `gorm:"column:client_id;primaryKey"`
	ContentType     string          `gorm:"column:content_type"`
	Value           json.RawMessage `gorm:"column:value"`
	Revision        uint64          `gorm:"column:revision"`
	LeaseTokenHash  []byte          `gorm:"column:lease_token_hash"`
	LeaseGeneration uint64          `gorm:"column:lease_generation"`
	LeaseExpiresAt  *time.Time      `gorm:"column:lease_expires_at"`
	UpdatedAt       time.Time       `gorm:"column:updated_at"`
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

func (s *SQLiteStore) AdminPutCheckpoint(ctx context.Context, checkpoint registrystore.ClientCheckpoint) (*registrystore.ClientCheckpoint, error) {
	return s.AdminPutCheckpointCAS(ctx, registrystore.CheckpointCASWrite{Checkpoint: checkpoint})
}

func (s *SQLiteStore) AdminPutCheckpointCAS(ctx context.Context, write registrystore.CheckpointCASWrite) (*registrystore.ClientCheckpoint, error) {
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
	tokenProvided := strings.TrimSpace(write.LeaseToken) != ""
	tokenValue := hex.EncodeToString(tokenHash[:])
	encryptedValue, err := s.encryptCheckpointValue(clientID, value)
	if err != nil {
		return nil, err
	}
	result := s.writeDBFor(ctx, "sqlite store put checkpoint").Exec(`
		INSERT INTO admin_checkpoints (client_id, content_type, value, revision, updated_at)
		VALUES (?, ?, ?, 1, CURRENT_TIMESTAMP)
		ON CONFLICT (client_id) DO UPDATE SET
			content_type = excluded.content_type,
			value = excluded.value,
			revision = admin_checkpoints.revision + 1,
			updated_at = CURRENT_TIMESTAMP
		WHERE (? = 0 OR admin_checkpoints.revision = ?)
		  AND (admin_checkpoints.lease_expires_at IS NULL
		       OR admin_checkpoints.lease_expires_at <= CURRENT_TIMESTAMP
		       OR (? = 1 AND admin_checkpoints.lease_token_hash = ?))`,
		clientID, contentType, encryptedValue,
		expectedRevision, expectedRevision, tokenProvided, tokenValue,
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

func (s *SQLiteStore) AdminAcquireCheckpointLease(ctx context.Context, clientID, token string, ttl time.Duration) (*registrystore.ClientCheckpointLease, error) {
	return s.sqliteUpdateCheckpointLease(ctx, "acquire", clientID, token, ttl)
}

func (s *SQLiteStore) AdminRenewCheckpointLease(ctx context.Context, clientID, token string, ttl time.Duration) (*registrystore.ClientCheckpointLease, error) {
	return s.sqliteUpdateCheckpointLease(ctx, "renew", clientID, token, ttl)
}

func (s *SQLiteStore) sqliteUpdateCheckpointLease(ctx context.Context, operation, clientID, token string, ttl time.Duration) (*registrystore.ClientCheckpointLease, error) {
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
	seconds := int64(ttl / time.Second)
	db := s.writeDBFor(ctx, "sqlite store update checkpoint lease")
	var execErr error
	var rows int64
	if operation == "acquire" {
		r := db.Exec(`UPDATE admin_checkpoints
			SET lease_token_hash = ?, lease_generation = lease_generation + 1,
			    lease_expires_at = datetime('now', '+' || ? || ' seconds')
			WHERE client_id = ? AND (lease_expires_at IS NULL OR lease_expires_at <= CURRENT_TIMESTAMP)`, hex.EncodeToString(hash[:]), seconds, clientID)
		execErr, rows = r.Error, r.RowsAffected
	} else {
		r := db.Exec(`UPDATE admin_checkpoints
			SET lease_expires_at = datetime('now', '+' || ? || ' seconds')
			WHERE client_id = ? AND lease_token_hash = ? AND lease_expires_at > CURRENT_TIMESTAMP`, seconds, clientID, hex.EncodeToString(hash[:]))
		execErr, rows = r.Error, r.RowsAffected
	}
	if execErr != nil {
		return nil, execErr
	}
	if rows == 0 {
		return nil, registrystore.NewCheckpointConflict()
	}
	var leaseRow struct {
		Generation uint64    `gorm:"column:lease_generation"`
		ExpiresAt  time.Time `gorm:"column:lease_expires_at"`
	}
	if err := db.Raw(`SELECT lease_generation, lease_expires_at FROM admin_checkpoints WHERE client_id = ?`, clientID).Scan(&leaseRow).Error; err != nil {
		return nil, err
	}
	return &registrystore.ClientCheckpointLease{ClientID: clientID, Token: token, Generation: leaseRow.Generation, ExpiresAt: leaseRow.ExpiresAt.UTC()}, nil
}

func (s *SQLiteStore) AdminReleaseCheckpointLease(ctx context.Context, clientID, token string) error {
	hash, err := registrystore.HashCheckpointLeaseToken(strings.TrimSpace(token))
	if err != nil {
		return err
	}
	result := s.writeDBFor(ctx, "sqlite store release checkpoint lease").Exec(`UPDATE admin_checkpoints SET lease_token_hash = NULL, lease_expires_at = NULL WHERE client_id = ? AND lease_token_hash = ?`, strings.TrimSpace(clientID), hex.EncodeToString(hash[:]))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return registrystore.NewCheckpointConflict()
	}
	return nil
}

func (s *SQLiteStore) encryptCheckpointValue(clientID string, value []byte) ([]byte, error) {
	if s.enc == nil || value == nil {
		return value, nil
	}
	return s.enc.EncryptField(value, adminCheckpointValueFieldDomain, clientID)
}

func (s *SQLiteStore) decryptCheckpointValue(clientID string, value []byte) ([]byte, error) {
	if s.enc == nil || value == nil {
		return value, nil
	}
	return s.enc.DecryptField(value, adminCheckpointValueFieldDomain, clientID)
}

var _ registrystore.AdminCheckpointStore = (*SQLiteStore)(nil)
var _ registrystore.AdminCheckpointLeaseStore = (*SQLiteStore)(nil)
