package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/dataencryption"
	"github.com/chirino/memory-service/internal/model"
	registrycache "github.com/chirino/memory-service/internal/registry/cache"
	registrymigrate "github.com/chirino/memory-service/internal/registry/migrate"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/security"
	"github.com/google/uuid"
	sqlite3 "github.com/mattn/go-sqlite3"
	"gorm.io/gorm"
)

func init() {
	registrystore.Register(registrystore.Plugin{
		Name: "sqlite",
		Loader: func(ctx context.Context) (registrystore.MemoryStore, error) {
			cfg := config.FromContext(ctx)
			handle, err := getSharedHandle(ctx)
			if err != nil {
				return nil, err
			}

			if security.DBPoolMaxConnections != nil {
				security.DBPoolMaxConnections.Set(1)
			}
			if security.DBPoolOpenConnections != nil {
				security.DBPoolOpenConnections.Set(float64(handle.sqlDB.Stats().OpenConnections))
			}

			store := &SQLiteStore{
				handle:       handle,
				db:           handle.db,
				cfg:          cfg,
				entriesCache: registrycache.EntriesCacheFromContext(ctx),
			}
			if !cfg.EncryptionDBDisabled {
				store.enc = dataencryption.FromContext(ctx)
			}
			return store, nil
		},
	})

	registrymigrate.Register(registrymigrate.Plugin{Order: 100, Migrator: &sqliteMigrator{}})
}

type sqliteMigrator struct{}

func (m *sqliteMigrator) Name() string { return "sqlite-schema" }
func (m *sqliteMigrator) Migrate(ctx context.Context) error {
	cfg := config.FromContext(ctx)
	if cfg != nil && !cfg.DatastoreMigrateAtStart {
		return nil
	}
	if cfg.DatastoreType != "" && cfg.DatastoreType != "sqlite" {
		return nil
	}
	log.Info("Running migration", "name", m.Name())
	handle, err := getSharedHandle(ctx)
	if err != nil {
		return fmt.Errorf("migration: failed to connect: %w", err)
	}

	if _, err := handle.sqlDB.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("migration: failed to execute schema: %w", err)
	}
	if handle.fts5Enabled {
		if _, err := handle.sqlDB.ExecContext(ctx, ftsSchemaSQL); err != nil {
			return fmt.Errorf("migration: failed to execute fts schema: %w", err)
		}
	}
	log.Info("SQLite schema migration complete", "fts5Enabled", handle.fts5Enabled)
	return nil
}

// SQLiteStore implements MemoryStore using GORM + SQLite.
type SQLiteStore struct {
	handle       *sharedHandle
	db           *gorm.DB
	cfg          *config.Config
	enc          *dataencryption.Service
	entriesCache registrycache.MemoryEntriesCache
}

func (s *SQLiteStore) InReadTx(ctx context.Context, fn func(context.Context) error) error {
	return s.handle.InReadTx(ctx, fn)
}

func (s *SQLiteStore) InWriteTx(ctx context.Context, fn func(context.Context) error) error {
	return s.handle.InWriteTx(ctx, fn)
}

func (s *SQLiteStore) dbFor(ctx context.Context) *gorm.DB {
	db, err := requireScope(ctx, "sqlite store")
	if err != nil {
		panic(err)
	}
	return db
}

func (s *SQLiteStore) writeDBFor(ctx context.Context, op string) *gorm.DB {
	db, err := requireWriteScope(ctx, op)
	if err != nil {
		panic(err)
	}
	return db
}

func sqliteUniqueViolation(err error) (*sqlite3.Error, bool) {
	var sqliteErr sqlite3.Error
	if errors.As(err, &sqliteErr) &&
		sqliteErr.Code == sqlite3.ErrConstraint &&
		(sqliteErr.ExtendedCode == sqlite3.ErrConstraintUnique || sqliteErr.ExtendedCode == sqlite3.ErrConstraintPrimaryKey) {
		return &sqliteErr, true
	}
	return nil, false
}

func logDuplicateKey(op string, err error, kv ...interface{}) {
	sqliteErr, ok := sqliteUniqueViolation(err)
	if !ok {
		return
	}
	fields := []interface{}{
		"operation", op,
		"sqliteCode", sqliteErr.Code,
		"sqliteExtendedCode", sqliteErr.ExtendedCode,
		"detail", sqliteErr.Error(),
	}
	fields = append(fields, kv...)
	log.Warn("SQLite duplicate key", fields...)
}

