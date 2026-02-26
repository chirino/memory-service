package postgres

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/model"
	registrycache "github.com/chirino/memory-service/internal/registry/cache"
	registrymigrate "github.com/chirino/memory-service/internal/registry/migrate"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/security"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func init() {
	registrystore.Register(registrystore.Plugin{
		Name: "postgres",
		Loader: func(ctx context.Context) (registrystore.MemoryStore, error) {
			cfg := config.FromContext(ctx)
			db, err := gorm.Open(postgres.Open(cfg.DBURL), &gorm.Config{})
			if err != nil {
				return nil, fmt.Errorf("failed to connect to postgres: %w", err)
			}
			sqlDB, err := db.DB()
			if err != nil {
				return nil, fmt.Errorf("failed to get underlying db: %w", err)
			}
			sqlDB.SetMaxOpenConns(cfg.DBMaxOpenConns)
			sqlDB.SetMaxIdleConns(cfg.DBMaxIdleConns)
			if security.DBPoolMaxConnections != nil {
				security.DBPoolMaxConnections.Set(float64(cfg.DBMaxOpenConns))
			}

			// Periodically update the open connections gauge.
			go func() {
				ticker := time.NewTicker(15 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						if security.DBPoolOpenConnections != nil {
							security.DBPoolOpenConnections.Set(float64(sqlDB.Stats().OpenConnections))
						}
					}
				}
			}()

			store := &PostgresStore{
				db:           db,
				cfg:          cfg,
				entriesCache: registrycache.EntriesCacheFromContext(ctx),
			}
			if cfg.EncryptionKey != "" && !cfg.EncryptionDBDisabled {
				key, err := config.DecodeEncryptionKey(cfg.EncryptionKey)
				if err != nil {
					return nil, fmt.Errorf("invalid encryption key: %w", err)
				}
				gcm, err := newGCM(key)
				if err != nil {
					return nil, fmt.Errorf("failed to create GCM: %w", err)
				}
				store.gcms = append(store.gcms, gcm)

				legacyKeys, err := config.DecodeEncryptionKeysCSV(cfg.EncryptionDecryptionKeys)
				if err != nil {
					return nil, fmt.Errorf("invalid decryption key list: %w", err)
				}
				for _, legacyKey := range legacyKeys {
					legacyGCM, legacyErr := newGCM(legacyKey)
					if legacyErr != nil {
						return nil, fmt.Errorf("failed to create legacy decryption GCM: %w", legacyErr)
					}
					store.gcms = append(store.gcms, legacyGCM)
				}
			}
			return store, nil
		},
	})

	registrymigrate.Register(registrymigrate.Plugin{Order: 100, Migrator: &postgresMigrator{}})
}

type postgresMigrator struct{}

func (m *postgresMigrator) Name() string { return "postgres-schema" }
func (m *postgresMigrator) Migrate(ctx context.Context) error {
	cfg := config.FromContext(ctx)
	if cfg != nil && !cfg.DatastoreMigrateAtStart {
		return nil
	}
	if cfg.DatastoreType != "" && cfg.DatastoreType != "postgres" {
		return nil // skip if not using postgres
	}
	log.Info("Running migration", "name", m.Name())
	db, err := gorm.Open(postgres.Open(cfg.DBURL), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("migration: failed to connect: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	// Read and execute embedded schema
	if _, err := sqlDB.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("migration: failed to execute schema: %w", err)
	}
	log.Info("Postgres schema migration complete")
	return nil
}

// PostgresStore implements MemoryStore using GORM + PostgreSQL.
type PostgresStore struct {
	db           *gorm.DB
	cfg          *config.Config
	gcms         []cipher.AEAD
	entriesCache registrycache.MemoryEntriesCache
}

func (s *PostgresStore) encrypt(plaintext []byte) ([]byte, error) {
	if len(s.gcms) == 0 || plaintext == nil {
		return plaintext, nil
	}
	gcm := s.gcms[0]
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func (s *PostgresStore) decrypt(ciphertext []byte) ([]byte, error) {
	if len(s.gcms) == 0 || ciphertext == nil {
		return ciphertext, nil
	}
	var lastErr error
	for _, gcm := range s.gcms {
		nonceSize := gcm.NonceSize()
		if len(ciphertext) < nonceSize {
			lastErr = fmt.Errorf("ciphertext too short")
			continue
		}
		nonce, payload := ciphertext[:nonceSize], ciphertext[nonceSize:]
		plaintext, err := gcm.Open(nil, nonce, payload, nil)
		if err == nil {
			return plaintext, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm, nil
}

func (s *PostgresStore) decryptString(data []byte) string {
	plain, err := s.decrypt(data)
	if err != nil {
		return string(data) // fallback for unencrypted data
	}
	return string(plain)
}

// --- Conversations ---

func (s *PostgresStore) CreateConversation(ctx context.Context, userID string, title string, metadata map[string]interface{}, forkedAtConversationID *uuid.UUID, forkedAtEntryID *uuid.UUID) (*registrystore.ConversationDetail, error) {
	groupID := uuid.New()
	// For root (non-forked) conversations, use the same UUID for conversation and group
	// to match Java parity (features reference conversationGroupId in SQL against conversations.id).
	convID := groupID
	if forkedAtConversationID != nil {
		convID = uuid.New()
	}
	return s.createConversationWithID(ctx, userID, convID, title, metadata, forkedAtConversationID, forkedAtEntryID)
}

func (s *PostgresStore) CreateConversationWithID(ctx context.Context, userID string, convID uuid.UUID, title string, metadata map[string]interface{}, forkedAtConversationID *uuid.UUID, forkedAtEntryID *uuid.UUID) (*registrystore.ConversationDetail, error) {
	return s.createConversationWithID(ctx, userID, convID, title, metadata, forkedAtConversationID, forkedAtEntryID)
}

func (s *PostgresStore) createConversationWithID(ctx context.Context, userID string, convID uuid.UUID, title string, metadata map[string]interface{}, forkedAtConversationID *uuid.UUID, forkedAtEntryID *uuid.UUID) (*registrystore.ConversationDetail, error) {
	groupID := uuid.New()
	now := time.Now()

	if metadata == nil {
		metadata = map[string]interface{}{}
	}

	// If forking, look up the source conversation's group
	var actualGroupID uuid.UUID
	if forkedAtConversationID != nil {
		var sourceConv model.Conversation
		if err := s.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", *forkedAtConversationID).First(&sourceConv).Error; err != nil {
			return nil, &NotFoundError{Resource: "conversation", ID: forkedAtConversationID.String()}
		}
		// Verify user has access
		if _, err := s.requireAccess(ctx, userID, sourceConv.ConversationGroupID, model.AccessLevelReader); err != nil {
			return nil, err
		}
		// Validate fork point entry exists
		if forkedAtEntryID != nil {
			var entry model.Entry
			if err := s.db.WithContext(ctx).Where("id = ? AND conversation_group_id = ?", *forkedAtEntryID, sourceConv.ConversationGroupID).First(&entry).Error; err != nil {
				return nil, &NotFoundError{Resource: "entry", ID: forkedAtEntryID.String()}
			}
			// Java parity: forkedAtEntryId stored is the entry BEFORE the fork point.
			// Find the entry just before the requested fork point in the same conversation group.
			var prevEntry model.Entry
			result := s.db.WithContext(ctx).
				Where("conversation_group_id = ? AND created_at < ?", sourceConv.ConversationGroupID, entry.CreatedAt).
				Order("created_at DESC").
				Limit(1).
				Find(&prevEntry)
			if result.Error != nil {
				return nil, fmt.Errorf("failed to load previous fork entry: %w", result.Error)
			}
			if result.RowsAffected > 0 {
				prevID := prevEntry.ID
				forkedAtEntryID = &prevID
			}
			// else: no previous entry — fork is at the very first entry.
			// Keep the original entry ID as the stop point (it is the last
			// entry to include from the parent).
		}
		actualGroupID = sourceConv.ConversationGroupID
	} else {
		// New root conversation — create a group; for non-forked, use convID as groupID for Java parity
		actualGroupID = convID
		group := model.ConversationGroup{ID: actualGroupID, CreatedAt: now}
		if err := s.db.WithContext(ctx).Create(&group).Error; err != nil {
			return nil, fmt.Errorf("failed to create conversation group: %w", err)
		}
		_ = groupID // unused for root conversations when convID is specified
	}

	encTitle, err := s.encrypt([]byte(title))
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt title: %w", err)
	}
	conv := model.Conversation{
		ID:                     convID,
		Title:                  encTitle,
		OwnerUserID:            userID,
		Metadata:               metadata,
		ConversationGroupID:    actualGroupID,
		ForkedAtConversationID: forkedAtConversationID,
		ForkedAtEntryID:        forkedAtEntryID,
		CreatedAt:              now,
		UpdatedAt:              now,
	}

	if err := s.db.WithContext(ctx).Create(&conv).Error; err != nil {
		return nil, fmt.Errorf("failed to create conversation: %w", err)
	}

	// Create owner membership (only for root conversations)
	if forkedAtConversationID == nil {
		membership := model.ConversationMembership{
			ConversationGroupID: actualGroupID,
			UserID:              userID,
			AccessLevel:         model.AccessLevelOwner,
			CreatedAt:           now,
		}
		if err := s.db.WithContext(ctx).Create(&membership).Error; err != nil {
			return nil, fmt.Errorf("failed to create membership: %w", err)
		}
	}

	return &registrystore.ConversationDetail{
		ConversationSummary: registrystore.ConversationSummary{
			ID:                     convID,
			Title:                  title,
			OwnerUserID:            userID,
			Metadata:               metadata,
			ConversationGroupID:    actualGroupID,
			ForkedAtConversationID: forkedAtConversationID,
			ForkedAtEntryID:        forkedAtEntryID,
			CreatedAt:              now,
			UpdatedAt:              now,
			AccessLevel:            model.AccessLevelOwner,
		},
	}, nil
}

func (s *PostgresStore) ListConversations(ctx context.Context, userID string, query *string, afterCursor *string, limit int, mode model.ConversationListMode) ([]registrystore.ConversationSummary, *string, error) {
	requestedLimit := limit
	queryStr := ""
	if query != nil {
		queryStr = strings.TrimSpace(*query)
	}

	tx := s.db.WithContext(ctx).
		Table("conversations c").
		Select("c.id, c.title, c.owner_user_id, c.metadata, c.conversation_group_id, c.forked_at_entry_id, c.forked_at_conversation_id, c.created_at, c.updated_at, c.deleted_at, cm.access_level").
		Joins("JOIN conversation_memberships cm ON cm.conversation_group_id = c.conversation_group_id AND cm.user_id = ?", userID).
		Joins("JOIN conversation_groups cg ON cg.id = c.conversation_group_id AND cg.deleted_at IS NULL").
		Where("c.deleted_at IS NULL")

	switch mode {
	case model.ListModeRoots:
		tx = tx.Where("c.forked_at_conversation_id IS NULL")
	case model.ListModeLatestFork:
		tx = tx.Where("c.updated_at = (SELECT MAX(c2.updated_at) FROM conversations c2 WHERE c2.conversation_group_id = c.conversation_group_id AND c2.deleted_at IS NULL)")
	}

	if afterCursor != nil {
		tx = tx.Where("c.created_at > (SELECT created_at FROM conversations WHERE id = ?)", *afterCursor)
	}

	queryLimit := requestedLimit + 1
	if queryStr != "" {
		// Titles are encrypted at rest, so text filtering must happen post-decryption.
		// Over-fetch a bounded window to keep pagination reasonably useful.
		queryLimit = requestedLimit * 5
		if queryLimit < requestedLimit+1 {
			queryLimit = requestedLimit + 1
		}
		if queryLimit > 1000 {
			queryLimit = 1000
		}
	}

	tx = tx.Order("c.created_at ASC").Limit(queryLimit)

	type row struct {
		ID                     uuid.UUID              `gorm:"column:id"`
		Title                  []byte                 `gorm:"column:title"`
		OwnerUserID            string                 `gorm:"column:owner_user_id"`
		Metadata               map[string]interface{} `gorm:"column:metadata;serializer:json"`
		ConversationGroupID    uuid.UUID              `gorm:"column:conversation_group_id"`
		ForkedAtEntryID        *uuid.UUID             `gorm:"column:forked_at_entry_id"`
		ForkedAtConversationID *uuid.UUID             `gorm:"column:forked_at_conversation_id"`
		CreatedAt              time.Time              `gorm:"column:created_at"`
		UpdatedAt              time.Time              `gorm:"column:updated_at"`
		DeletedAt              *time.Time             `gorm:"column:deleted_at"`
		AccessLevel            model.AccessLevel      `gorm:"column:access_level"`
	}
	var rows []row
	if err := tx.Scan(&rows).Error; err != nil {
		return nil, nil, fmt.Errorf("failed to list conversations: %w", err)
	}

	if queryStr != "" {
		lq := strings.ToLower(queryStr)
		filtered := rows[:0]
		for _, r := range rows {
			if strings.Contains(strings.ToLower(s.decryptString(r.Title)), lq) {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}

	hasMore := len(rows) > requestedLimit
	if hasMore {
		rows = rows[:requestedLimit]
	}

	summaries := make([]registrystore.ConversationSummary, len(rows))
	for i, r := range rows {
		summaries[i] = registrystore.ConversationSummary{
			ID:                     r.ID,
			Title:                  s.decryptString(r.Title),
			OwnerUserID:            r.OwnerUserID,
			Metadata:               r.Metadata,
			ConversationGroupID:    r.ConversationGroupID,
			ForkedAtEntryID:        r.ForkedAtEntryID,
			ForkedAtConversationID: r.ForkedAtConversationID,
			CreatedAt:              r.CreatedAt,
			UpdatedAt:              r.UpdatedAt,
			DeletedAt:              r.DeletedAt,
			AccessLevel:            r.AccessLevel,
		}
	}

	var cursor *string
	if hasMore && len(summaries) > 0 {
		c := summaries[len(summaries)-1].ID.String()
		cursor = &c
	}
	return summaries, cursor, nil
}

func (s *PostgresStore) GetConversation(ctx context.Context, userID string, conversationID uuid.UUID) (*registrystore.ConversationDetail, error) {
	var conv model.Conversation
	if err := s.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", conversationID).First(&conv).Error; err != nil {
		return nil, &NotFoundError{Resource: "conversation", ID: conversationID.String()}
	}
	access, err := s.requireAccess(ctx, userID, conv.ConversationGroupID, model.AccessLevelReader)
	if err != nil {
		return nil, err
	}

	return &registrystore.ConversationDetail{
		ConversationSummary: registrystore.ConversationSummary{
			ID:                     conv.ID,
			Title:                  s.decryptString(conv.Title),
			OwnerUserID:            conv.OwnerUserID,
			Metadata:               conv.Metadata,
			ConversationGroupID:    conv.ConversationGroupID,
			ForkedAtConversationID: conv.ForkedAtConversationID,
			ForkedAtEntryID:        conv.ForkedAtEntryID,
			CreatedAt:              conv.CreatedAt,
			UpdatedAt:              conv.UpdatedAt,
			AccessLevel:            access,
		},
	}, nil
}

func (s *PostgresStore) UpdateConversation(ctx context.Context, userID string, conversationID uuid.UUID, title *string, metadata map[string]interface{}) (*registrystore.ConversationDetail, error) {
	var conv model.Conversation
	if err := s.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", conversationID).First(&conv).Error; err != nil {
		return nil, &NotFoundError{Resource: "conversation", ID: conversationID.String()}
	}
	if _, err := s.requireAccess(ctx, userID, conv.ConversationGroupID, model.AccessLevelWriter); err != nil {
		return nil, err
	}

	updates := map[string]interface{}{"updated_at": time.Now()}
	if title != nil {
		encTitle, err := s.encrypt([]byte(*title))
		if err != nil {
			return nil, fmt.Errorf("failed to encrypt title: %w", err)
		}
		updates["title"] = encTitle
	}
	if metadata != nil {
		updates["metadata"] = metadata
	}
	if err := s.db.WithContext(ctx).Model(&conv).Updates(updates).Error; err != nil {
		return nil, fmt.Errorf("failed to update conversation: %w", err)
	}
	return s.GetConversation(ctx, userID, conversationID)
}

func (s *PostgresStore) DeleteConversation(ctx context.Context, userID string, conversationID uuid.UUID) error {
	var conv model.Conversation
	if err := s.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", conversationID).First(&conv).Error; err != nil {
		return &NotFoundError{Resource: "conversation", ID: conversationID.String()}
	}
	if _, err := s.requireAccess(ctx, userID, conv.ConversationGroupID, model.AccessLevelOwner); err != nil {
		return err
	}

	now := time.Now()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Soft-delete the conversation group and all conversations in the fork tree.
		if err := tx.Model(&model.ConversationGroup{}).
			Where("id = ?", conv.ConversationGroupID).
			Update("deleted_at", now).Error; err != nil {
			return fmt.Errorf("failed to soft-delete group: %w", err)
		}
		if err := tx.Model(&model.Conversation{}).
			Where("conversation_group_id = ? AND deleted_at IS NULL", conv.ConversationGroupID).
			Update("deleted_at", now).Error; err != nil {
			return fmt.Errorf("failed to soft-delete conversations: %w", err)
		}

		// Java parity: memberships and entries are hard-deleted when a group is deleted.
		if err := tx.Where("conversation_group_id = ?", conv.ConversationGroupID).
			Delete(&model.ConversationMembership{}).Error; err != nil {
			return fmt.Errorf("failed to delete memberships: %w", err)
		}
		if err := tx.Where("conversation_group_id = ?", conv.ConversationGroupID).
			Delete(&model.Entry{}).Error; err != nil {
			return fmt.Errorf("failed to delete entries: %w", err)
		}
		if err := tx.Where("conversation_group_id = ?", conv.ConversationGroupID).
			Delete(&model.OwnershipTransfer{}).Error; err != nil {
			return fmt.Errorf("failed to delete ownership transfers: %w", err)
		}
		return nil
	})
}

