//go:build !nopostgresql

package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/chirino/memory-service/internal/model"
	"github.com/chirino/memory-service/internal/plugin/store/fieldmigration"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
)

var _ registrystore.FieldEncryptionMigrator = (*PostgresStore)(nil)

func (s *PostgresStore) MigrateEncryptedFields(ctx context.Context, opts registrystore.FieldEncryptionMigrationOptions) (*registrystore.FieldEncryptionMigrationStats, error) {
	if s.enc == nil {
		return nil, fmt.Errorf("postgres field migration requires database encryption to be enabled")
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 500
	}
	stats := &registrystore.FieldEncryptionMigrationStats{}
	if err := s.migrateConversationTitles(ctx, opts, stats.Domain(conversationTitleFieldDomain)); err != nil {
		return stats, err
	}
	if err := s.migrateEntryContent(ctx, opts, stats.Domain(entryContentFieldDomain)); err != nil {
		return stats, err
	}
	if err := s.migrateCheckpointValues(ctx, opts, stats.Domain(adminCheckpointValueFieldDomain)); err != nil {
		return stats, err
	}
	if err := s.migrateMemoryValues(ctx, opts, stats.Domain(memoryValueFieldDomain)); err != nil {
		return stats, err
	}
	return stats, nil
}

func (s *PostgresStore) migrateConversationTitles(ctx context.Context, opts registrystore.FieldEncryptionMigrationOptions, stats *registrystore.FieldEncryptionMigrationDomainStats) error {
	for offset := 0; ; {
		var rows []model.Conversation
		if err := s.dbFor(ctx).Select("id", "title").Order("id ASC").Limit(opts.BatchSize).Offset(offset).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			if err := s.migrateSQLField(opts, stats, row.Title, conversationTitleFieldDomain, row.ID, func(newValue []byte) (int64, error) {
				result := s.dbFor(ctx).Model(&model.Conversation{}).Where("id = ? AND title = ?", row.ID, row.Title).Update("title", newValue)
				return result.RowsAffected, result.Error
			}); err != nil {
				return err
			}
		}
		offset += len(rows)
	}
}

func (s *PostgresStore) migrateEntryContent(ctx context.Context, opts registrystore.FieldEncryptionMigrationOptions, stats *registrystore.FieldEncryptionMigrationDomainStats) error {
	for offset := 0; ; {
		var rows []model.Entry
		if err := s.dbFor(ctx).Select("id", "conversation_group_id", "content").Order("id ASC").Limit(opts.BatchSize).Offset(offset).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			identity := strings.ToLower(row.ID.String())
			if err := s.migrateSQLField(opts, stats, row.Content, entryContentFieldDomain, identity, func(newValue []byte) (int64, error) {
				result := s.dbFor(ctx).Model(&model.Entry{}).
					Where("id = ? AND conversation_group_id = ? AND content = ?", row.ID, row.ConversationGroupID, row.Content).
					Update("content", newValue)
				return result.RowsAffected, result.Error
			}); err != nil {
				return err
			}
		}
		offset += len(rows)
	}
}

func (s *PostgresStore) migrateCheckpointValues(ctx context.Context, opts registrystore.FieldEncryptionMigrationOptions, stats *registrystore.FieldEncryptionMigrationDomainStats) error {
	for offset := 0; ; {
		var rows []postgresCheckpointRow
		if err := s.dbFor(ctx).Select("client_id", "value").Order("client_id ASC").Limit(opts.BatchSize).Offset(offset).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			if err := s.migrateSQLField(opts, stats, row.Value, adminCheckpointValueFieldDomain, row.ClientID, func(newValue []byte) (int64, error) {
				result := s.dbFor(ctx).Model(&postgresCheckpointRow{}).Where("client_id = ? AND value = ?", row.ClientID, row.Value).Update("value", newValue)
				return result.RowsAffected, result.Error
			}); err != nil {
				return err
			}
		}
		offset += len(rows)
	}
}

func (s *PostgresStore) migrateMemoryValues(ctx context.Context, opts registrystore.FieldEncryptionMigrationOptions, stats *registrystore.FieldEncryptionMigrationDomainStats) error {
	for offset := 0; ; {
		var rows []memoryRow
		if err := s.dbFor(ctx).Select("id", "value_encrypted").Where("value_encrypted IS NOT NULL").Order("id ASC").Limit(opts.BatchSize).Offset(offset).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			identity := strings.ToLower(row.ID.String())
			if err := s.migrateSQLField(opts, stats, row.ValueEncrypted, memoryValueFieldDomain, identity, func(newValue []byte) (int64, error) {
				result := s.dbFor(ctx).Model(&memoryRow{}).Where("id = ? AND value_encrypted = ?", row.ID, row.ValueEncrypted).Update("value_encrypted", newValue)
				return result.RowsAffected, result.Error
			}); err != nil {
				return err
			}
		}
		offset += len(rows)
	}
}

func (s *PostgresStore) migrateSQLField(opts registrystore.FieldEncryptionMigrationOptions, stats *registrystore.FieldEncryptionMigrationDomainStats, ciphertext []byte, domain string, identity string, update func([]byte) (int64, error)) error {
	stats.Scanned++
	result, err := fieldmigration.MigrateValue(s.enc, ciphertext, domain, identity)
	if err != nil {
		return fmt.Errorf("migrate encrypted field domain=%s identity=%s: %w", domain, identity, err)
	}
	if result.AlreadyV4 {
		stats.AlreadyV4++
		return nil
	}
	if result.Headerless {
		stats.HeaderlessValues++
	} else {
		stats.LegacyValues++
	}
	if opts.DryRun {
		stats.DryRunWouldMigrate++
		return nil
	}
	rows, err := update(result.Ciphertext)
	if err != nil {
		return fmt.Errorf("migrate encrypted field domain=%s identity=%s: %w", domain, identity, err)
	}
	if rows == 0 {
		stats.ConcurrentSkipped++
		return nil
	}
	stats.Migrated++
	return nil
}
