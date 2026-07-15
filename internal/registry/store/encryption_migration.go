package store

import "context"

// FieldEncryptionMigrationOptions configures persisted encrypted-field migration.
type FieldEncryptionMigrationOptions struct {
	DryRun    bool
	BatchSize int
}

// FieldEncryptionMigrationDomainStats records migration progress for one field domain.
type FieldEncryptionMigrationDomainStats struct {
	Scanned            int
	AlreadyV4          int
	LegacyValues       int
	HeaderlessValues   int
	DryRunWouldMigrate int
	Migrated           int
	ConcurrentSkipped  int
}

// FieldEncryptionMigrationStats records migration progress across all field domains.
type FieldEncryptionMigrationStats struct {
	Domains map[string]*FieldEncryptionMigrationDomainStats
}

// Domain returns mutable stats for name.
func (s *FieldEncryptionMigrationStats) Domain(name string) *FieldEncryptionMigrationDomainStats {
	if s.Domains == nil {
		s.Domains = map[string]*FieldEncryptionMigrationDomainStats{}
	}
	stats := s.Domains[name]
	if stats == nil {
		stats = &FieldEncryptionMigrationDomainStats{}
		s.Domains[name] = stats
	}
	return stats
}

// FieldEncryptionMigrator is implemented by stores that can rewrite legacy
// persisted encrypted fields to the current MSEH v4 field format.
type FieldEncryptionMigrator interface {
	MigrateEncryptedFields(ctx context.Context, opts FieldEncryptionMigrationOptions) (*FieldEncryptionMigrationStats, error)
}