// --- Memberships ---

func (s *PostgresStore) ListMemberships(ctx context.Context, userID string, conversationID uuid.UUID, afterCursor *string, limit int) ([]model.ConversationMembership, *string, error) {
	groupID, err := s.getGroupID(ctx, userID, conversationID, model.AccessLevelReader)
	if err != nil {
		return nil, nil, err
	}

	tx := s.db.WithContext(ctx).Where("conversation_group_id = ?", groupID).Order("created_at ASC")
	if afterCursor != nil {
		tx = tx.Where("created_at > (SELECT created_at FROM conversation_memberships WHERE conversation_group_id = ? AND user_id = ?)", groupID, *afterCursor)
	}
	tx = tx.Limit(limit + 1)

	var memberships []model.ConversationMembership
	if err := tx.Find(&memberships).Error; err != nil {
		return nil, nil, fmt.Errorf("failed to list memberships: %w", err)
	}

	hasMore := len(memberships) > limit
	if hasMore {
		memberships = memberships[:limit]
	}
	var cursor *string
	if hasMore && len(memberships) > 0 {
		c := memberships[len(memberships)-1].UserID
		cursor = &c
	}
	return memberships, cursor, nil
}

func (s *PostgresStore) ShareConversation(ctx context.Context, userID string, conversationID uuid.UUID, targetUserID string, accessLevel model.AccessLevel) (*model.ConversationMembership, error) {
	groupID, err := s.getGroupID(ctx, userID, conversationID, model.AccessLevelManager)
	if err != nil {
		return nil, err
	}
	if accessLevel == model.AccessLevelOwner {
		return nil, &ValidationError{Field: "accessLevel", Message: "cannot share with owner access; use ownership transfer"}
	}

	membership := model.ConversationMembership{
		ConversationGroupID: groupID,
		UserID:              targetUserID,
		AccessLevel:         accessLevel,
		CreatedAt:           time.Now(),
	}
	result := s.db.WithContext(ctx).Create(&membership)
	if result.Error != nil {
		if strings.Contains(result.Error.Error(), "duplicate key") {
			return nil, &ConflictError{Message: "user already has access to this conversation"}
		}
		return nil, fmt.Errorf("failed to share conversation: %w", result.Error)
	}
	return &membership, nil
}

func (s *PostgresStore) UpdateMembership(ctx context.Context, userID string, conversationID uuid.UUID, memberUserID string, accessLevel model.AccessLevel) (*model.ConversationMembership, error) {
	groupID, err := s.getGroupID(ctx, userID, conversationID, model.AccessLevelManager)
	if err != nil {
		return nil, err
	}
	if accessLevel == model.AccessLevelOwner {
		return nil, &ValidationError{Field: "accessLevel", Message: "cannot set owner access; use ownership transfer"}
	}

	result := s.db.WithContext(ctx).Model(&model.ConversationMembership{}).
		Where("conversation_group_id = ? AND user_id = ?", groupID, memberUserID).
		Update("access_level", accessLevel)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to update membership: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return nil, &NotFoundError{Resource: "membership", ID: memberUserID}
	}

	var m model.ConversationMembership
	result = s.db.WithContext(ctx).
		Where("conversation_group_id = ? AND user_id = ?", groupID, memberUserID).
		Limit(1).
		Find(&m)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to reload membership: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return nil, &NotFoundError{Resource: "membership", ID: memberUserID}
	}
	return &m, nil
}

func (s *PostgresStore) DeleteMembership(ctx context.Context, userID string, conversationID uuid.UUID, memberUserID string) error {
	groupID, err := s.getGroupID(ctx, userID, conversationID, model.AccessLevelManager)
	if err != nil {
		return err
	}
	// Cannot delete the owner
	var m model.ConversationMembership
	if err := s.db.WithContext(ctx).Where("conversation_group_id = ? AND user_id = ?", groupID, memberUserID).First(&m).Error; err != nil {
		return &NotFoundError{Resource: "membership", ID: memberUserID}
	}
	if m.AccessLevel == model.AccessLevelOwner {
		return &ValidationError{Field: "userId", Message: "cannot remove the owner"}
	}

	// Java parity: removing the pending transfer recipient cancels the transfer.
	s.db.WithContext(ctx).
		Where("conversation_group_id = ? AND to_user_id = ?", groupID, memberUserID).
		Delete(&model.OwnershipTransfer{})

	s.db.WithContext(ctx).Where("conversation_group_id = ? AND user_id = ?", groupID, memberUserID).Delete(&model.ConversationMembership{})
	return nil
}

// --- Forks ---

func (s *PostgresStore) ListForks(ctx context.Context, userID string, conversationID uuid.UUID, afterCursor *string, limit int) ([]registrystore.ConversationForkSummary, *string, error) {
	groupID, err := s.getGroupID(ctx, userID, conversationID, model.AccessLevelReader)
	if err != nil {
		return nil, nil, err
	}

	tx := s.db.WithContext(ctx).
		Table("conversations").
		Where("conversation_group_id = ? AND deleted_at IS NULL", groupID).
		Order("created_at ASC")

	if afterCursor != nil {
		tx = tx.Where("created_at > (SELECT created_at FROM conversations WHERE id = ?)", *afterCursor)
	}
	tx = tx.Limit(limit + 1)

	var convs []model.Conversation
	if err := tx.Find(&convs).Error; err != nil {
		return nil, nil, fmt.Errorf("failed to list forks: %w", err)
	}

	hasMore := len(convs) > limit
	if hasMore {
		convs = convs[:limit]
	}

	forks := make([]registrystore.ConversationForkSummary, len(convs))
	for i, c := range convs {
		forks[i] = registrystore.ConversationForkSummary{
			ID:                     c.ID,
			Title:                  s.decryptString(c.Title),
			ForkedAtEntryID:        c.ForkedAtEntryID,
			ForkedAtConversationID: c.ForkedAtConversationID,
			CreatedAt:              c.CreatedAt,
		}
	}

	var cursor *string
	if hasMore && len(forks) > 0 {
		c := forks[len(forks)-1].ID.String()
		cursor = &c
	}
	return forks, cursor, nil
}

