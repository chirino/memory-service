//go:build !nomongo

package mongo

import (
	"context"
	"fmt"
	"strings"

	"github.com/chirino/memory-service/internal/plugin/store/fieldmigration"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var _ registrystore.FieldEncryptionMigrator = (*MongoStore)(nil)

func (s *MongoStore) MigrateEncryptedFields(ctx context.Context, opts registrystore.FieldEncryptionMigrationOptions) (*registrystore.FieldEncryptionMigrationStats, error) {
	if s.enc == nil {
		return nil, fmt.Errorf("mongo field migration requires database encryption to be enabled")
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

func (s *MongoStore) migrateConversationTitles(ctx context.Context, opts registrystore.FieldEncryptionMigrationOptions, stats *registrystore.FieldEncryptionMigrationDomainStats) error {
	for offset := int64(0); ; {
		findOpts := options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(int64(opts.BatchSize)).SetSkip(offset)
		cur, err := s.conversations().Find(ctx, bson.M{}, findOpts)
		if err != nil {
			return err
		}
		var rows []convDoc
		if err := cur.All(ctx, &rows); err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			if err := s.migrateMongoField(ctx, opts, stats, row.Title, conversationTitleFieldDomain, row.ID, s.conversations(), bson.M{"_id": row.ID, "title": row.Title}, "title"); err != nil {
				return err
			}
		}
		offset += int64(len(rows))
	}
}

func (s *MongoStore) migrateEntryContent(ctx context.Context, opts registrystore.FieldEncryptionMigrationOptions, stats *registrystore.FieldEncryptionMigrationDomainStats) error {
	for offset := int64(0); ; {
		findOpts := options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(int64(opts.BatchSize)).SetSkip(offset)
		cur, err := s.entries().Find(ctx, bson.M{}, findOpts)
		if err != nil {
			return err
		}
		var rows []entryDoc
		if err := cur.All(ctx, &rows); err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			if err := s.migrateMongoField(ctx, opts, stats, row.Content, entryContentFieldDomain, strings.ToLower(row.ID), s.entries(), bson.M{"_id": row.ID, "content": row.Content}, "content"); err != nil {
				return err
			}
		}
		offset += int64(len(rows))
	}
}

func (s *MongoStore) migrateCheckpointValues(ctx context.Context, opts registrystore.FieldEncryptionMigrationOptions, stats *registrystore.FieldEncryptionMigrationDomainStats) error {
	collection := s.db.Collection("admin_checkpoints")
	for offset := int64(0); ; {
		findOpts := options.Find().SetSort(bson.D{{Key: "client_id", Value: 1}}).SetLimit(int64(opts.BatchSize)).SetSkip(offset)
		cur, err := collection.Find(ctx, bson.M{}, findOpts)
		if err != nil {
			return err
		}
		var rows []checkpointDoc
		if err := cur.All(ctx, &rows); err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			if err := s.migrateMongoField(ctx, opts, stats, row.Value, adminCheckpointValueFieldDomain, row.ClientID, collection, bson.M{"client_id": row.ClientID, "value": row.Value}, "value"); err != nil {
				return err
			}
		}
		offset += int64(len(rows))
	}
}

func (s *MongoStore) migrateMemoryValues(ctx context.Context, opts registrystore.FieldEncryptionMigrationOptions, stats *registrystore.FieldEncryptionMigrationDomainStats) error {
	collection := s.db.Collection("memories")
	for offset := int64(0); ; {
		findOpts := options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(int64(opts.BatchSize)).SetSkip(offset)
		cur, err := collection.Find(ctx, bson.M{"value_encrypted": bson.M{"$exists": true, "$ne": nil}}, findOpts)
		if err != nil {
			return err
		}
		var rows []memoryDoc
		if err := cur.All(ctx, &rows); err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			if err := s.migrateMongoField(ctx, opts, stats, row.ValueEncrypted, memoryValueFieldDomain, strings.ToLower(row.ID), collection, bson.M{"_id": row.ID, "value_encrypted": row.ValueEncrypted}, "value_encrypted"); err != nil {
				return err
			}
		}
		offset += int64(len(rows))
	}
}

func (s *MongoStore) migrateMongoField(ctx context.Context, opts registrystore.FieldEncryptionMigrationOptions, stats *registrystore.FieldEncryptionMigrationDomainStats, ciphertext []byte, domain string, identity string, collection *mongo.Collection, filter bson.M, fieldName string) error {
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
	update, err := collection.UpdateOne(ctx, filter, bson.M{"$set": bson.M{fieldName: result.Ciphertext}})
	if err != nil {
		return fmt.Errorf("migrate encrypted field domain=%s identity=%s: %w", domain, identity, err)
	}
	if update.MatchedCount == 0 {
		stats.ConcurrentSkipped++
		return nil
	}
	stats.Migrated++
	return nil
}