func uuidPtrString(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

func (s *SQLiteStore) encrypt(plaintext []byte) ([]byte, error) {
	if s.enc == nil || plaintext == nil {
		return plaintext, nil
	}
	return s.enc.Encrypt(plaintext)
}

func (s *SQLiteStore) decrypt(ciphertext []byte) ([]byte, error) {
	if s.enc == nil || ciphertext == nil {
		return ciphertext, nil
	}
	return s.enc.Decrypt(ciphertext)
}

func (s *SQLiteStore) decryptString(data []byte) string {
	plain, err := s.decrypt(data)
	if err != nil {
		log.Warn("dek: decryption failed, returning raw bytes", "error", err)
		return string(data) // fallback for unencrypted data
	}
	return string(plain)
}

// --- Conversations ---

func (s *SQLiteStore) CreateConversation(ctx context.Context, userID string, title string, metadata map[string]interface{}, forkedAtConversationID *uuid.UUID, forkedAtEntryID *uuid.UUID) (*registrystore.ConversationDetail, error) {
	groupID := uuid.New()
	// For root (non-forked) conversations, use the same UUID for conversation and group
	// to match Java parity (features reference conversationGroupId in SQL against conversations.id).
	convID := groupID
	if forkedAtConversationID != nil {
		convID = uuid.New()
	}
	return s.createConversationWithID(ctx, userID, convID, title, metadata, forkedAtConversationID, forkedAtEntryID)
}

func (s *SQLiteStore) CreateConversationWithID(ctx context.Context, userID string, convID uuid.UUID, title string, metadata map[string]interface{}, forkedAtConversationID *uuid.UUID, forkedAtEntryID *uuid.UUID) (*registrystore.ConversationDetail, error) {
	return s.createConversationWithID(ctx, userID, convID, title, metadata, forkedAtConversationID, forkedAtEntryID)
}

func (s *SQLiteStore) createConversationWithID(ctx context.Context, userID string, convID uuid.UUID, title string, metadata map[string]interface{}, forkedAtConversationID *uuid.UUID, forkedAtEntryID *uuid.UUID) (*registrystore.ConversationDetail, error) {
	db := s.writeDBFor(ctx, "sqlite store create conversation")
	groupID := uuid.New()
	now := time.Now()

	if metadata == nil {
		metadata = map[string]interface{}{}
	}

	// If forking, look up the source conversation's group
	var actualGroupID uuid.UUID
	if forkedAtConversationID != nil {
		var sourceConv model.Conversation
		if err := db.Where("id = ? AND deleted_at IS NULL", *forkedAtConversationID).First(&sourceConv).Error; err != nil {
			return nil, &NotFoundError{Resource: "conversation", ID: forkedAtConversationID.String()}
		}
		// Verify user has access
		if _, err := s.requireAccess(ctx, userID, sourceConv.ConversationGroupID, model.AccessLevelReader); err != nil {
			return nil, err
		}
		// Validate fork point entry exists
		if forkedAtEntryID != nil {
			var entry model.Entry
			if err := db.Where("id = ? AND conversation_group_id = ?", *forkedAtEntryID, sourceConv.ConversationGroupID).First(&entry).Error; err != nil {
				return nil, &NotFoundError{Resource: "entry", ID: forkedAtEntryID.String()}
			}
		}
		actualGroupID = sourceConv.ConversationGroupID
	} else {
		// New root conversation — create a group; for non-forked, use convID as groupID for Java parity
		actualGroupID = convID
		group := model.ConversationGroup{ID: actualGroupID, CreatedAt: now}
		if err := db.Create(&group).Error; err != nil {
			logDuplicateKey("createConversationWithID:createGroup", err,
				"userID", userID,
				"conversationID", convID.String(),
				"conversationGroupID", actualGroupID.String(),
				"forkedAtConversationID", uuidPtrString(forkedAtConversationID),
				"forkedAtEntryID", uuidPtrString(forkedAtEntryID),
			)
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

	if err := db.Create(&conv).Error; err != nil {
		logDuplicateKey("createConversationWithID:createConversation", err,
			"userID", userID,
			"conversationID", convID.String(),
			"conversationGroupID", actualGroupID.String(),
			"forkedAtConversationID", uuidPtrString(forkedAtConversationID),
			"forkedAtEntryID", uuidPtrString(forkedAtEntryID),
		)
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
		if err := db.Create(&membership).Error; err != nil {
			logDuplicateKey("createConversationWithID:createMembership", err,
				"userID", userID,
				"conversationID", convID.String(),
				"conversationGroupID", actualGroupID.String(),
			)
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

func (s *SQLiteStore) ListConversations(ctx context.Context, userID string, query *string, afterCursor *string, limit int, mode model.ConversationListMode) ([]registrystore.ConversationSummary, *string, error) {
	requestedLimit := limit
	queryStr := ""
	if query != nil {
		queryStr = strings.TrimSpace(*query)
	}

	tx := s.dbFor(ctx).
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
		tx = tx.Where("c.created_at < (SELECT created_at FROM conversations WHERE id = ?)", *afterCursor)
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

	tx = tx.Order("c.created_at DESC").Limit(queryLimit)

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

func (s *SQLiteStore) GetConversation(ctx context.Context, userID string, conversationID uuid.UUID) (*registrystore.ConversationDetail, error) {
	var conv model.Conversation
	if err := s.dbFor(ctx).Where("id = ? AND deleted_at IS NULL", conversationID).First(&conv).Error; err != nil {
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

func (s *SQLiteStore) UpdateConversation(ctx context.Context, userID string, conversationID uuid.UUID, title *string, metadata map[string]interface{}) (*registrystore.ConversationDetail, error) {
	db := s.writeDBFor(ctx, "sqlite store update conversation")
	var conv model.Conversation
	if err := db.Where("id = ? AND deleted_at IS NULL", conversationID).First(&conv).Error; err != nil {
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
	if err := db.Model(&conv).Updates(updates).Error; err != nil {
		return nil, fmt.Errorf("failed to update conversation: %w", err)
	}
	return s.GetConversation(ctx, userID, conversationID)
}

func (s *SQLiteStore) DeleteConversation(ctx context.Context, userID string, conversationID uuid.UUID) error {
	db := s.writeDBFor(ctx, "sqlite store delete conversation")
	var conv model.Conversation
	if err := db.Where("id = ? AND deleted_at IS NULL", conversationID).First(&conv).Error; err != nil {
		return &NotFoundError{Resource: "conversation", ID: conversationID.String()}
	}
	if _, err := s.requireAccess(ctx, userID, conv.ConversationGroupID, model.AccessLevelOwner); err != nil {
		return err
	}

	now := time.Now()
	return db.Transaction(func(tx *gorm.DB) error {
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

func (s *SQLiteStore) ListMemberships(ctx context.Context, userID string, conversationID uuid.UUID, afterCursor *string, limit int) ([]model.ConversationMembership, *string, error) {
	groupID, err := s.getGroupID(ctx, userID, conversationID, model.AccessLevelReader)
	if err != nil {
		return nil, nil, err
	}

	tx := s.dbFor(ctx).Where("conversation_group_id = ?", groupID).Order("created_at ASC")
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

func (s *SQLiteStore) ShareConversation(ctx context.Context, userID string, conversationID uuid.UUID, targetUserID string, accessLevel model.AccessLevel) (*model.ConversationMembership, error) {
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
	result := s.writeDBFor(ctx, "sqlite store share conversation").Create(&membership)
	if result.Error != nil {
		if strings.Contains(result.Error.Error(), "duplicate key") {
			return nil, &ConflictError{Message: "user already has access to this conversation"}
		}
		return nil, fmt.Errorf("failed to share conversation: %w", result.Error)
	}
	return &membership, nil
}

func (s *SQLiteStore) UpdateMembership(ctx context.Context, userID string, conversationID uuid.UUID, memberUserID string, accessLevel model.AccessLevel) (*model.ConversationMembership, error) {
	groupID, err := s.getGroupID(ctx, userID, conversationID, model.AccessLevelManager)
	if err != nil {
		return nil, err
	}
	if accessLevel == model.AccessLevelOwner {
		return nil, &ValidationError{Field: "accessLevel", Message: "cannot set owner access; use ownership transfer"}
	}

	db := s.writeDBFor(ctx, "sqlite store update membership")
	result := db.Model(&model.ConversationMembership{}).
		Where("conversation_group_id = ? AND user_id = ?", groupID, memberUserID).
		Update("access_level", accessLevel)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to update membership: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return nil, &NotFoundError{Resource: "membership", ID: memberUserID}
	}

	var m model.ConversationMembership
	result = db.
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

func (s *SQLiteStore) DeleteMembership(ctx context.Context, userID string, conversationID uuid.UUID, memberUserID string) error {
	db := s.writeDBFor(ctx, "sqlite store delete membership")
	groupID, err := s.getGroupID(ctx, userID, conversationID, model.AccessLevelManager)
	if err != nil {
		return err
	}
	// Cannot delete the owner
	var m model.ConversationMembership
	if err := db.Where("conversation_group_id = ? AND user_id = ?", groupID, memberUserID).First(&m).Error; err != nil {
		return &NotFoundError{Resource: "membership", ID: memberUserID}
	}
	if m.AccessLevel == model.AccessLevelOwner {
		return &ValidationError{Field: "userId", Message: "cannot remove the owner"}
	}

	// Java parity: removing the pending transfer recipient cancels the transfer.
	db.
		Where("conversation_group_id = ? AND to_user_id = ?", groupID, memberUserID).
		Delete(&model.OwnershipTransfer{})

	db.Where("conversation_group_id = ? AND user_id = ?", groupID, memberUserID).Delete(&model.ConversationMembership{})
	return nil
}

// --- Forks ---

func (s *SQLiteStore) ListForks(ctx context.Context, userID string, conversationID uuid.UUID, afterCursor *string, limit int) ([]registrystore.ConversationForkSummary, *string, error) {
	groupID, err := s.getGroupID(ctx, userID, conversationID, model.AccessLevelReader)
	if err != nil {
		return nil, nil, err
	}

	tx := s.dbFor(ctx).
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

func (s *SQLiteStore) ListPendingTransfers(ctx context.Context, userID string, role string, afterCursor *string, limit int) ([]registrystore.OwnershipTransferDto, *string, error) {
	tx := s.dbFor(ctx).Table("conversation_ownership_transfers").Order("created_at ASC")

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

func (s *SQLiteStore) GetTransfer(ctx context.Context, userID string, transferID uuid.UUID) (*registrystore.OwnershipTransferDto, error) {
	var t model.OwnershipTransfer
	if err := s.dbFor(ctx).Where("id = ?", transferID).First(&t).Error; err != nil {
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
func (s *SQLiteStore) resolveConversationID(ctx context.Context, groupID uuid.UUID) uuid.UUID {
	var conv model.Conversation
	if err := s.dbFor(ctx).Where("conversation_group_id = ? AND deleted_at IS NULL", groupID).First(&conv).Error; err != nil {
		return uuid.Nil
	}
	return conv.ID
}

func (s *SQLiteStore) CreateOwnershipTransfer(ctx context.Context, userID string, conversationID uuid.UUID, toUserID string) (*registrystore.OwnershipTransferDto, error) {
	db := s.writeDBFor(ctx, "sqlite store create ownership transfer")
	var conv model.Conversation
	if err := db.Where("id = ? AND deleted_at IS NULL", conversationID).First(&conv).Error; err != nil {
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
	if err := db.
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
	if err := db.Create(&transfer).Error; err != nil {
		if _, ok := sqliteUniqueViolation(err); ok {
			// Look up the existing transfer ID for the conflict response.
			var existing model.OwnershipTransfer
			findResult := db.
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

func (s *SQLiteStore) AcceptTransfer(ctx context.Context, userID string, transferID uuid.UUID) error {
	db := s.writeDBFor(ctx, "sqlite store accept transfer")
	var t model.OwnershipTransfer
	if err := db.Where("id = ?", transferID).First(&t).Error; err != nil {
		return &NotFoundError{Resource: "transfer", ID: transferID.String()}
	}
	if t.ToUserID != userID {
		return &ForbiddenError{}
	}

	return db.Transaction(func(tx *gorm.DB) error {
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

func (s *SQLiteStore) DeleteTransfer(ctx context.Context, userID string, transferID uuid.UUID) error {
	db := s.writeDBFor(ctx, "sqlite store delete transfer")
	var t model.OwnershipTransfer
	if err := db.Where("id = ?", transferID).First(&t).Error; err != nil {
		return &NotFoundError{Resource: "transfer", ID: transferID.String()}
	}
	if t.FromUserID != userID && t.ToUserID != userID {
		return &ForbiddenError{}
	}
	db.Where("id = ?", transferID).Delete(&model.OwnershipTransfer{})
	return nil
}

// --- Entries ---

func (s *SQLiteStore) GetEntries(ctx context.Context, userID string, conversationID uuid.UUID, afterEntryID *string, limit int, channel *model.Channel, epochFilter *registrystore.MemoryEpochFilter, clientID *string, allForks bool) (*registrystore.PagedEntries, error) {
	var conv model.Conversation
	result := s.dbFor(ctx).Where("id = ? AND deleted_at IS NULL", conversationID).Limit(1).Find(&conv)
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

func (s *SQLiteStore) GetEntryGroupID(ctx context.Context, entryID uuid.UUID) (uuid.UUID, error) {
	var entry model.Entry
	result := s.dbFor(ctx).Select("conversation_group_id").Where("id = ?", entryID).Limit(1).Find(&entry)
	if result.Error != nil {
		return uuid.Nil, result.Error
	}
	if result.RowsAffected == 0 {
		return uuid.Nil, &NotFoundError{Resource: "entry", ID: entryID.String()}
	}
	return entry.ConversationGroupID, nil
}

func (s *SQLiteStore) AppendEntries(ctx context.Context, userID string, conversationID uuid.UUID, entries []registrystore.CreateEntryRequest, clientID *string, epoch *int64) ([]model.Entry, error) {
	db := s.writeDBFor(ctx, "sqlite store append entries")
	var conv model.Conversation
	convResult := db.Where("id = ? AND deleted_at IS NULL", conversationID).Limit(1).Find(&conv)
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
			// Concurrent writers can race to auto-create the same root conversation.
			// If another request won the insert, load the conversation and continue.
			sqliteErr, ok := sqliteUniqueViolation(err)
			if !ok {
				return nil, err
			}
			log.Warn("append auto-create race detected",
				"userID", userID,
				"conversationID", conversationID.String(),
				"sqliteCode", sqliteErr.Code,
				"sqliteExtendedCode", sqliteErr.ExtendedCode,
				"detail", sqliteErr.Error(),
				"forkedAtConversationID", uuidPtrString(forkedAtConvID),
				"forkedAtEntryID", uuidPtrString(forkedAtEntryID),
			)
			loaded := false
			for attempt := 0; attempt < 10; attempt++ {
				convResult = db.
					Where("id = ? AND deleted_at IS NULL", conversationID).
					Limit(1).
					Find(&conv)
				if convResult.Error != nil {
					return nil, convResult.Error
				}
				if convResult.RowsAffected > 0 {
					loaded = true
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if !loaded {
				return nil, err
			}
		} else {
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
		if err := db.Create(&entry).Error; err != nil {
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
					db.Model(&model.Conversation{}).Where("id = ?", conversationID).Update("title", title)
				}
				break
			}
		}
	}

	// Update conversation timestamp
	db.Model(&model.Conversation{}).Where("id = ?", conversationID).Update("updated_at", now)

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

func (s *SQLiteStore) SyncAgentEntry(ctx context.Context, userID string, conversationID uuid.UUID, entry registrystore.CreateEntryRequest, clientID string) (*registrystore.SyncResult, error) {
	db := s.writeDBFor(ctx, "sqlite store sync agent entry")
	incomingContent := parseContentArray(entry.Content)

	autoCreated := false
	var conv model.Conversation
	result := db.Where("id = ? AND deleted_at IS NULL", conversationID).Limit(1).Find(&conv)
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
	if err := db.Create(&newEntry).Error; err != nil {
		return nil, fmt.Errorf("failed to sync entry: %w", err)
	}
	newEntry.Content = appendContent
	s.warmEntriesCache(ctx, conv, ancestry, clientID)
	return &registrystore.SyncResult{Entry: &newEntry, Epoch: &epochToUse, NoOp: false, EpochIncremented: epochIncremented}, nil
}

// autoCreateConversation creates a conversation with a given ID for sync auto-creation.
func (s *SQLiteStore) autoCreateConversation(ctx context.Context, userID string, conversationID uuid.UUID) (model.Conversation, error) {
	db := s.writeDBFor(ctx, "sqlite store auto create conversation")
	now := time.Now()
	groupID := uuid.New()

	group := model.ConversationGroup{
		ID:        groupID,
		CreatedAt: now,
	}
	if err := db.Create(&group).Error; err != nil {
		logDuplicateKey("autoCreateConversation:createGroup", err,
			"userID", userID,
			"conversationID", conversationID.String(),
			"conversationGroupID", groupID.String(),
		)
		return model.Conversation{}, fmt.Errorf("failed to create conversation group: %w", err)
	}

	conv := model.Conversation{
		ID:                  conversationID,
		ConversationGroupID: groupID,
		OwnerUserID:         userID,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if err := db.Create(&conv).Error; err != nil {
		// Clean up the orphaned group before handling the error.
		_ = db.Delete(&group).Error
		logDuplicateKey("autoCreateConversation:createConversation", err,
			"userID", userID,
			"conversationID", conversationID.String(),
			"conversationGroupID", groupID.String(),
		)
		if _, ok := sqliteUniqueViolation(err); ok {
			// A concurrent request already created this conversation; fetch and return it.
			var existing model.Conversation
			if findErr := db.Limit(1).Find(&existing, "id = ?", conversationID).Error; findErr != nil {
				return model.Conversation{}, fmt.Errorf("failed to fetch existing conversation: %w", findErr)
			}
			return existing, nil
		}
		return model.Conversation{}, fmt.Errorf("failed to create conversation: %w", err)
	}

	membership := model.ConversationMembership{
		ConversationGroupID: groupID,
		UserID:              userID,
		AccessLevel:         model.AccessLevelOwner,
		CreatedAt:           now,
	}
	if err := db.Create(&membership).Error; err != nil {
		return model.Conversation{}, fmt.Errorf("failed to create membership: %w", err)
	}

	return conv, nil
}

// --- Indexing ---

func (s *SQLiteStore) IndexEntries(ctx context.Context, entries []registrystore.IndexEntryRequest) (*registrystore.IndexConversationsResponse, error) {
	count := 0
	for _, req := range entries {
		result := s.writeDBFor(ctx, "sqlite store index entries").Exec(
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

func (s *SQLiteStore) ListUnindexedEntries(ctx context.Context, limit int, afterCursor *string) ([]model.Entry, *string, error) {
	tx := s.dbFor(ctx).
		Where("channel = ? AND indexed_content IS NULL", model.ChannelHistory).
		Order("created_at ASC").
		Limit(limit + 1)

	if afterCursor != nil {
		tx = tx.Where("created_at > (SELECT MAX(e.created_at) FROM entries e WHERE e.id = ?)", *afterCursor)
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

func (s *SQLiteStore) FindEntriesPendingVectorIndexing(ctx context.Context, limit int) ([]model.Entry, error) {
	var entries []model.Entry
	err := s.dbFor(ctx).
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

func (s *SQLiteStore) SetIndexedAt(ctx context.Context, entryID uuid.UUID, conversationGroupID uuid.UUID, indexedAt time.Time) error {
	result := s.writeDBFor(ctx, "sqlite store set indexed at").Exec(
		"UPDATE entries SET indexed_at = ? WHERE id = ? AND conversation_group_id = ?",
		indexedAt, entryID, conversationGroupID,
	)
	return result.Error
}

// --- Search ---

func (s *SQLiteStore) ListConversationGroupIDs(ctx context.Context, userID string) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := s.dbFor(ctx).
		Model(&model.ConversationMembership{}).
		Distinct("conversation_group_id").
		Where("user_id = ?", userID).
		Pluck("conversation_group_id", &ids).Error
	return ids, err
}

func (s *SQLiteStore) FetchSearchResultDetails(ctx context.Context, userID string, entryIDs []uuid.UUID, includeEntry bool) ([]registrystore.SearchResult, error) {
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
	err := s.dbFor(ctx).Raw(`
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

// toPrefixTsQuery converts plain text to an FTS5 prefix query.
// e.g. "Jav script" becomes "\"Jav\"* AND \"script\"*"
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
			parts = append(parts, `"`+escaped+`"*`)
		}
	}
	return strings.Join(parts, " AND ")
}

// escapeTsQueryWord removes characters that have special meaning in FTS query syntax.
func escapeTsQueryWord(word string) string {
	var b strings.Builder
	for _, r := range word {
		switch r {
		case '&', '|', '!', '(', ')', ':', '\'', '\\', '*', '"':
			// skip query special characters
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func paginateSearchResultsByEntryCursor(results []registrystore.SearchResult, afterCursor *string, limit int) ([]registrystore.SearchResult, *string, error) {
	if limit <= 0 {
		limit = 20
	}
	start := 0
	if afterCursor != nil {
		cursorID, err := uuid.Parse(strings.TrimSpace(*afterCursor))
		if err != nil {
			return nil, nil, fmt.Errorf("invalid afterCursor: %w", err)
		}
		start = len(results)
		for i := range results {
			if results[i].EntryID == cursorID {
				start = i + 1
				break
			}
		}
	}
	if start >= len(results) {
		return []registrystore.SearchResult{}, nil, nil
	}
	end := start + limit
	if end > len(results) {
		end = len(results)
	}
	page := results[start:end]
	var next *string
	if end < len(results) && len(page) > 0 {
		v := page[len(page)-1].EntryID.String()
		next = &v
	}
	return page, next, nil
}

func (s *SQLiteStore) SearchEntries(ctx context.Context, userID string, query string, afterCursor *string, limit int, includeEntry bool, groupByConversation bool) (*registrystore.SearchResults, error) {
	prefixQuery := toPrefixTsQuery(query)
	if prefixQuery == "" {
		return &registrystore.SearchResults{Data: []registrystore.SearchResult{}}, nil
	}
	if s.handle == nil || !s.handle.fts5Enabled {
		return &registrystore.SearchResults{Data: []registrystore.SearchResult{}}, nil
	}
	// Full-text search using the external-content FTS5 table.
	sql := `
		SELECT e.id as entry_id, e.conversation_id, e.conversation_group_id, c.title as conversation_title,
		       -bm25(entries_fts) as score,
		       highlight(entries_fts, 0, '**', '**') as highlight
		FROM entries_fts
		JOIN entries e ON e.rowid = entries_fts.rowid
		JOIN conversations c ON c.id = e.conversation_id AND c.conversation_group_id = e.conversation_group_id AND c.deleted_at IS NULL
		JOIN conversation_memberships cm ON cm.conversation_group_id = c.conversation_group_id AND cm.user_id = ?
		WHERE entries_fts MATCH ?
		ORDER BY bm25(entries_fts) ASC, e.id ASC
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
	if err := s.dbFor(ctx).Raw(sql, userID, prefixQuery).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("full-text search failed: %w", err)
	}

	results := make([]registrystore.SearchResult, 0, len(rows))
	seenConversation := map[uuid.UUID]struct{}{}
	for _, r := range rows {
		if groupByConversation {
			if _, exists := seenConversation[r.ConversationID]; exists {
				continue
			}
			seenConversation[r.ConversationID] = struct{}{}
		}
		highlight := r.Highlight
		item := registrystore.SearchResult{
			EntryID:        r.EntryID,
			ConversationID: r.ConversationID,
			Score:          r.Score,
			Kind:           "sqlite",
			Highlights:     &highlight,
		}
		if len(r.ConversationTitle) > 0 {
			title := s.decryptString(r.ConversationTitle)
			item.ConversationTitle = &title
		}
		if includeEntry {
			var entry model.Entry
			result := s.dbFor(ctx).
				Where("id = ? AND conversation_group_id = ?", r.EntryID, r.ConversationGroupID).
				Limit(1).
				Find(&entry)
			if result.Error == nil && result.RowsAffected > 0 {
				if decrypted, err := s.decrypt(entry.Content); err == nil {
					entry.Content = decrypted
				}
				item.Entry = &entry
			}
		}
		results = append(results, item)
	}

	page, cursor, err := paginateSearchResultsByEntryCursor(results, afterCursor, limit)
	if err != nil {
		return nil, err
	}
	return &registrystore.SearchResults{Data: page, AfterCursor: cursor}, nil
}

// --- Admin ---

func (s *SQLiteStore) AdminListConversations(ctx context.Context, query registrystore.AdminConversationQuery) ([]registrystore.ConversationSummary, *string, error) {
	const selectColumns = "c.id, c.title, c.owner_user_id, c.metadata, c.conversation_group_id, c.forked_at_entry_id, c.forked_at_conversation_id, c.created_at, c.updated_at, c.deleted_at, 'owner' as access_level"

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

	base := s.dbFor(ctx).Table("conversations c")

	if !query.IncludeDeleted && !query.OnlyDeleted {
		base = base.Where("c.deleted_at IS NULL")
	}
	if query.OnlyDeleted {
		base = base.Where("c.deleted_at IS NOT NULL")
	}
	if query.UserID != nil {
		base = base.Where("c.owner_user_id = ?", *query.UserID)
	}
	if query.DeletedAfter != nil {
		base = base.Where("c.deleted_at >= ?", *query.DeletedAfter)
	}
	if query.DeletedBefore != nil {
		base = base.Where("c.deleted_at < ?", *query.DeletedBefore)
	}

	var tx *gorm.DB
	switch query.Mode {
	case model.ListModeRoots:
		tx = base.
			Where("c.forked_at_conversation_id IS NULL").
			Select(selectColumns)
	case model.ListModeLatestFork:
		ranked := base.Select(selectColumns + ", ROW_NUMBER() OVER (PARTITION BY c.conversation_group_id ORDER BY c.updated_at DESC, c.created_at DESC, c.id DESC) AS group_rank")
		tx = s.dbFor(ctx).
			Table("(?) AS ranked", ranked).
			Select("id, title, owner_user_id, metadata, conversation_group_id, forked_at_entry_id, forked_at_conversation_id, created_at, updated_at, deleted_at, access_level").
			Where("group_rank = 1")
	default:
		tx = base.Select(selectColumns)
	}

	if query.AfterCursor != nil {
		tx = tx.Where("created_at < (SELECT created_at FROM conversations WHERE id = ?)", *query.AfterCursor)
	}
	tx = tx.Order("created_at DESC").Limit(query.Limit + 1)

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

func (s *SQLiteStore) AdminGetConversation(ctx context.Context, conversationID uuid.UUID) (*registrystore.ConversationDetail, error) {
	var conv model.Conversation
	if err := s.dbFor(ctx).Where("id = ?", conversationID).First(&conv).Error; err != nil {
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

func (s *SQLiteStore) AdminDeleteConversation(ctx context.Context, conversationID uuid.UUID) error {
	db := s.writeDBFor(ctx, "sqlite store admin delete conversation")
	var conv model.Conversation
	if err := db.Where("id = ?", conversationID).First(&conv).Error; err != nil {
		return &NotFoundError{Resource: "conversation", ID: conversationID.String()}
	}
	now := time.Now()
	db.Model(&model.ConversationGroup{}).Where("id = ?", conv.ConversationGroupID).Update("deleted_at", now)
	db.Model(&model.Conversation{}).Where("conversation_group_id = ? AND deleted_at IS NULL", conv.ConversationGroupID).Update("deleted_at", now)
	return nil
}

func (s *SQLiteStore) AdminRestoreConversation(ctx context.Context, conversationID uuid.UUID) error {
	db := s.writeDBFor(ctx, "sqlite store admin restore conversation")
	var conv model.Conversation
	if err := db.Where("id = ?", conversationID).First(&conv).Error; err != nil {
		return &NotFoundError{Resource: "conversation", ID: conversationID.String()}
	}
	if conv.DeletedAt == nil {
		return &ConflictError{Message: "conversation is not deleted"}
	}
	db.Model(&model.ConversationGroup{}).Where("id = ?", conv.ConversationGroupID).Update("deleted_at", nil)
	db.Model(&model.Conversation{}).Where("conversation_group_id = ?", conv.ConversationGroupID).Update("deleted_at", nil)
	return nil
}

func (s *SQLiteStore) AdminGetEntries(ctx context.Context, conversationID uuid.UUID, query registrystore.AdminMessageQuery) (*registrystore.PagedEntries, error) {
	var conv model.Conversation
	if err := s.dbFor(ctx).Where("id = ?", conversationID).First(&conv).Error; err != nil {
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

func (s *SQLiteStore) AdminListMemberships(ctx context.Context, conversationID uuid.UUID, afterCursor *string, limit int) ([]model.ConversationMembership, *string, error) {
	var conv model.Conversation
	if err := s.dbFor(ctx).Where("id = ?", conversationID).First(&conv).Error; err != nil {
		return nil, nil, &NotFoundError{Resource: "conversation", ID: conversationID.String()}
	}

	tx := s.dbFor(ctx).Where("conversation_group_id = ?", conv.ConversationGroupID).Order("created_at ASC")
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

func (s *SQLiteStore) AdminListForks(ctx context.Context, conversationID uuid.UUID, afterCursor *string, limit int) ([]registrystore.ConversationForkSummary, *string, error) {
	var conv model.Conversation
	if err := s.dbFor(ctx).Where("id = ?", conversationID).First(&conv).Error; err != nil {
		return nil, nil, &NotFoundError{Resource: "conversation", ID: conversationID.String()}
	}

	tx := s.dbFor(ctx).
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

func (s *SQLiteStore) AdminSearchEntries(ctx context.Context, query registrystore.AdminSearchQuery) (*registrystore.SearchResults, error) {
	prefixQuery := toPrefixTsQuery(query.Query)
	if prefixQuery == "" {
		return &registrystore.SearchResults{Data: []registrystore.SearchResult{}}, nil
	}
	if s.handle == nil || !s.handle.fts5Enabled {
		return &registrystore.SearchResults{Data: []registrystore.SearchResult{}}, nil
	}
	sql := `
		SELECT e.id as entry_id, e.conversation_id, e.conversation_group_id, c.title as conversation_title,
		       -bm25(entries_fts) as score,
		       highlight(entries_fts, 0, '**', '**') as highlight
		FROM entries_fts
		JOIN entries e ON e.rowid = entries_fts.rowid
		JOIN conversations c ON c.id = e.conversation_id AND c.conversation_group_id = e.conversation_group_id
		WHERE entries_fts MATCH ?
	`
	args := []interface{}{prefixQuery}
	if !query.IncludeDeleted {
		sql += " AND c.deleted_at IS NULL"
	}

	if query.UserID != nil {
		sql += " AND c.owner_user_id = ?"
		args = append(args, *query.UserID)
	}
	sql += " ORDER BY bm25(entries_fts) ASC, e.id ASC"

	type searchRow struct {
		EntryID             uuid.UUID `gorm:"column:entry_id"`
		ConversationID      uuid.UUID `gorm:"column:conversation_id"`
		ConversationGroupID uuid.UUID `gorm:"column:conversation_group_id"`
		ConversationTitle   []byte    `gorm:"column:conversation_title"`
		Score               float64   `gorm:"column:score"`
		Highlight           string    `gorm:"column:highlight"`
	}
	var rows []searchRow
	if err := s.dbFor(ctx).Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("admin search failed: %w", err)
	}

	results := make([]registrystore.SearchResult, len(rows))
	for i, r := range rows {
		highlight := r.Highlight
		results[i] = registrystore.SearchResult{
			EntryID:        r.EntryID,
			ConversationID: r.ConversationID,
			Score:          r.Score,
			Kind:           "sqlite",
			Highlights:     &highlight,
		}
		if len(r.ConversationTitle) > 0 {
			title := s.decryptString(r.ConversationTitle)
			results[i].ConversationTitle = &title
		}
		if query.IncludeEntry {
			var entry model.Entry
			result := s.dbFor(ctx).
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
	page, cursor, err := paginateSearchResultsByEntryCursor(results, query.AfterCursor, query.Limit)
	if err != nil {
		return nil, err
	}
	return &registrystore.SearchResults{Data: page, AfterCursor: cursor}, nil
}

func (s *SQLiteStore) AdminListAttachments(ctx context.Context, query registrystore.AdminAttachmentQuery) ([]registrystore.AdminAttachment, *string, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}

	tx := s.dbFor(ctx).Table("attachments AS a").
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

func (s *SQLiteStore) AdminGetAttachment(ctx context.Context, attachmentID uuid.UUID) (*registrystore.AdminAttachment, error) {
	type row struct {
		model.Attachment
		RefCount int64 `gorm:"column:ref_count"`
	}

	var r row
	err := s.dbFor(ctx).Table("attachments AS a").
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

func (s *SQLiteStore) AdminDeleteAttachment(ctx context.Context, attachmentID uuid.UUID) error {
	result := s.writeDBFor(ctx, "sqlite store admin delete attachment").Where("id = ?", attachmentID).Delete(&model.Attachment{})
	if result.Error != nil {
		return fmt.Errorf("admin delete attachment failed: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return &NotFoundError{Resource: "attachment", ID: attachmentID.String()}
	}
	return nil
}

// --- Eviction ---

func (s *SQLiteStore) FindEvictableGroupIDs(ctx context.Context, cutoff time.Time, limit int) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := s.dbFor(ctx).
		Model(&model.ConversationGroup{}).
		Where("deleted_at IS NOT NULL AND deleted_at < ?", cutoff).
		Limit(limit).
		Pluck("id", &ids).Error
	return ids, err
}

func (s *SQLiteStore) CountEvictableGroups(ctx context.Context, cutoff time.Time) (int64, error) {
	var count int64
	err := s.dbFor(ctx).
		Model(&model.ConversationGroup{}).
		Where("deleted_at IS NOT NULL AND deleted_at < ?", cutoff).
		Count(&count).Error
	return count, err
}

func (s *SQLiteStore) HardDeleteConversationGroups(ctx context.Context, groupIDs []uuid.UUID) error {
	// ON DELETE CASCADE handles entries and conversations
	return s.writeDBFor(ctx, "sqlite store hard delete conversation groups").Where("id IN ?", groupIDs).Delete(&model.ConversationGroup{}).Error
}

func (s *SQLiteStore) CreateTask(ctx context.Context, taskType string, taskBody map[string]interface{}) error {
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
	err := s.writeDBFor(ctx, "sqlite store create task").Create(&task).Error
	if err == nil {
		return nil
	}
	if taskName != nil {
		if _, ok := sqliteUniqueViolation(err); ok {
			// Singleton task already exists; idempotent no-op.
			return nil
		}
	}
	return err
}

func (s *SQLiteStore) ClaimReadyTasks(ctx context.Context, limit int) ([]model.Task, error) {
	tx := s.writeDBFor(ctx, "sqlite store claim ready tasks")
	now := time.Now().UTC()
	retryAt := now.Add(5 * time.Minute)

	var tasks []model.Task
	if err := tx.
		Where("retry_at <= ?", now).
		Order("retry_at ASC, created_at ASC").
		Limit(limit).
		Find(&tasks).Error; err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return tasks, nil
	}

	ids := make([]uuid.UUID, len(tasks))
	for i, task := range tasks {
		ids[i] = task.ID
	}
	if err := tx.Model(&model.Task{}).
		Where("id IN ?", ids).
		Update("retry_at", retryAt).Error; err != nil {
		return nil, err
	}
	return tasks, nil
}

func (s *SQLiteStore) DeleteTask(ctx context.Context, taskID uuid.UUID) error {
	return s.writeDBFor(ctx, "sqlite store delete task").Where("id = ?", taskID).Delete(&model.Task{}).Error
}

func (s *SQLiteStore) FailTask(ctx context.Context, taskID uuid.UUID, errMsg string, retryDelay time.Duration) error {
	return s.writeDBFor(ctx, "sqlite store fail task").Model(&model.Task{}).Where("id = ?", taskID).Updates(map[string]interface{}{
		"retry_count": gorm.Expr("retry_count + 1"),
		"retry_at":    time.Now().Add(retryDelay),
		"last_error":  errMsg,
	}).Error
}

func (s *SQLiteStore) AdminGetAttachmentByStorageKey(ctx context.Context, storageKey string) (*registrystore.AdminAttachment, error) {
	type row struct {
		model.Attachment
		RefCount int64 `gorm:"column:ref_count"`
	}

	var r row
	err := s.dbFor(ctx).Table("attachments AS a").
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

func (s *SQLiteStore) requireAccess(ctx context.Context, userID string, groupID uuid.UUID, minLevel model.AccessLevel) (model.AccessLevel, error) {
	var m model.ConversationMembership
	result := s.dbFor(ctx).
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
func (s *SQLiteStore) fetchLatestMemoryEntries(ctx context.Context, conv model.Conversation, ancestry []forkAncestor, clientID string) ([]model.Entry, error) {
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
func (s *SQLiteStore) warmEntriesCache(ctx context.Context, conv model.Conversation, ancestry []forkAncestor, clientID string) {
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

func (s *SQLiteStore) listEntriesForGroup(ctx context.Context, groupID uuid.UUID) ([]model.Entry, error) {
	var entries []model.Entry
	if err := s.dbFor(ctx).
		Where("conversation_group_id = ?", groupID).
		Order("created_at ASC").
		Find(&entries).Error; err != nil {
		return nil, fmt.Errorf("failed to list entries: %w", err)
	}
	return entries, nil
}

func (s *SQLiteStore) buildAncestryStack(ctx context.Context, target model.Conversation) ([]forkAncestor, error) {
	var conversations []model.Conversation
	if err := s.dbFor(ctx).
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

func advanceForkAncestorForNilStop(ancestry []forkAncestor, ancestorIndex *int, current *forkAncestor, isTarget *bool) bool {
	// Nil stop points mean "exclude all inherited entries from this ancestor".
	for !*isTarget && current.StopAtEntryID == nil {
		*ancestorIndex = *ancestorIndex + 1
		if *ancestorIndex >= len(ancestry) {
			return false
		}
		*current = ancestry[*ancestorIndex]
		*isTarget = *ancestorIndex == len(ancestry)-1
	}
	return true
}

func filterEntriesByAncestry(allEntries []model.Entry, ancestry []forkAncestor) []model.Entry {
	if len(ancestry) == 0 {
		return allEntries
	}

	result := make([]model.Entry, 0, len(allEntries))
	ancestorIndex := 0
	current := ancestry[ancestorIndex]
	isTarget := ancestorIndex == len(ancestry)-1
	if !advanceForkAncestorForNilStop(ancestry, &ancestorIndex, &current, &isTarget) {
		return result
	}

	for _, entry := range allEntries {
		if entry.ConversationID != current.ConversationID {
			continue
		}

		if !isTarget && current.StopAtEntryID != nil && entry.ID == *current.StopAtEntryID {
			ancestorIndex++
			if ancestorIndex < len(ancestry) {
				current = ancestry[ancestorIndex]
				isTarget = ancestorIndex == len(ancestry)-1
				if !advanceForkAncestorForNilStop(ancestry, &ancestorIndex, &current, &isTarget) {
					break
				}
			}
			continue
		}
		result = append(result, entry)
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
	if !advanceForkAncestorForNilStop(ancestry, &ancestorIndex, &current, &isTarget) {
		return result
	}

	for _, entry := range allEntries {
		if entry.ConversationID != current.ConversationID {
			continue
		}

		if !isTarget && current.StopAtEntryID != nil && entry.ID == *current.StopAtEntryID {
			ancestorIndex++
			if ancestorIndex < len(ancestry) {
				current = ancestry[ancestorIndex]
				isTarget = ancestorIndex == len(ancestry)-1
				if !advanceForkAncestorForNilStop(ancestry, &ancestorIndex, &current, &isTarget) {
					break
				}
			}
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

func decryptEntries(s *SQLiteStore, entries []model.Entry) {
	for i := range entries {
		if decrypted, err := s.decrypt(entries[i].Content); err == nil {
			entries[i].Content = decrypted
		}
	}
}

func flattenMemoryContent(s *SQLiteStore, entries []model.Entry) []any {
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

func (s *SQLiteStore) CreateAttachment(ctx context.Context, userID string, conversationID uuid.UUID, attachment model.Attachment) (*model.Attachment, error) {
	db := s.writeDBFor(ctx, "sqlite store create attachment")
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
	if err := db.Create(&attachment).Error; err != nil {
		return nil, fmt.Errorf("create attachment failed: %w", err)
	}
	return &attachment, nil
}

func (s *SQLiteStore) UpdateAttachment(ctx context.Context, userID string, attachmentID uuid.UUID, update registrystore.AttachmentUpdate) (*model.Attachment, error) {
	db := s.writeDBFor(ctx, "sqlite store update attachment")
	var attachment model.Attachment
	if err := db.Where("id = ? AND deleted_at IS NULL", attachmentID).First(&attachment).Error; err != nil {
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
		if err := db.Model(&model.Attachment{}).Where("id = ?", attachmentID).Updates(values).Error; err != nil {
			return nil, fmt.Errorf("update attachment failed: %w", err)
		}
	}

	if err := db.Where("id = ? AND deleted_at IS NULL", attachmentID).First(&attachment).Error; err != nil {
		return nil, &NotFoundError{Resource: "attachment", ID: attachmentID.String()}
	}
	return &attachment, nil
}

func (s *SQLiteStore) ListAttachments(ctx context.Context, userID string, conversationID uuid.UUID, afterCursor *string, limit int) ([]model.Attachment, *string, error) {
	tx := s.dbFor(ctx).Where("deleted_at IS NULL")

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

func (s *SQLiteStore) GetAttachment(ctx context.Context, userID string, conversationID uuid.UUID, attachmentID uuid.UUID) (*model.Attachment, error) {
	var attachment model.Attachment
	if err := s.dbFor(ctx).Where("id = ? AND deleted_at IS NULL", attachmentID).First(&attachment).Error; err != nil {
		return nil, &NotFoundError{Resource: "attachment", ID: attachmentID.String()}
	}

	// Unlinked attachments are only visible to the uploader.
	if attachment.EntryID == nil {
		if attachment.UserID != userID {
			return nil, &ForbiddenError{}
		}
		return &attachment, nil
	}

	tx := s.dbFor(ctx).Where("id = ?", *attachment.EntryID)
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

func (s *SQLiteStore) DeleteAttachment(ctx context.Context, userID string, conversationID uuid.UUID, attachmentID uuid.UUID) error {
	db := s.writeDBFor(ctx, "sqlite store delete attachment")
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

	result := db.Where("id = ?", attachmentID).Delete(&model.Attachment{})
	if result.Error != nil {
		return fmt.Errorf("delete attachment failed: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return &NotFoundError{Resource: "attachment", ID: attachmentID.String()}
	}
	return nil
}

func (s *SQLiteStore) getGroupID(ctx context.Context, userID string, conversationID uuid.UUID, minLevel model.AccessLevel) (uuid.UUID, error) {
	var conv model.Conversation
	if err := s.dbFor(ctx).Where("id = ? AND deleted_at IS NULL", conversationID).First(&conv).Error; err != nil {
		return uuid.Nil, &NotFoundError{Resource: "conversation", ID: conversationID.String()}
	}
	if _, err := s.requireAccess(ctx, userID, conv.ConversationGroupID, minLevel); err != nil {
		return uuid.Nil, err
	}
	return conv.ConversationGroupID, nil
}