// --- Ownership Transfers ---

func (s *PostgresStore) ListPendingTransfers(ctx context.Context, userID string, role string, afterCursor *string, limit int) ([]registrystore.OwnershipTransferDto, *string, error) {
	tx := s.db.WithContext(ctx).Table("conversation_ownership_transfers").Order("created_at ASC")

	switch role {
	case "sender":
		tx = tx.Where("from_user_id = ?", userID)
	case "recipient":
		tx = tx.Where("to_user_id = ?", userID)
	default:
		tx = tx.Where("from_user_id = ? OR to_user_id = ?", userID, userID)
	}

	if afterCursor != nil {
		tx = tx.Where("created_at > (SELECT created_at FROM conversation_ownership_transfers WHERE id = ?)", *afterCursor)
	}
	tx = tx.Limit(limit + 1)

	var transfers []model.OwnershipTransfer
	if err := tx.Find(&transfers).Error; err != nil {
		return nil, nil, fmt.Errorf("failed to list transfers: %w", err)
	}

	hasMore := len(transfers) > limit
	if hasMore {
		transfers = transfers[:limit]
	}

	dtos := make([]registrystore.OwnershipTransferDto, len(transfers))
	for i, t := range transfers {
		dtos[i] = registrystore.OwnershipTransferDto{
			ID:                  t.ID,
			ConversationGroupID: t.ConversationGroupID,
			ConversationID:      s.resolveConversationID(ctx, t.ConversationGroupID),
			FromUserID:          t.FromUserID,
			ToUserID:            t.ToUserID,
			CreatedAt:           t.CreatedAt,
		}
	}

	var cursor *string
	if hasMore && len(dtos) > 0 {
		c := dtos[len(dtos)-1].ID.String()
		cursor = &c
	}
	return dtos, cursor, nil
}

func (s *PostgresStore) GetTransfer(ctx context.Context, userID string, transferID uuid.UUID) (*registrystore.OwnershipTransferDto, error) {
	var t model.OwnershipTransfer
	if err := s.db.WithContext(ctx).Where("id = ?", transferID).First(&t).Error; err != nil {
		return nil, &NotFoundError{Resource: "transfer", ID: transferID.String()}
	}
	if t.FromUserID != userID && t.ToUserID != userID {
		return nil, &NotFoundError{Resource: "transfer", ID: transferID.String()}
	}
	return &registrystore.OwnershipTransferDto{
		ID:                  t.ID,
		ConversationGroupID: t.ConversationGroupID,
		ConversationID:      s.resolveConversationID(ctx, t.ConversationGroupID),
		FromUserID:          t.FromUserID,
		ToUserID:            t.ToUserID,
		CreatedAt:           t.CreatedAt,
	}, nil
}

// resolveConversationID finds the primary (non-deleted) conversation ID for a group.
func (s *PostgresStore) resolveConversationID(ctx context.Context, groupID uuid.UUID) uuid.UUID {
	var conv model.Conversation
	if err := s.db.WithContext(ctx).Where("conversation_group_id = ? AND deleted_at IS NULL", groupID).First(&conv).Error; err != nil {
		return uuid.Nil
	}
	return conv.ID
}

func (s *PostgresStore) CreateOwnershipTransfer(ctx context.Context, userID string, conversationID uuid.UUID, toUserID string) (*registrystore.OwnershipTransferDto, error) {
	var conv model.Conversation
	if err := s.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", conversationID).First(&conv).Error; err != nil {
		return nil, &NotFoundError{Resource: "conversation", ID: conversationID.String()}
	}
	if _, err := s.requireAccess(ctx, userID, conv.ConversationGroupID, model.AccessLevelOwner); err != nil {
		return nil, err
	}
	if userID == toUserID {
		return nil, &ValidationError{Field: "newOwnerUserId", Message: "cannot transfer to yourself"}
	}
	// Parity with Java behavior: recipient must already be a conversation member.
	var recipient model.ConversationMembership
	if err := s.db.WithContext(ctx).
		Where("conversation_group_id = ? AND user_id = ?", conv.ConversationGroupID, toUserID).
		First(&recipient).Error; err != nil {
		return nil, &ValidationError{Field: "newOwnerUserId", Message: "recipient must already be a member"}
	}

	transfer := model.OwnershipTransfer{
		ID:                  uuid.New(),
		ConversationGroupID: conv.ConversationGroupID,
		FromUserID:          userID,
		ToUserID:            toUserID,
		CreatedAt:           time.Now(),
	}
	if err := s.db.WithContext(ctx).Create(&transfer).Error; err != nil {
		if strings.Contains(err.Error(), "unique_transfer_per_conversation") {
			// Look up the existing transfer ID for the conflict response.
			var existing model.OwnershipTransfer
			findResult := s.db.WithContext(ctx).
				Where("conversation_group_id = ?", conv.ConversationGroupID).
				Limit(1).
				Find(&existing)
			if findResult.Error == nil && findResult.RowsAffected > 0 {
				return nil, &ConflictError{
					Message: "a transfer is already pending for this conversation",
					Code:    "TRANSFER_ALREADY_PENDING",
					Details: map[string]interface{}{"existingTransferId": existing.ID.String()},
				}
			}
			return nil, &ConflictError{Message: "a transfer is already pending for this conversation", Code: "TRANSFER_ALREADY_PENDING"}
		}
		return nil, fmt.Errorf("failed to create transfer: %w", err)
	}
	return &registrystore.OwnershipTransferDto{
		ID:                  transfer.ID,
		ConversationGroupID: transfer.ConversationGroupID,
		ConversationID:      conversationID,
		FromUserID:          transfer.FromUserID,
		ToUserID:            transfer.ToUserID,
		CreatedAt:           transfer.CreatedAt,
	}, nil
}

func (s *PostgresStore) AcceptTransfer(ctx context.Context, userID string, transferID uuid.UUID) error {
	var t model.OwnershipTransfer
	if err := s.db.WithContext(ctx).Where("id = ?", transferID).First(&t).Error; err != nil {
		return &NotFoundError{Resource: "transfer", ID: transferID.String()}
	}
	if t.ToUserID != userID {
		return &ForbiddenError{}
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Update old owner membership to manager
		tx.Model(&model.ConversationMembership{}).
			Where("conversation_group_id = ? AND user_id = ?", t.ConversationGroupID, t.FromUserID).
			Update("access_level", model.AccessLevelManager)

		// Upsert new owner membership
		var existing model.ConversationMembership
		existingResult := tx.
			Where("conversation_group_id = ? AND user_id = ?", t.ConversationGroupID, t.ToUserID).
			Limit(1).
			Find(&existing)
		if existingResult.Error != nil {
			return fmt.Errorf("failed to load recipient membership: %w", existingResult.Error)
		}
		if existingResult.RowsAffected == 0 {
			// Create new
			tx.Create(&model.ConversationMembership{
				ConversationGroupID: t.ConversationGroupID,
				UserID:              t.ToUserID,
				AccessLevel:         model.AccessLevelOwner,
				CreatedAt:           time.Now(),
			})
		} else {
			tx.Model(&existing).Update("access_level", model.AccessLevelOwner)
		}

		// Update conversation owner
		tx.Model(&model.Conversation{}).
			Where("conversation_group_id = ? AND deleted_at IS NULL", t.ConversationGroupID).
			Update("owner_user_id", t.ToUserID)

		// Delete the transfer record
		tx.Where("id = ?", transferID).Delete(&model.OwnershipTransfer{})
		return nil
	})
}

func (s *PostgresStore) DeleteTransfer(ctx context.Context, userID string, transferID uuid.UUID) error {
	var t model.OwnershipTransfer
	if err := s.db.WithContext(ctx).Where("id = ?", transferID).First(&t).Error; err != nil {
		return &NotFoundError{Resource: "transfer", ID: transferID.String()}
	}
	if t.FromUserID != userID && t.ToUserID != userID {
		return &ForbiddenError{}
	}
	s.db.WithContext(ctx).Where("id = ?", transferID).Delete(&model.OwnershipTransfer{})
	return nil
}

// --- Entries ---

func (s *PostgresStore) GetEntries(ctx context.Context, userID string, conversationID uuid.UUID, afterEntryID *string, limit int, channel *model.Channel, epochFilter *registrystore.MemoryEpochFilter, clientID *string, allForks bool) (*registrystore.PagedEntries, error) {
	var conv model.Conversation
	result := s.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", conversationID).Limit(1).Find(&conv)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, &NotFoundError{Resource: "conversation", ID: conversationID.String()}
	}
	if _, err := s.requireAccess(ctx, userID, conv.ConversationGroupID, model.AccessLevelReader); err != nil {
		return nil, err
	}

	if limit <= 0 {
		limit = 50
	}

	// channel==nil means "all channels" (agent without filter).
	// Determine effective channel for filtering.
	var effectiveChannel model.Channel
	if channel != nil {
		effectiveChannel = *channel
	}

	if effectiveChannel == model.ChannelMemory && clientID == nil {
		return nil, &ForbiddenError{}
	}

	if allForks {
		entries, err := s.listEntriesForGroup(ctx, conv.ConversationGroupID)
		if err != nil {
			return nil, err
		}
		entries = filterEntriesForAllForks(entries, effectiveChannel, clientID, epochFilter)
		entries, cursor := paginateEntries(entries, afterEntryID, limit)
		decryptEntries(s, entries)
		return &registrystore.PagedEntries{Data: entries, AfterCursor: cursor}, nil
	}

	ancestry, err := s.buildAncestryStack(ctx, conv)
	if err != nil {
		return nil, err
	}

	var filtered []model.Entry
	if effectiveChannel == model.ChannelMemory {
		// Memory-only: filter memory entries by epoch/clientID.
		// Use the cache for the common latest-epoch case.
		if epochFilter == nil || epochFilter.Mode == registrystore.MemoryEpochModeLatest {
			filtered, err = s.fetchLatestMemoryEntries(ctx, conv, ancestry, *clientID)
			if err != nil {
				return nil, err
			}
		} else {
			allEntries, err := s.listEntriesForGroup(ctx, conv.ConversationGroupID)
			if err != nil {
				return nil, err
			}
			filtered = filterMemoryEntriesWithEpoch(allEntries, ancestry, *clientID, epochFilter)
		}
	} else {
		allEntries, err := s.listEntriesForGroup(ctx, conv.ConversationGroupID)
		if err != nil {
			return nil, err
		}
		if effectiveChannel == "" && clientID != nil {
			// All channels (agent without filter): return all entries in ancestry order.
			filtered = filterEntriesByAncestry(allEntries, ancestry)
		} else {
			// Single channel filter (or default history).
			filtered = filterEntriesByAncestry(allEntries, ancestry)
			if effectiveChannel != "" {
				tmp := filtered[:0]
				for _, entry := range filtered {
					if entry.Channel == effectiveChannel {
						tmp = append(tmp, entry)
					}
				}
				filtered = tmp
			}
		}
	}

	filtered, cursor := paginateEntries(filtered, afterEntryID, limit)
	decryptEntries(s, filtered)
	return &registrystore.PagedEntries{Data: filtered, AfterCursor: cursor}, nil
}

func (s *PostgresStore) GetEntryGroupID(ctx context.Context, entryID uuid.UUID) (uuid.UUID, error) {
	var entry model.Entry
	result := s.db.WithContext(ctx).Select("conversation_group_id").Where("id = ?", entryID).Limit(1).Find(&entry)
	if result.Error != nil {
		return uuid.Nil, result.Error
	}
	if result.RowsAffected == 0 {
		return uuid.Nil, &NotFoundError{Resource: "entry", ID: entryID.String()}
	}
	return entry.ConversationGroupID, nil
}

func (s *PostgresStore) AppendEntries(ctx context.Context, userID string, conversationID uuid.UUID, entries []registrystore.CreateEntryRequest, clientID *string, epoch *int64) ([]model.Entry, error) {
	var conv model.Conversation
	convResult := s.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", conversationID).Limit(1).Find(&conv)
	if convResult.Error != nil {
		return nil, convResult.Error
	}
	if convResult.RowsAffected == 0 {
		// Auto-create conversation if it doesn't exist (Java parity).
		// Check first entry for fork metadata.
		var forkedAtConvID *uuid.UUID
		var forkedAtEntryID *uuid.UUID
		if len(entries) > 0 {
			forkedAtConvID = entries[0].ForkedAtConversationID
			forkedAtEntryID = entries[0].ForkedAtEntryID
		}

		title := inferTitleFromEntries(entries)
		detail, err := s.createConversationWithID(ctx, userID, conversationID, title, nil, forkedAtConvID, forkedAtEntryID)
		if err != nil {
			return nil, err
		}
		encTitle, err := s.encrypt([]byte(detail.Title))
		if err != nil {
			return nil, fmt.Errorf("failed to encrypt title: %w", err)
		}
		conv = model.Conversation{
			ID:                  detail.ID,
			ConversationGroupID: detail.ConversationGroupID,
			OwnerUserID:         detail.OwnerUserID,
			Title:               encTitle,
			CreatedAt:           detail.CreatedAt,
			UpdatedAt:           detail.UpdatedAt,
		}
	}
	if _, err := s.requireAccess(ctx, userID, conv.ConversationGroupID, model.AccessLevelWriter); err != nil {
		return nil, err
	}

	now := time.Now()
	result := make([]model.Entry, len(entries))
	for i, req := range entries {
		ch := model.Channel(strings.ToLower(req.Channel))
		if ch == "" {
			ch = model.ChannelHistory
		}

		// Auto-assign epoch=1 for memory entries when no epoch specified.
		entryEpoch := epoch
		if ch == model.ChannelMemory && entryEpoch == nil {
			var one int64 = 1
			entryEpoch = &one
		}

		encContent, err := s.encrypt(req.Content)
		if err != nil {
			return nil, fmt.Errorf("failed to encrypt entry content: %w", err)
		}
		entry := model.Entry{
			ID:                  uuid.New(),
			ConversationID:      conversationID,
			ConversationGroupID: conv.ConversationGroupID,
			UserID:              &userID,
			ClientID:            clientID,
			Channel:             ch,
			Epoch:               entryEpoch,
			ContentType:         req.ContentType,
			Content:             encContent,
			IndexedContent:      req.IndexedContent,
			CreatedAt:           now,
		}
		if err := s.db.WithContext(ctx).Create(&entry).Error; err != nil {
			return nil, fmt.Errorf("failed to append entry: %w", err)
		}
		entry.Content = req.Content // return unencrypted
		result[i] = entry
	}

	// Derive conversation title from first history entry if title is empty.
	if len(conv.Title) == 0 {
		for _, e := range result {
			if e.Channel == model.ChannelHistory {
				title := deriveTitleFromContent(string(e.Content))
				if title != "" {
					s.db.WithContext(ctx).Model(&model.Conversation{}).Where("id = ?", conversationID).Update("title", title)
				}
				break
			}
		}
	}

	// Update conversation timestamp
	s.db.WithContext(ctx).Model(&model.Conversation{}).Where("id = ?", conversationID).Update("updated_at", now)

	// Keep memory latest-epoch cache warm after memory appends.
	if clientID != nil {
		for _, e := range result {
			if e.Channel == model.ChannelMemory {
				if ancestry, err := s.buildAncestryStack(ctx, conv); err == nil {
					s.warmEntriesCache(ctx, conv, ancestry, *clientID)
				}
				break
			}
		}
	}

	return result, nil
}

// inferTitleFromEntries derives a title from the first history entry in the list.
func inferTitleFromEntries(entries []registrystore.CreateEntryRequest) string {
	for _, e := range entries {
		ch := strings.ToLower(e.Channel)
		if ch == "" || ch == string(model.ChannelHistory) {
			title := deriveTitleFromContent(string(e.Content))
			if title != "" {
				return title
			}
		}
	}
	return ""
}

// deriveTitleFromContent extracts text from the first content object and truncates to 40 chars.
func deriveTitleFromContent(content string) string {
	// Try parsing as JSON array
	var arr []map[string]any
	if err := json.Unmarshal([]byte(content), &arr); err == nil && len(arr) > 0 {
		if text, ok := arr[0]["text"].(string); ok && text != "" {
			if len(text) > 40 {
				return text[:40]
			}
			return text
		}
	}
	return ""
}

func (s *PostgresStore) SyncAgentEntry(ctx context.Context, userID string, conversationID uuid.UUID, entry registrystore.CreateEntryRequest, clientID string) (*registrystore.SyncResult, error) {
	incomingContent := parseContentArray(entry.Content)

	autoCreated := false
	var conv model.Conversation
	result := s.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", conversationID).Limit(1).Find(&conv)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		// Auto-create conversation if it does not exist and content is non-empty.
		if len(incomingContent) == 0 {
			return &registrystore.SyncResult{NoOp: true}, nil
		}
		var err error
		conv, err = s.autoCreateConversation(ctx, userID, conversationID)
		if err != nil {
			return nil, err
		}
		autoCreated = true
	}
	if _, err := s.requireAccess(ctx, userID, conv.ConversationGroupID, model.AccessLevelWriter); err != nil {
		return nil, err
	}

	ancestry, err := s.buildAncestryStack(ctx, conv)
	if err != nil {
		return nil, err
	}
	latestEpochEntries, err := s.fetchLatestMemoryEntries(ctx, conv, ancestry, clientID)
	if err != nil {
		return nil, err
	}

	existingContent := flattenMemoryContent(s, latestEpochEntries)

	// Compute the current latest epoch value.
	var latestEpoch *int64
	for _, existing := range latestEpochEntries {
		if existing.Epoch == nil {
			continue
		}
		if latestEpoch == nil || *existing.Epoch > *latestEpoch {
			v := *existing.Epoch
			latestEpoch = &v
		}
	}

	// Empty incoming content on empty existing = no-op.
	if len(incomingContent) == 0 && len(existingContent) == 0 {
		return &registrystore.SyncResult{NoOp: true, Epoch: latestEpoch}, nil
	}

	// No-op when incoming matches existing exactly.
	if reflect.DeepEqual(existingContent, incomingContent) {
		return &registrystore.SyncResult{NoOp: true, Epoch: latestEpoch}, nil
	}

	appendContent := entry.Content
	var epochToUse int64
	epochIncremented := false
	if latestEpoch != nil {
		epochToUse = *latestEpoch
	} else {
		epochToUse = 1
		// Only mark epoch as incremented when the conversation was auto-created.
		// For existing conversations, the first sync at epoch 1 is not an "increment".
		epochIncremented = autoCreated
	}

	if len(incomingContent) == 0 {
		// Empty sync clears memory: create new epoch with empty content.
		if latestEpoch != nil {
			epochToUse = *latestEpoch + 1
		}
		epochIncremented = true
		appendContent = json.RawMessage("[]")
	} else if isPrefixContent(existingContent, incomingContent) {
		delta := incomingContent[len(existingContent):]
		if len(delta) == 0 {
			return &registrystore.SyncResult{NoOp: true, Epoch: latestEpoch}, nil
		}
		appendContent = marshalContentArray(delta)
	} else {
		// Divergence from latest epoch: start a new epoch with the full incoming content.
		if latestEpoch != nil {
			epochToUse = *latestEpoch + 1
			epochIncremented = true
		}
		appendContent = marshalContentArray(incomingContent)
	}

	now := time.Now()
	encContent, err := s.encrypt(appendContent)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt entry content: %w", err)
	}
	newEntry := model.Entry{
		ID:                  uuid.New(),
		ConversationID:      conversationID,
		ConversationGroupID: conv.ConversationGroupID,
		UserID:              &userID,
		ClientID:            &clientID,
		Channel:             model.ChannelMemory,
		Epoch:               &epochToUse,
		ContentType:         entry.ContentType,
		Content:             encContent,
		IndexedContent:      entry.IndexedContent,
		CreatedAt:           now,
	}
	if err := s.db.WithContext(ctx).Create(&newEntry).Error; err != nil {
		return nil, fmt.Errorf("failed to sync entry: %w", err)
	}
	newEntry.Content = appendContent
	s.warmEntriesCache(ctx, conv, ancestry, clientID)
	return &registrystore.SyncResult{Entry: &newEntry, Epoch: &epochToUse, NoOp: false, EpochIncremented: epochIncremented}, nil
}

// autoCreateConversation creates a conversation with a given ID for sync auto-creation.
func (s *PostgresStore) autoCreateConversation(ctx context.Context, userID string, conversationID uuid.UUID) (model.Conversation, error) {
	now := time.Now()
	groupID := uuid.New()

	group := model.ConversationGroup{
		ID:        groupID,
		CreatedAt: now,
	}
	if err := s.db.WithContext(ctx).Create(&group).Error; err != nil {
		return model.Conversation{}, fmt.Errorf("failed to create conversation group: %w", err)
	}

	conv := model.Conversation{
		ID:                  conversationID,
		ConversationGroupID: groupID,
		OwnerUserID:         userID,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if err := s.db.WithContext(ctx).Create(&conv).Error; err != nil {
		return model.Conversation{}, fmt.Errorf("failed to create conversation: %w", err)
	}

	membership := model.ConversationMembership{
		ConversationGroupID: groupID,
		UserID:              userID,
		AccessLevel:         model.AccessLevelOwner,
		CreatedAt:           now,
	}
	if err := s.db.WithContext(ctx).Create(&membership).Error; err != nil {
		return model.Conversation{}, fmt.Errorf("failed to create membership: %w", err)
	}

	return conv, nil
}

// --- Indexing ---

func (s *PostgresStore) IndexEntries(ctx context.Context, entries []registrystore.IndexEntryRequest) (*registrystore.IndexConversationsResponse, error) {
	count := 0
	for _, req := range entries {
		result := s.db.WithContext(ctx).Exec(
			"UPDATE entries SET indexed_content = ? WHERE id = ? AND conversation_group_id = (SELECT conversation_group_id FROM conversations WHERE id = ?)",
			req.IndexedContent, req.EntryID, req.ConversationID,
		)
		if result.Error != nil {
			log.Error("Failed to index entry", "err", result.Error, "entryId", req.EntryID)
			continue
		}
		if result.RowsAffected == 0 {
			return nil, &registrystore.NotFoundError{Resource: "entry", ID: req.EntryID.String()}
		}
		count++
	}
	return &registrystore.IndexConversationsResponse{Indexed: count}, nil
}

func (s *PostgresStore) ListUnindexedEntries(ctx context.Context, limit int, afterCursor *string) ([]model.Entry, *string, error) {
	tx := s.db.WithContext(ctx).
		Where("channel = ? AND indexed_content IS NULL", model.ChannelHistory).
		Order("created_at ASC").
		Limit(limit + 1)

	if afterCursor != nil {
		tx = tx.Where("created_at > (SELECT MAX(e.created_at) FROM entries e WHERE e.id::text = ?)", *afterCursor)
	}

	var entries []model.Entry
	if err := tx.Find(&entries).Error; err != nil {
		return nil, nil, fmt.Errorf("failed to list unindexed entries: %w", err)
	}

	hasMore := len(entries) > limit
	if hasMore {
		entries = entries[:limit]
	}

	// Decrypt
	for i := range entries {
		if decrypted, err := s.decrypt(entries[i].Content); err == nil {
			entries[i].Content = decrypted
		}
	}

	var cursor *string
	if hasMore && len(entries) > 0 {
		c := entries[len(entries)-1].ID.String()
		cursor = &c
	}
	return entries, cursor, nil
}

func (s *PostgresStore) FindEntriesPendingVectorIndexing(ctx context.Context, limit int) ([]model.Entry, error) {
	var entries []model.Entry
	err := s.db.WithContext(ctx).
		Where("indexed_content IS NOT NULL AND indexed_at IS NULL").
		Order("created_at ASC").
		Limit(limit).
		Find(&entries).Error
	if err != nil {
		return nil, fmt.Errorf("failed to find entries pending vector indexing: %w", err)
	}
	for i := range entries {
		if decrypted, err := s.decrypt(entries[i].Content); err == nil {
			entries[i].Content = decrypted
		}
	}
	return entries, nil
}

func (s *PostgresStore) SetIndexedAt(ctx context.Context, entryID uuid.UUID, conversationGroupID uuid.UUID, indexedAt time.Time) error {
	result := s.db.WithContext(ctx).Exec(
		"UPDATE entries SET indexed_at = ? WHERE id = ? AND conversation_group_id = ?",
		indexedAt, entryID, conversationGroupID,
	)
	return result.Error
}

// --- Search ---

func (s *PostgresStore) ListConversationGroupIDs(ctx context.Context, userID string) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := s.db.WithContext(ctx).
		Model(&model.ConversationMembership{}).
		Distinct("conversation_group_id").
		Where("user_id = ?", userID).
		Pluck("conversation_group_id", &ids).Error
	return ids, err
}

func (s *PostgresStore) FetchSearchResultDetails(ctx context.Context, userID string, entryIDs []uuid.UUID, includeEntry bool) ([]registrystore.SearchResult, error) {
	if len(entryIDs) == 0 {
		return nil, nil
	}
	type row struct {
		EntryID           uuid.UUID `gorm:"column:entry_id"`
		ConversationID    uuid.UUID `gorm:"column:conversation_id"`
		ConversationTitle []byte    `gorm:"column:conversation_title"`
		IndexedContent    string    `gorm:"column:indexed_content"`
	}
	var rows []row
	err := s.db.WithContext(ctx).Raw(`
		SELECT e.id as entry_id, e.conversation_id, c.title as conversation_title, e.indexed_content
		FROM entries e
		JOIN conversations c ON c.id = e.conversation_id AND c.deleted_at IS NULL
		JOIN conversation_memberships cm ON cm.conversation_group_id = c.conversation_group_id AND cm.user_id = ?
		WHERE e.id IN ?
	`, userID, entryIDs).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("fetch search result details failed: %w", err)
	}
	results := make([]registrystore.SearchResult, len(rows))
	for i, r := range rows {
		title := s.decryptString(r.ConversationTitle)
		highlight := r.IndexedContent
		if len(highlight) > 200 {
			highlight = highlight[:200] + "..."
		}
		results[i] = registrystore.SearchResult{
			EntryID:           r.EntryID,
			ConversationID:    r.ConversationID,
			ConversationTitle: &title,
			Highlights:        &highlight,
		}
	}
	return results, nil
}

// toPrefixTsQuery converts a plain text query to a PostgreSQL tsquery with prefix matching.
// e.g. "Jav script" becomes "Jav:* & script:*"
func toPrefixTsQuery(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return ""
	}
	words := strings.Fields(query)
	parts := make([]string, 0, len(words))
	for _, word := range words {
		escaped := escapeTsQueryWord(word)
		if escaped != "" {
			parts = append(parts, escaped+":*")
		}
	}
	return strings.Join(parts, " & ")
}

// escapeTsQueryWord removes characters that have special meaning in tsquery syntax.
func escapeTsQueryWord(word string) string {
	var b strings.Builder
	for _, r := range word {
		switch r {
		case '&', '|', '!', '(', ')', ':', '\'', '\\', '*':
			// skip tsquery special characters
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (s *PostgresStore) SearchEntries(ctx context.Context, userID string, query string, limit int, includeEntry bool) (*registrystore.SearchResults, error) {
	prefixQuery := toPrefixTsQuery(query)
	if prefixQuery == "" {
		return &registrystore.SearchResults{Data: []registrystore.SearchResult{}}, nil
	}
	// Full-text search using tsvector with prefix matching. Request limit+1 for pagination.
	sql := `
		SELECT e.id as entry_id, e.conversation_id, e.conversation_group_id, c.title as conversation_title,
		       ts_rank(e.indexed_content_tsv, to_tsquery('english', ?)) as score,
		       ts_headline('english', e.indexed_content, to_tsquery('english', ?),
		           'StartSel=**, StopSel=**, MaxWords=50, MinWords=20') as highlight
		FROM entries e
		JOIN conversations c ON c.id = e.conversation_id AND c.conversation_group_id = e.conversation_group_id AND c.deleted_at IS NULL
		JOIN conversation_memberships cm ON cm.conversation_group_id = c.conversation_group_id AND cm.user_id = ?
		WHERE e.indexed_content_tsv @@ to_tsquery('english', ?)
		ORDER BY score DESC
		LIMIT ?
	`
	type searchRow struct {
		EntryID             uuid.UUID `gorm:"column:entry_id"`
		ConversationID      uuid.UUID `gorm:"column:conversation_id"`
		ConversationGroupID uuid.UUID `gorm:"column:conversation_group_id"`
		ConversationTitle   []byte    `gorm:"column:conversation_title"`
		Score               float64   `gorm:"column:score"`
		Highlight           string    `gorm:"column:highlight"`
	}
	var rows []searchRow
	if err := s.db.WithContext(ctx).Raw(sql, prefixQuery, prefixQuery, userID, prefixQuery, limit+1).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("full-text search failed: %w", err)
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	results := make([]registrystore.SearchResult, len(rows))
	for i, r := range rows {
		highlight := r.Highlight
		results[i] = registrystore.SearchResult{
			EntryID:        r.EntryID,
			ConversationID: r.ConversationID,
			Score:          r.Score,
			Kind:           "postgres",
			Highlights:     &highlight,
		}
		if len(r.ConversationTitle) > 0 {
			title := s.decryptString(r.ConversationTitle)
			results[i].ConversationTitle = &title
		}
		if includeEntry {
			var entry model.Entry
			result := s.db.WithContext(ctx).
				Where("id = ? AND conversation_group_id = ?", r.EntryID, r.ConversationGroupID).
				Limit(1).
				Find(&entry)
			if result.Error == nil && result.RowsAffected > 0 {
				if decrypted, err := s.decrypt(entry.Content); err == nil {
					entry.Content = decrypted
				}
				results[i].Entry = &entry
			}
		}
	}

	var cursor *string
	if hasMore && len(results) > 0 {
		c := results[len(results)-1].EntryID.String()
		cursor = &c
	}
	return &registrystore.SearchResults{Data: results, AfterCursor: cursor}, nil
}

// --- Admin ---

func (s *PostgresStore) AdminListConversations(ctx context.Context, query registrystore.AdminConversationQuery) ([]registrystore.ConversationSummary, *string, error) {
	tx := s.db.WithContext(ctx).
		Table("conversations c").
		Select("c.id, c.title, c.owner_user_id, c.metadata, c.conversation_group_id, c.forked_at_entry_id, c.forked_at_conversation_id, c.created_at, c.updated_at, c.deleted_at, 'owner' as access_level")

	if !query.IncludeDeleted && !query.OnlyDeleted {
		tx = tx.Where("c.deleted_at IS NULL")
	}
	if query.OnlyDeleted {
		tx = tx.Where("c.deleted_at IS NOT NULL")
	}
	if query.UserID != nil {
		tx = tx.Where("c.owner_user_id = ?", *query.UserID)
	}
	if query.DeletedAfter != nil {
		tx = tx.Where("c.deleted_at >= ?", *query.DeletedAfter)
	}
	if query.DeletedBefore != nil {
		tx = tx.Where("c.deleted_at < ?", *query.DeletedBefore)
	}

	switch query.Mode {
	case model.ListModeRoots:
		tx = tx.Where("c.forked_at_conversation_id IS NULL")
	case model.ListModeLatestFork:
		tx = tx.Where("c.updated_at = (SELECT MAX(c2.updated_at) FROM conversations c2 WHERE c2.conversation_group_id = c.conversation_group_id)")
	}

	if query.AfterCursor != nil {
		tx = tx.Where("c.created_at > (SELECT created_at FROM conversations WHERE id = ?)", *query.AfterCursor)
	}
	tx = tx.Order("c.created_at ASC").Limit(query.Limit + 1)

	type row struct {
		ID                     uuid.UUID              `gorm:"column:id"`
		Title                  []byte                 `gorm:"column:title"`
		OwnerUserID            string                 `gorm:"column:owner_user_id"`
		Metadata               map[string]interface{} `gorm:"column:metadata;serializer:json"`
		ConversationGroupID    uuid.UUID              `gorm:"column:conversation_group_id"`
		ForkedAtEntryID        *uuid.UUID             `gorm:"column:forked_at_entry_id"`
		ForkedAtConversationID *uuid.UUID             `gorm:"column:forked_at_conversation_id"`
		CreatedAt              time.Time              `gorm:"column:created_at"`
		UpdatedAt              time.Time              `gorm:"column:updated_at"`
		DeletedAt              *time.Time             `gorm:"column:deleted_at"`
		AccessLevel            model.AccessLevel      `gorm:"column:access_level"`
	}
	var rows []row
	if err := tx.Scan(&rows).Error; err != nil {
		return nil, nil, fmt.Errorf("failed to admin list conversations: %w", err)
	}

	hasMore := len(rows) > query.Limit
	if hasMore {
		rows = rows[:query.Limit]
	}

	summaries := make([]registrystore.ConversationSummary, len(rows))
	for i, r := range rows {
		summaries[i] = registrystore.ConversationSummary{
			ID:                     r.ID,
			Title:                  s.decryptString(r.Title),
			OwnerUserID:            r.OwnerUserID,
			Metadata:               r.Metadata,
			ConversationGroupID:    r.ConversationGroupID,
			ForkedAtEntryID:        r.ForkedAtEntryID,
			ForkedAtConversationID: r.ForkedAtConversationID,
			CreatedAt:              r.CreatedAt,
			UpdatedAt:              r.UpdatedAt,
			DeletedAt:              r.DeletedAt,
			AccessLevel:            r.AccessLevel,
		}
	}

	var cursor *string
	if hasMore && len(summaries) > 0 {
		c := summaries[len(summaries)-1].ID.String()
		cursor = &c
	}
	return summaries, cursor, nil
}

func (s *PostgresStore) AdminGetConversation(ctx context.Context, conversationID uuid.UUID) (*registrystore.ConversationDetail, error) {
	var conv model.Conversation
	if err := s.db.WithContext(ctx).Where("id = ?", conversationID).First(&conv).Error; err != nil {
		return nil, &NotFoundError{Resource: "conversation", ID: conversationID.String()}
	}
	return &registrystore.ConversationDetail{
		ConversationSummary: registrystore.ConversationSummary{
			ID:                     conv.ID,
			Title:                  s.decryptString(conv.Title),
			OwnerUserID:            conv.OwnerUserID,
			Metadata:               conv.Metadata,
			ConversationGroupID:    conv.ConversationGroupID,
			ForkedAtConversationID: conv.ForkedAtConversationID,
			ForkedAtEntryID:        conv.ForkedAtEntryID,
			CreatedAt:              conv.CreatedAt,
			UpdatedAt:              conv.UpdatedAt,
			DeletedAt:              conv.DeletedAt,
			AccessLevel:            model.AccessLevelOwner,
		},
	}, nil
}

func (s *PostgresStore) AdminDeleteConversation(ctx context.Context, conversationID uuid.UUID) error {
	var conv model.Conversation
	if err := s.db.WithContext(ctx).Where("id = ?", conversationID).First(&conv).Error; err != nil {
		return &NotFoundError{Resource: "conversation", ID: conversationID.String()}
	}
	now := time.Now()
	s.db.WithContext(ctx).Model(&model.ConversationGroup{}).Where("id = ?", conv.ConversationGroupID).Update("deleted_at", now)
	s.db.WithContext(ctx).Model(&model.Conversation{}).Where("conversation_group_id = ? AND deleted_at IS NULL", conv.ConversationGroupID).Update("deleted_at", now)
	return nil
}

func (s *PostgresStore) AdminRestoreConversation(ctx context.Context, conversationID uuid.UUID) error {
	var conv model.Conversation
	if err := s.db.WithContext(ctx).Where("id = ?", conversationID).First(&conv).Error; err != nil {
		return &NotFoundError{Resource: "conversation", ID: conversationID.String()}
	}
	if conv.DeletedAt == nil {
		return &ConflictError{Message: "conversation is not deleted"}
	}
	s.db.WithContext(ctx).Model(&model.ConversationGroup{}).Where("id = ?", conv.ConversationGroupID).Update("deleted_at", nil)
	s.db.WithContext(ctx).Model(&model.Conversation{}).Where("conversation_group_id = ?", conv.ConversationGroupID).Update("deleted_at", nil)
	return nil
}

func (s *PostgresStore) AdminGetEntries(ctx context.Context, conversationID uuid.UUID, query registrystore.AdminMessageQuery) (*registrystore.PagedEntries, error) {
	var conv model.Conversation
	if err := s.db.WithContext(ctx).Where("id = ?", conversationID).First(&conv).Error; err != nil {
		return nil, &NotFoundError{Resource: "conversation", ID: conversationID.String()}
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}

	allEntries, err := s.listEntriesForGroup(ctx, conv.ConversationGroupID)
	if err != nil {
		return nil, err
	}

	var filtered []model.Entry
	if query.AllForks {
		filtered = allEntries
	} else {
		ancestry, err := s.buildAncestryStack(ctx, conv)
		if err != nil {
			return nil, err
		}
		filtered = filterEntriesByAncestry(allEntries, ancestry)
	}
	if query.Channel != nil {
		ch := *query.Channel
		tmp := filtered[:0]
		for _, entry := range filtered {
			if entry.Channel == ch {
				tmp = append(tmp, entry)
			}
		}
		filtered = tmp
	}

	filtered, cursor := paginateEntries(filtered, query.AfterCursor, limit)
	for i := range filtered {
		if decrypted, err := s.decrypt(filtered[i].Content); err == nil {
			filtered[i].Content = decrypted
		}
	}
	return &registrystore.PagedEntries{Data: filtered, AfterCursor: cursor}, nil
}

func (s *PostgresStore) AdminListMemberships(ctx context.Context, conversationID uuid.UUID, afterCursor *string, limit int) ([]model.ConversationMembership, *string, error) {
	var conv model.Conversation
	if err := s.db.WithContext(ctx).Where("id = ?", conversationID).First(&conv).Error; err != nil {
		return nil, nil, &NotFoundError{Resource: "conversation", ID: conversationID.String()}
	}

	tx := s.db.WithContext(ctx).Where("conversation_group_id = ?", conv.ConversationGroupID).Order("created_at ASC")
	if afterCursor != nil {
		tx = tx.Where("created_at > (SELECT created_at FROM conversation_memberships WHERE conversation_group_id = ? AND user_id = ?)", conv.ConversationGroupID, *afterCursor)
	}
	tx = tx.Limit(limit + 1)

	var memberships []model.ConversationMembership
	if err := tx.Find(&memberships).Error; err != nil {
		return nil, nil, fmt.Errorf("failed to admin list memberships: %w", err)
	}

	hasMore := len(memberships) > limit
	if hasMore {
		memberships = memberships[:limit]
	}
	var cursor *string
	if hasMore && len(memberships) > 0 {
		c := memberships[len(memberships)-1].UserID
		cursor = &c
	}
	return memberships, cursor, nil
}

func (s *PostgresStore) AdminListForks(ctx context.Context, conversationID uuid.UUID, afterCursor *string, limit int) ([]registrystore.ConversationForkSummary, *string, error) {
	var conv model.Conversation
	if err := s.db.WithContext(ctx).Where("id = ?", conversationID).First(&conv).Error; err != nil {
		return nil, nil, &NotFoundError{Resource: "conversation", ID: conversationID.String()}
	}

	tx := s.db.WithContext(ctx).
		Table("conversations").
		Where("conversation_group_id = ?", conv.ConversationGroupID).
		Order("created_at ASC")

	if afterCursor != nil {
		tx = tx.Where("created_at > (SELECT created_at FROM conversations WHERE id = ?)", *afterCursor)
	}
	tx = tx.Limit(limit + 1)

	var convs []model.Conversation
	if err := tx.Find(&convs).Error; err != nil {
		return nil, nil, fmt.Errorf("failed to admin list forks: %w", err)
	}

	hasMore := len(convs) > limit
	if hasMore {
		convs = convs[:limit]
	}

	forks := make([]registrystore.ConversationForkSummary, len(convs))
	for i, c := range convs {
		forks[i] = registrystore.ConversationForkSummary{
			ID:                     c.ID,
			Title:                  s.decryptString(c.Title),
			ForkedAtEntryID:        c.ForkedAtEntryID,
			ForkedAtConversationID: c.ForkedAtConversationID,
			CreatedAt:              c.CreatedAt,
		}
	}

	var cursor *string
	if hasMore && len(forks) > 0 {
		c := forks[len(forks)-1].ID.String()
		cursor = &c
	}
	return forks, cursor, nil
}

func (s *PostgresStore) AdminSearchEntries(ctx context.Context, query registrystore.AdminSearchQuery) (*registrystore.SearchResults, error) {
	prefixQuery := toPrefixTsQuery(query.Query)
	if prefixQuery == "" {
		return &registrystore.SearchResults{Data: []registrystore.SearchResult{}}, nil
	}
	sql := `
		SELECT e.id as entry_id, e.conversation_id, e.conversation_group_id, c.title as conversation_title,
		       ts_rank(e.indexed_content_tsv, to_tsquery('english', ?)) as score,
		       ts_headline('english', e.indexed_content, to_tsquery('english', ?),
		           'StartSel=**, StopSel=**, MaxWords=50, MinWords=20') as highlight
		FROM entries e
		JOIN conversations c ON c.id = e.conversation_id AND c.conversation_group_id = e.conversation_group_id
		WHERE e.indexed_content_tsv @@ to_tsquery('english', ?)
	`
	args := []interface{}{prefixQuery, prefixQuery, prefixQuery}

	if query.UserID != nil {
		sql += " AND c.owner_user_id = ?"
		args = append(args, *query.UserID)
	}
	sql += " ORDER BY score DESC LIMIT ?"
	args = append(args, query.Limit)

	type searchRow struct {
		EntryID             uuid.UUID `gorm:"column:entry_id"`
		ConversationID      uuid.UUID `gorm:"column:conversation_id"`
		ConversationGroupID uuid.UUID `gorm:"column:conversation_group_id"`
		ConversationTitle   []byte    `gorm:"column:conversation_title"`
		Score               float64   `gorm:"column:score"`
		Highlight           string    `gorm:"column:highlight"`
	}
	var rows []searchRow
	if err := s.db.WithContext(ctx).Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("admin search failed: %w", err)
	}

	results := make([]registrystore.SearchResult, len(rows))
	for i, r := range rows {
		highlight := r.Highlight
		results[i] = registrystore.SearchResult{
			EntryID:        r.EntryID,
			ConversationID: r.ConversationID,
			Score:          r.Score,
			Kind:           "postgres",
			Highlights:     &highlight,
		}
		if len(r.ConversationTitle) > 0 {
			title := s.decryptString(r.ConversationTitle)
			results[i].ConversationTitle = &title
		}
		if query.IncludeEntry {
			var entry model.Entry
			result := s.db.WithContext(ctx).
				Where("id = ? AND conversation_group_id = ?", r.EntryID, r.ConversationGroupID).
				Limit(1).
				Find(&entry)
			if result.Error == nil && result.RowsAffected > 0 {
				if decrypted, err := s.decrypt(entry.Content); err == nil {
					entry.Content = decrypted
				}
				results[i].Entry = &entry
			}
		}
	}
	return &registrystore.SearchResults{Data: results}, nil
}

func (s *PostgresStore) AdminListAttachments(ctx context.Context, query registrystore.AdminAttachmentQuery) ([]registrystore.AdminAttachment, *string, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}

	tx := s.db.WithContext(ctx).Table("attachments AS a").
		Select("a.*, (SELECT COUNT(*) FROM attachments a2 WHERE a2.storage_key = a.storage_key AND a2.deleted_at IS NULL) AS ref_count")

	if query.UserID != nil {
		tx = tx.Where("a.user_id = ?", *query.UserID)
	}
	if query.EntryID != nil {
		tx = tx.Where("a.entry_id = ?", *query.EntryID)
	}

	switch strings.ToLower(strings.TrimSpace(query.Status)) {
	case "linked":
		tx = tx.Where("a.entry_id IS NOT NULL")
	case "unlinked":
		tx = tx.Where("a.entry_id IS NULL")
	case "expired":
		tx = tx.Where("a.expires_at IS NOT NULL AND a.expires_at < ?", time.Now())
	case "", "all":
		// no-op
	default:
		return nil, nil, &ValidationError{Field: "status", Message: "invalid status"}
	}

	if query.AfterCursor != nil {
		tx = tx.Where("a.created_at > (SELECT created_at FROM attachments WHERE id = ?)", *query.AfterCursor)
	}

	type row struct {
		model.Attachment
		RefCount int64 `gorm:"column:ref_count"`
	}
	var rows []row
	if err := tx.Order("a.created_at ASC, a.id ASC").Limit(limit + 1).Scan(&rows).Error; err != nil {
		return nil, nil, fmt.Errorf("admin list attachments failed: %w", err)
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	out := make([]registrystore.AdminAttachment, len(rows))
	for i, r := range rows {
		out[i] = registrystore.AdminAttachment{
			Attachment: r.Attachment,
			RefCount:   r.RefCount,
		}
	}

	var cursor *string
	if hasMore && len(rows) > 0 {
		c := rows[len(rows)-1].ID.String()
		cursor = &c
	}
	return out, cursor, nil
}

func (s *PostgresStore) AdminGetAttachment(ctx context.Context, attachmentID uuid.UUID) (*registrystore.AdminAttachment, error) {
	type row struct {
		model.Attachment
		RefCount int64 `gorm:"column:ref_count"`
	}

	var r row
	err := s.db.WithContext(ctx).Table("attachments AS a").
		Select("a.*, (SELECT COUNT(*) FROM attachments a2 WHERE a2.storage_key = a.storage_key AND a2.deleted_at IS NULL) AS ref_count").
		Where("a.id = ?", attachmentID).
		Take(&r).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, &NotFoundError{Resource: "attachment", ID: attachmentID.String()}
		}
		return nil, fmt.Errorf("admin get attachment failed: %w", err)
	}

	return &registrystore.AdminAttachment{
		Attachment: r.Attachment,
		RefCount:   r.RefCount,
	}, nil
}

func (s *PostgresStore) AdminDeleteAttachment(ctx context.Context, attachmentID uuid.UUID) error {
	result := s.db.WithContext(ctx).Where("id = ?", attachmentID).Delete(&model.Attachment{})
	if result.Error != nil {
		return fmt.Errorf("admin delete attachment failed: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return &NotFoundError{Resource: "attachment", ID: attachmentID.String()}
	}
	return nil
}

// --- Eviction ---

func (s *PostgresStore) FindEvictableGroupIDs(ctx context.Context, cutoff time.Time, limit int) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := s.db.WithContext(ctx).
		Model(&model.ConversationGroup{}).
		Where("deleted_at IS NOT NULL AND deleted_at < ?", cutoff).
		Limit(limit).
		Pluck("id", &ids).Error
	return ids, err
}

func (s *PostgresStore) CountEvictableGroups(ctx context.Context, cutoff time.Time) (int64, error) {
	var count int64
	err := s.db.WithContext(ctx).
		Model(&model.ConversationGroup{}).
		Where("deleted_at IS NOT NULL AND deleted_at < ?", cutoff).
		Count(&count).Error
	return count, err
}

func (s *PostgresStore) HardDeleteConversationGroups(ctx context.Context, groupIDs []uuid.UUID) error {
	// ON DELETE CASCADE handles entries and conversations
	return s.db.WithContext(ctx).Where("id IN ?", groupIDs).Delete(&model.ConversationGroup{}).Error
}

func (s *PostgresStore) CreateTask(ctx context.Context, taskType string, taskBody map[string]interface{}) error {
	var taskName *string
	if rawName, ok := taskBody["taskName"]; ok {
		if name, ok := rawName.(string); ok {
			trimmed := strings.TrimSpace(name)
			if trimmed != "" {
				taskName = &trimmed
			}
		}
	}

	task := model.Task{
		TaskName: taskName,
		ID:       uuid.New(),
		TaskType: taskType,
		TaskBody: taskBody,
	}
	err := s.db.WithContext(ctx).Create(&task).Error
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if taskName != nil && errors.As(err, &pgErr) && pgErr.Code == "23505" {
		// Singleton task already exists; idempotent no-op.
		return nil
	}
	return err
}

func (s *PostgresStore) ClaimReadyTasks(ctx context.Context, limit int) ([]model.Task, error) {
	var tasks []model.Task
	err := s.db.WithContext(ctx).Raw(`
		WITH claimed AS (
			SELECT id
			FROM tasks
			WHERE retry_at <= NOW()
			ORDER BY retry_at, created_at
			LIMIT ?
			FOR UPDATE SKIP LOCKED
		)
		UPDATE tasks t
		SET retry_at = NOW() + INTERVAL '5 minutes'
		FROM claimed
		WHERE t.id = claimed.id
		RETURNING t.*
	`, limit).
		Scan(&tasks).Error
	return tasks, err
}

func (s *PostgresStore) DeleteTask(ctx context.Context, taskID uuid.UUID) error {
	return s.db.WithContext(ctx).Where("id = ?", taskID).Delete(&model.Task{}).Error
}

func (s *PostgresStore) FailTask(ctx context.Context, taskID uuid.UUID, errMsg string, retryDelay time.Duration) error {
	return s.db.WithContext(ctx).Model(&model.Task{}).Where("id = ?", taskID).Updates(map[string]interface{}{
		"retry_count": gorm.Expr("retry_count + 1"),
		"retry_at":    time.Now().Add(retryDelay),
		"last_error":  errMsg,
	}).Error
}

func (s *PostgresStore) AdminGetAttachmentByStorageKey(ctx context.Context, storageKey string) (*registrystore.AdminAttachment, error) {
	type row struct {
		model.Attachment
		RefCount int64 `gorm:"column:ref_count"`
	}

	var r row
	err := s.db.WithContext(ctx).Table("attachments AS a").
		Select("a.*, (SELECT COUNT(*) FROM attachments a2 WHERE a2.storage_key = a.storage_key AND a2.deleted_at IS NULL) AS ref_count").
		Where("a.storage_key = ? AND a.deleted_at IS NULL", storageKey).
		Take(&r).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, &NotFoundError{Resource: "attachment", ID: storageKey}
		}
		return nil, fmt.Errorf("admin get attachment by storage key failed: %w", err)
	}

	return &registrystore.AdminAttachment{
		Attachment: r.Attachment,
		RefCount:   r.RefCount,
	}, nil
}

// --- Helpers ---

func (s *PostgresStore) requireAccess(ctx context.Context, userID string, groupID uuid.UUID, minLevel model.AccessLevel) (model.AccessLevel, error) {
	var m model.ConversationMembership
	result := s.db.WithContext(ctx).
		Where("conversation_group_id = ? AND user_id = ?", groupID, userID).
		Limit(1).
		Find(&m)
	if result.Error != nil {
		return "", fmt.Errorf("failed to check access: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return "", &ForbiddenError{}
	}
	if !m.AccessLevel.IsAtLeast(minLevel) {
		return "", &ForbiddenError{}
	}
	return m.AccessLevel, nil
}

type forkAncestor struct {
	ConversationID uuid.UUID
	StopAtEntryID  *uuid.UUID
}

// fetchLatestMemoryEntries returns the latest-epoch memory entries for the given
// conversation and clientID, using MemoryEntriesCache as a read-through layer.
func (s *PostgresStore) fetchLatestMemoryEntries(ctx context.Context, conv model.Conversation, ancestry []forkAncestor, clientID string) ([]model.Entry, error) {
	if s.entriesCache != nil && s.entriesCache.Available() {
		cached, err := s.entriesCache.Get(ctx, conv.ID, clientID)
		if err == nil && cached != nil {
			if security.CacheHitsTotal != nil {
				security.CacheHitsTotal.Inc()
			}
			return cached.Entries, nil
		}
	}

	allEntries, err := s.listEntriesForGroup(ctx, conv.ConversationGroupID)
	if err != nil {
		return nil, err
	}
	latestFilter := &registrystore.MemoryEpochFilter{Mode: registrystore.MemoryEpochModeLatest}
	entries := filterMemoryEntriesWithEpoch(allEntries, ancestry, clientID, latestFilter)

	if s.entriesCache != nil && s.entriesCache.Available() {
		if security.CacheMissesTotal != nil {
			security.CacheMissesTotal.Inc()
		}
		if len(entries) > 0 {
			var epoch *int64
			for i := range entries {
				if entries[i].Epoch != nil && (epoch == nil || *entries[i].Epoch > *epoch) {
					epoch = entries[i].Epoch
				}
			}
			if serr := s.entriesCache.Set(ctx, conv.ID, clientID, registrycache.CachedMemoryEntries{Entries: entries, Epoch: epoch}, 0); serr != nil {
				log.Warn("entries cache set error", "err", serr)
			}
		}
	}
	return entries, nil
}

// warmEntriesCache re-fetches the latest memory entries from the DB and updates the cache.
// Called after a successful SyncAgentEntry write to keep the cache warm.
func (s *PostgresStore) warmEntriesCache(ctx context.Context, conv model.Conversation, ancestry []forkAncestor, clientID string) {
	if s.entriesCache == nil || !s.entriesCache.Available() {
		return
	}
	allEntries, err := s.listEntriesForGroup(ctx, conv.ConversationGroupID)
	if err != nil {
		log.Warn("warmEntriesCache: failed to list entries", "err", err)
		return
	}
	latestFilter := &registrystore.MemoryEpochFilter{Mode: registrystore.MemoryEpochModeLatest}
	entries := filterMemoryEntriesWithEpoch(allEntries, ancestry, clientID, latestFilter)
	if len(entries) == 0 {
		if rerr := s.entriesCache.Remove(ctx, conv.ID, clientID); rerr != nil {
			log.Warn("warmEntriesCache: cache remove error", "err", rerr)
		}
		return
	}
	var epoch *int64
	for i := range entries {
		if entries[i].Epoch != nil && (epoch == nil || *entries[i].Epoch > *epoch) {
			epoch = entries[i].Epoch
		}
	}
	if serr := s.entriesCache.Set(ctx, conv.ID, clientID, registrycache.CachedMemoryEntries{Entries: entries, Epoch: epoch}, 0); serr != nil {
		log.Warn("warmEntriesCache: cache set error", "err", serr)
	}
}

func (s *PostgresStore) listEntriesForGroup(ctx context.Context, groupID uuid.UUID) ([]model.Entry, error) {
	var entries []model.Entry
	if err := s.db.WithContext(ctx).
		Where("conversation_group_id = ?", groupID).
		Order("created_at ASC").
		Find(&entries).Error; err != nil {
		return nil, fmt.Errorf("failed to list entries: %w", err)
	}
	return entries, nil
}

func (s *PostgresStore) buildAncestryStack(ctx context.Context, target model.Conversation) ([]forkAncestor, error) {
	var conversations []model.Conversation
	if err := s.db.WithContext(ctx).
		Where("conversation_group_id = ? AND deleted_at IS NULL", target.ConversationGroupID).
		Find(&conversations).Error; err != nil {
		return nil, fmt.Errorf("failed to load fork ancestry: %w", err)
	}

	byID := make(map[uuid.UUID]model.Conversation, len(conversations))
	for _, conv := range conversations {
		byID[conv.ID] = conv
	}

	stack := make([]forkAncestor, 0, len(conversations))
	current := target
	var stopAt *uuid.UUID

	for {
		stack = append(stack, forkAncestor{
			ConversationID: current.ID,
			StopAtEntryID:  stopAt,
		})

		stopAt = current.ForkedAtEntryID
		if current.ForkedAtConversationID == nil {
			break
		}
		parent, ok := byID[*current.ForkedAtConversationID]
		if !ok {
			break
		}
		current = parent
	}

	for i, j := 0, len(stack)-1; i < j; i, j = i+1, j-1 {
		stack[i], stack[j] = stack[j], stack[i]
	}
	return stack, nil
}

func filterEntriesByAncestry(allEntries []model.Entry, ancestry []forkAncestor) []model.Entry {
	if len(ancestry) == 0 {
		return allEntries
	}

	result := make([]model.Entry, 0, len(allEntries))
	ancestorIndex := 0
	current := ancestry[ancestorIndex]
	isTarget := ancestorIndex == len(ancestry)-1

	for _, entry := range allEntries {
		if entry.ConversationID != current.ConversationID {
			continue
		}

		result = append(result, entry)
		if !isTarget && current.StopAtEntryID != nil && entry.ID == *current.StopAtEntryID {
			ancestorIndex++
			if ancestorIndex < len(ancestry) {
				current = ancestry[ancestorIndex]
				isTarget = ancestorIndex == len(ancestry)-1
			}
		}
	}
	return result
}

func normalizeEpochFilter(filter *registrystore.MemoryEpochFilter) registrystore.MemoryEpochFilter {
	if filter == nil || filter.Mode == "" {
		return registrystore.MemoryEpochFilter{Mode: registrystore.MemoryEpochModeLatest}
	}
	return *filter
}

func filterEntriesForAllForks(entries []model.Entry, channel model.Channel, clientID *string, epochFilter *registrystore.MemoryEpochFilter) []model.Entry {
	if channel == "" {
		return entries
	}

	filtered := make([]model.Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.Channel != channel {
			continue
		}
		if channel == model.ChannelMemory && clientID != nil {
			if entry.ClientID == nil || *entry.ClientID != *clientID {
				continue
			}
		}
		filtered = append(filtered, entry)
	}

	if channel != model.ChannelMemory {
		return filtered
	}

	epoch := normalizeEpochFilter(epochFilter)
	switch epoch.Mode {
	case registrystore.MemoryEpochModeAll:
		return filtered
	case registrystore.MemoryEpochModeEpoch:
		if epoch.Epoch == nil {
			return nil
		}
		result := make([]model.Entry, 0, len(filtered))
		for _, entry := range filtered {
			entryEpoch := int64(0)
			if entry.Epoch != nil {
				entryEpoch = *entry.Epoch
			}
			if entryEpoch == *epoch.Epoch {
				result = append(result, entry)
			}
		}
		return result
	default:
		// latest
		var maxEpoch int64
		hasEpoch := false
		for _, entry := range filtered {
			entryEpoch := int64(0)
			if entry.Epoch != nil {
				entryEpoch = *entry.Epoch
			}
			if !hasEpoch || entryEpoch > maxEpoch {
				maxEpoch = entryEpoch
				hasEpoch = true
			}
		}
		if !hasEpoch {
			return nil
		}
		result := make([]model.Entry, 0, len(filtered))
		for _, entry := range filtered {
			entryEpoch := int64(0)
			if entry.Epoch != nil {
				entryEpoch = *entry.Epoch
			}
			if entryEpoch == maxEpoch {
				result = append(result, entry)
			}
		}
		return result
	}
}

func filterMemoryEntriesWithEpoch(allEntries []model.Entry, ancestry []forkAncestor, clientID string, epochFilter *registrystore.MemoryEpochFilter) []model.Entry {
	epoch := normalizeEpochFilter(epochFilter)
	result := make([]model.Entry, 0, len(allEntries))
	maxEpochSeen := int64(0)
	maxEpochInitialized := false

	if len(ancestry) == 0 {
		return result
	}

	ancestorIndex := 0
	current := ancestry[ancestorIndex]
	isTarget := ancestorIndex == len(ancestry)-1

	for _, entry := range allEntries {
		if entry.ConversationID != current.ConversationID {
			continue
		}

		if entry.Channel == model.ChannelMemory && entry.ClientID != nil && *entry.ClientID == clientID {
			entryEpoch := int64(0)
			if entry.Epoch != nil {
				entryEpoch = *entry.Epoch
			}

			switch epoch.Mode {
			case registrystore.MemoryEpochModeAll:
				result = append(result, entry)
			case registrystore.MemoryEpochModeEpoch:
				if epoch.Epoch != nil && entryEpoch == *epoch.Epoch {
					result = append(result, entry)
				}
			default:
				// latest
				if !maxEpochInitialized || entryEpoch > maxEpochSeen {
					result = result[:0]
					maxEpochSeen = entryEpoch
					maxEpochInitialized = true
				}
				if entryEpoch == maxEpochSeen {
					result = append(result, entry)
				}
			}
		}

		if !isTarget && current.StopAtEntryID != nil && entry.ID == *current.StopAtEntryID {
			ancestorIndex++
			if ancestorIndex < len(ancestry) {
				current = ancestry[ancestorIndex]
				isTarget = ancestorIndex == len(ancestry)-1
			}
		}
	}

	return result
}

func paginateEntries(entries []model.Entry, afterEntryID *string, limit int) ([]model.Entry, *string) {
	start := 0
	if afterEntryID != nil {
		for i, entry := range entries {
			if entry.ID.String() == *afterEntryID {
				start = i + 1
				break
			}
		}
	}

	if start >= len(entries) {
		return []model.Entry{}, nil
	}

	end := start + limit
	if end > len(entries) {
		end = len(entries)
	}

	page := entries[start:end]
	var cursor *string
	if end < len(entries) && len(page) > 0 {
		c := page[len(page)-1].ID.String()
		cursor = &c
	}
	return page, cursor
}

func decryptEntries(s *PostgresStore, entries []model.Entry) {
	for i := range entries {
		if decrypted, err := s.decrypt(entries[i].Content); err == nil {
			entries[i].Content = decrypted
		}
	}
}

func flattenMemoryContent(s *PostgresStore, entries []model.Entry) []any {
	result := make([]any, 0)
	for _, entry := range entries {
		content := entry.Content
		if decrypted, err := s.decrypt(content); err == nil {
			content = decrypted
		}
		result = append(result, parseContentArray(content)...)
	}
	return result
}

func parseContentArray(raw []byte) []any {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return []any{}
	}

	var list []any
	if err := json.Unmarshal(raw, &list); err == nil {
		return list
	}

	var obj any
	if err := json.Unmarshal([]byte(raw), &obj); err == nil {
		return []any{obj}
	}
	return []any{raw}
}

func marshalContentArray(content []any) json.RawMessage {
	b, err := json.Marshal(content)
	if err != nil {
		return json.RawMessage("[]")
	}
	return b
}

func isPrefixContent(existing, incoming []any) bool {
	if len(existing) > len(incoming) {
		return false
	}
	for i := range existing {
		if !reflect.DeepEqual(existing[i], incoming[i]) {
			return false
		}
	}
	return true
}

// --- Attachments ---

func (s *PostgresStore) CreateAttachment(ctx context.Context, userID string, conversationID uuid.UUID, attachment model.Attachment) (*model.Attachment, error) {
	// conversationID is optional; when not provided, create an unlinked attachment
	// owned by the uploader.
	if conversationID != uuid.Nil {
		if _, err := s.getGroupID(ctx, userID, conversationID, model.AccessLevelWriter); err != nil {
			return nil, err
		}
	}
	attachment.ID = uuid.New()
	attachment.UserID = userID
	if strings.TrimSpace(attachment.Status) == "" {
		attachment.Status = "ready"
	}
	if err := s.db.WithContext(ctx).Create(&attachment).Error; err != nil {
		return nil, fmt.Errorf("create attachment failed: %w", err)
	}
	return &attachment, nil
}

func (s *PostgresStore) UpdateAttachment(ctx context.Context, userID string, attachmentID uuid.UUID, update registrystore.AttachmentUpdate) (*model.Attachment, error) {
	var attachment model.Attachment
	if err := s.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", attachmentID).First(&attachment).Error; err != nil {
		return nil, &NotFoundError{Resource: "attachment", ID: attachmentID.String()}
	}
	if attachment.UserID != userID {
		return nil, &ForbiddenError{}
	}

	values := map[string]any{}
	if update.StorageKey != nil {
		values["storage_key"] = *update.StorageKey
	}
	if update.Filename != nil {
		values["filename"] = *update.Filename
	}
	if update.ContentType != nil {
		values["content_type"] = *update.ContentType
	}
	if update.Size != nil {
		values["size"] = *update.Size
	}
	if update.SHA256 != nil {
		values["sha256"] = *update.SHA256
	}
	if update.Status != nil {
		values["status"] = *update.Status
	}
	if update.SourceURL != nil {
		values["source_url"] = *update.SourceURL
	}
	if update.ExpiresAt != nil {
		values["expires_at"] = *update.ExpiresAt
	}
	if update.EntryID != nil {
		values["entry_id"] = *update.EntryID
	}

	if len(values) > 0 {
		if err := s.db.WithContext(ctx).Model(&model.Attachment{}).Where("id = ?", attachmentID).Updates(values).Error; err != nil {
			return nil, fmt.Errorf("update attachment failed: %w", err)
		}
	}

	if err := s.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", attachmentID).First(&attachment).Error; err != nil {
		return nil, &NotFoundError{Resource: "attachment", ID: attachmentID.String()}
	}
	return &attachment, nil
}

func (s *PostgresStore) ListAttachments(ctx context.Context, userID string, conversationID uuid.UUID, afterCursor *string, limit int) ([]model.Attachment, *string, error) {
	tx := s.db.WithContext(ctx).Where("deleted_at IS NULL")

	if conversationID == uuid.Nil {
		// Contract path does not include conversation id; list caller-owned unlinked attachments.
		tx = tx.Where("user_id = ? AND entry_id IS NULL", userID)
	} else {
		groupID, err := s.getGroupID(ctx, userID, conversationID, model.AccessLevelReader)
		if err != nil {
			return nil, nil, err
		}
		tx = tx.Where(
			"entry_id IN (SELECT id FROM entries WHERE conversation_id = ? AND conversation_group_id = ?)",
			conversationID, groupID,
		)
	}

	tx = tx.Order("created_at ASC").Limit(limit + 1)
	if afterCursor != nil {
		tx = tx.Where("created_at > (SELECT created_at FROM attachments WHERE id = ?)", *afterCursor)
	}

	var attachments []model.Attachment
	if err := tx.Find(&attachments).Error; err != nil {
		return nil, nil, fmt.Errorf("list attachments failed: %w", err)
	}

	hasMore := len(attachments) > limit
	if hasMore {
		attachments = attachments[:limit]
	}
	var cursor *string
	if hasMore && len(attachments) > 0 {
		c := attachments[len(attachments)-1].ID.String()
		cursor = &c
	}
	return attachments, cursor, nil
}

func (s *PostgresStore) GetAttachment(ctx context.Context, userID string, conversationID uuid.UUID, attachmentID uuid.UUID) (*model.Attachment, error) {
	var attachment model.Attachment
	if err := s.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", attachmentID).First(&attachment).Error; err != nil {
		return nil, &NotFoundError{Resource: "attachment", ID: attachmentID.String()}
	}

	// Unlinked attachments are only visible to the uploader.
	if attachment.EntryID == nil {
		if attachment.UserID != userID {
			return nil, &ForbiddenError{}
		}
		return &attachment, nil
	}

	tx := s.db.WithContext(ctx).Where("id = ?", *attachment.EntryID)
	if conversationID != uuid.Nil {
		tx = tx.Where("conversation_id = ?", conversationID)
	}
	var entries []model.Entry
	if err := tx.Find(&entries).Error; err != nil {
		return nil, fmt.Errorf("get attachment entry lookup failed: %w", err)
	}
	if len(entries) == 0 {
		// Entry was hard-deleted (conversation deletion). Fall back to ownership check.
		if attachment.UserID == userID {
			return &attachment, nil
		}
		return nil, &NotFoundError{Resource: "attachment", ID: attachmentID.String()}
	}

	var sawForbidden bool
	for _, entry := range entries {
		if _, err := s.requireAccess(ctx, userID, entry.ConversationGroupID, model.AccessLevelReader); err == nil {
			return &attachment, nil
		} else {
			var forbidden *ForbiddenError
			if errors.As(err, &forbidden) {
				sawForbidden = true
				continue
			}
			return nil, err
		}
	}
	if sawForbidden {
		return nil, &ForbiddenError{}
	}
	return nil, &NotFoundError{Resource: "attachment", ID: attachmentID.String()}
}

func (s *PostgresStore) DeleteAttachment(ctx context.Context, userID string, conversationID uuid.UUID, attachmentID uuid.UUID) error {
	attachment, err := s.GetAttachment(ctx, userID, conversationID, attachmentID)
	if err != nil {
		return err
	}

	// Only the uploader can delete, and only before attachment is linked to an entry.
	if attachment.UserID != userID {
		return &ForbiddenError{}
	}
	if attachment.EntryID != nil {
		return &ConflictError{Message: "linked attachments cannot be deleted"}
	}

	result := s.db.WithContext(ctx).Where("id = ?", attachmentID).Delete(&model.Attachment{})
	if result.Error != nil {
		return fmt.Errorf("delete attachment failed: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return &NotFoundError{Resource: "attachment", ID: attachmentID.String()}
	}
	return nil
}

func (s *PostgresStore) getGroupID(ctx context.Context, userID string, conversationID uuid.UUID, minLevel model.AccessLevel) (uuid.UUID, error) {
	var conv model.Conversation
	if err := s.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", conversationID).First(&conv).Error; err != nil {
		return uuid.Nil, &NotFoundError{Resource: "conversation", ID: conversationID.String()}
	}
	if _, err := s.requireAccess(ctx, userID, conv.ConversationGroupID, minLevel); err != nil {
		return uuid.Nil, err
	}
	return conv.ConversationGroupID, nil
}
