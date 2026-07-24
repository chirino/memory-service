//go:build !nosqlite

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/dataencryption"
	"github.com/chirino/memory-service/internal/model"
	"github.com/chirino/memory-service/internal/plugin/store/sqlentry"
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

	if err := sqliteRequireCurrentSchemaOrEmpty(ctx, handle.sqlDB); err != nil {
		return err
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

func sqliteRequireCurrentSchemaOrEmpty(ctx context.Context, db *sql.DB) error {
	const versionKey = "core_schema_version"
	const expectedVersion = "1"

	var hasMetadata bool
	if err := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'schema_metadata')").Scan(&hasMetadata); err != nil {
		return fmt.Errorf("migration: failed to inspect schema metadata: %w", err)
	}
	if !hasMetadata {
		var hasCoreTables bool
		err := db.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM sqlite_master
				WHERE type = 'table'
				  AND name IN (
				      'conversation_groups',
				      'conversations',
				      'conversation_memberships',
				      'entries',
				      'tasks',
				      'outbox_events',
				      'attachments',
				      'memories',
				      'memory_usage_stats',
				      'memory_vectors'
				  )
			)
		`).Scan(&hasCoreTables)
		if err != nil {
			return fmt.Errorf("migration: failed to inspect existing schema: %w", err)
		}
		if hasCoreTables {
			return fmt.Errorf("migration: existing incompatible SQLite schema detected; reset the datastore before applying schema version %s", expectedVersion)
		}
		return nil
	}

	var version string
	err := db.QueryRowContext(ctx, "SELECT value FROM schema_metadata WHERE key = ?", versionKey).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("migration: schema metadata is missing %s; reset the datastore before applying schema version %s", versionKey, expectedVersion)
	}
	if err != nil {
		return fmt.Errorf("migration: failed to read schema metadata: %w", err)
	}
	if version != expectedVersion {
		return fmt.Errorf("migration: unsupported SQLite schema version %s; reset the datastore before applying schema version %s", version, expectedVersion)
	}
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

func (s *SQLiteStore) OutboxEnabled() bool {
	return s != nil && s.cfg != nil && s.cfg.OutboxEnabled
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

func conversationIDPtrString(id *string) string {
	if id == nil {
		return ""
	}
	return string(*id)
}

const conversationTitleFieldDomain = "conversation.title"
const entryContentFieldDomain = "entry.content"

func (s *SQLiteStore) encryptConversationTitle(conversationID string, title string) ([]byte, error) {
	if s.enc == nil {
		return []byte(title), nil
	}
	return s.enc.EncryptField([]byte(title), conversationTitleFieldDomain, conversationID)
}

func (s *SQLiteStore) decryptConversationTitle(conversationID string, data []byte) (string, error) {
	if s.enc == nil || data == nil {
		return string(data), nil
	}
	plain, err := s.enc.DecryptField(data, conversationTitleFieldDomain, conversationID)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (s *SQLiteStore) encryptEntryContent(entryID uuid.UUID, content []byte) ([]byte, error) {
	if s.enc == nil || content == nil {
		return content, nil
	}
	return s.enc.EncryptField(content, entryContentFieldDomain, strings.ToLower(entryID.String()))
}

func (s *SQLiteStore) decryptEntryContent(entryID uuid.UUID, data []byte) ([]byte, error) {
	if s.enc == nil || data == nil {
		return data, nil
	}
	return s.enc.DecryptField(data, entryContentFieldDomain, strings.ToLower(entryID.String()))
}

// --- Conversations ---

func (s *SQLiteStore) CreateConversation(ctx context.Context, userID string, clientID string, title string, metadata map[string]interface{}, agentID *string, forkedAtConversationID *string, forkedAtEntryID *uuid.UUID) (*registrystore.ConversationDetail, error) {
	convID := string(uuid.NewString())
	return s.createConversationWithID(ctx, userID, clientID, convID, title, metadata, agentID, forkedAtConversationID, forkedAtEntryID, nil, nil)
}

func (s *SQLiteStore) CreateConversationWithID(ctx context.Context, userID string, clientID string, convID string, title string, metadata map[string]interface{}, agentID *string, forkedAtConversationID *string, forkedAtEntryID *uuid.UUID) (*registrystore.ConversationDetail, error) {
	return s.createConversationWithID(ctx, userID, clientID, convID, title, metadata, agentID, forkedAtConversationID, forkedAtEntryID, nil, nil)
}

func (s *SQLiteStore) createConversationWithID(ctx context.Context, userID string, clientID string, convID string, title string, metadata map[string]interface{}, agentID *string, forkedAtConversationID *string, forkedAtEntryID *uuid.UUID, startedByConversationID *string, startedByEntryID *uuid.UUID) (*registrystore.ConversationDetail, error) {
	db := s.writeDBFor(ctx, "sqlite store create conversation")
	groupID := uuid.New()
	now := time.Now()

	if metadata == nil {
		metadata = map[string]interface{}{}
	}

	if forkedAtConversationID != nil && startedByConversationID != nil {
		return nil, &registrystore.ValidationError{Field: "lineage", Message: "fork and started-by lineage cannot both be set"}
	}

	// If forking, look up the source conversation's group
	var actualGroupID uuid.UUID
	ownerUserID := userID
	var membershipsToCopy []model.ConversationMembership
	var sourceConv *model.Conversation
	var anchorOwnerDepth *int
	if forkedAtConversationID != nil {
		var parent model.Conversation
		if err := db.Where("id = ? AND archived_at IS NULL", *forkedAtConversationID).First(&parent).Error; err != nil {
			return nil, &registrystore.NotFoundError{Resource: "conversation", ID: string(*forkedAtConversationID)}
		}
		sourceConv = &parent
		// Verify user has access
		if _, err := s.requireAccess(ctx, userID, parent.ConversationGroupID, model.AccessLevelReader); err != nil {
			return nil, err
		}
		// Validate fork point entry exists
		if forkedAtEntryID != nil {
			var entry model.Entry
			if err := db.Where("id = ? AND conversation_group_id = ?", *forkedAtEntryID, parent.ConversationGroupID).First(&entry).Error; err != nil {
				return nil, &registrystore.NotFoundError{Resource: "entry", ID: forkedAtEntryID.String()}
			}
			if entry.Channel != model.ChannelHistory && entry.Channel != model.ChannelJournal {
				return nil, &registrystore.ValidationError{Field: "forkedAtEntryId", Message: "can only fork at history or journal entries"}
			}
			if entry.Channel == model.ChannelJournal {
				if clientID == "" || entry.ClientID == nil || *entry.ClientID != clientID {
					return nil, &registrystore.ForbiddenError{}
				}
			}
			ownerDepth, err := s.visibleAncestryDepthForEntry(ctx, parent, entry)
			if err != nil {
				return nil, err
			}
			if ownerDepth == nil {
				return nil, &registrystore.ValidationError{Field: "forkedAtEntryId", Message: "forkedAtEntryId must be visible in the parent conversation ancestry"}
			}
			anchorOwnerDepth = ownerDepth
		}
		actualGroupID = parent.ConversationGroupID
	} else if startedByConversationID != nil {
		var parentConv model.Conversation
		findResult := db.Where("id = ? AND archived_at IS NULL", *startedByConversationID).Limit(1).Find(&parentConv)
		if findResult.Error != nil {
			return nil, findResult.Error
		}
		if findResult.RowsAffected == 0 {
			return nil, &registrystore.NotFoundError{Resource: "conversation", ID: string(*startedByConversationID)}
		}
		if _, err := s.requireAccess(ctx, userID, parentConv.ConversationGroupID, model.AccessLevelWriter); err != nil {
			return nil, err
		}
		if startedByEntryID != nil {
			visible, err := s.entryVisibleInConversationAncestry(ctx, parentConv, *startedByEntryID)
			if err != nil {
				return nil, err
			}
			if !visible {
				return nil, &registrystore.ValidationError{Field: "startedByEntryId", Message: "startedByEntryId must be visible in the parent conversation ancestry"}
			}
		}
		actualGroupID = groupID
		ownerUserID = parentConv.OwnerUserID
		group := model.ConversationGroup{ID: actualGroupID, CreatedAt: now}
		if err := db.Create(&group).Error; err != nil {
			logDuplicateKey("createConversationWithID:createStartedGroup", err,
				"userID", userID,
				"conversationID", string(convID),
				"conversationGroupID", actualGroupID.String(),
				"startedByConversationID", conversationIDPtrString(startedByConversationID),
				"startedByEntryID", uuidPtrString(startedByEntryID),
			)
			return nil, fmt.Errorf("failed to create conversation group: %w", err)
		}
		if err := db.Where("conversation_group_id = ?", parentConv.ConversationGroupID).Order("created_at ASC").Find(&membershipsToCopy).Error; err != nil {
			return nil, fmt.Errorf("failed to load parent memberships: %w", err)
		}
	} else {
		actualGroupID = groupID
		group := model.ConversationGroup{ID: actualGroupID, CreatedAt: now}
		if err := db.Create(&group).Error; err != nil {
			logDuplicateKey("createConversationWithID:createGroup", err,
				"userID", userID,
				"conversationID", string(convID),
				"conversationGroupID", actualGroupID.String(),
				"forkedAtConversationID", conversationIDPtrString(forkedAtConversationID),
				"forkedAtEntryID", uuidPtrString(forkedAtEntryID),
			)
			return nil, fmt.Errorf("failed to create conversation group: %w", err)
		}
	}

	encTitle, err := s.encryptConversationTitle(string(convID), title)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt title: %w", err)
	}
	conv := model.Conversation{
		ID:                      convID,
		Title:                   encTitle,
		OwnerUserID:             ownerUserID,
		ClientID:                clientID,
		AgentID:                 agentID,
		Metadata:                metadata,
		ConversationGroupID:     actualGroupID,
		StartedByConversationID: startedByConversationID,
		StartedByEntryID:        startedByEntryID,
		CreatedAt:               now,
		UpdatedAt:               now,
	}

	if err := db.Create(&conv).Error; err != nil {
		logDuplicateKey("createConversationWithID:createConversation", err,
			"userID", userID,
			"conversationID", string(convID),
			"conversationGroupID", actualGroupID.String(),
			"forkedAtConversationID", conversationIDPtrString(forkedAtConversationID),
			"forkedAtEntryID", uuidPtrString(forkedAtEntryID),
		)
		return nil, fmt.Errorf("failed to create conversation: %w", err)
	}

	if err := s.createConversationAncestry(ctx, db, actualGroupID, convID, sourceConv, forkedAtEntryID, anchorOwnerDepth); err != nil {
		return nil, err
	}

	// Root conversations get a new owner membership; started conversations copy parent memberships.
	if forkedAtConversationID == nil {
		if len(membershipsToCopy) == 0 {
			membershipsToCopy = []model.ConversationMembership{{
				ConversationGroupID: actualGroupID,
				UserID:              ownerUserID,
				AccessLevel:         model.AccessLevelOwner,
				CreatedAt:           now,
			}}
		} else {
			for i := range membershipsToCopy {
				membershipsToCopy[i].ConversationGroupID = actualGroupID
				membershipsToCopy[i].CreatedAt = now
			}
		}
		if err := db.Create(&membershipsToCopy).Error; err != nil {
			logDuplicateKey("createConversationWithID:createMemberships", err,
				"userID", userID,
				"conversationID", string(convID),
				"conversationGroupID", actualGroupID.String(),
			)
			return nil, fmt.Errorf("failed to create memberships: %w", err)
		}
	}

	return &registrystore.ConversationDetail{
		ConversationSummary: registrystore.ConversationSummary{
			ID:                      convID,
			Title:                   title,
			OwnerUserID:             ownerUserID,
			ClientID:                clientID,
			AgentID:                 agentID,
			Metadata:                metadata,
			ConversationGroupID:     actualGroupID,
			ForkedAtConversationID:  forkedAtConversationID,
			ForkedAtEntryID:         forkedAtEntryID,
			StartedByConversationID: startedByConversationID,
			StartedByEntryID:        startedByEntryID,
			CreatedAt:               now,
			UpdatedAt:               now,
			AccessLevel:             model.AccessLevelOwner,
		},
	}, nil
}

func (s *SQLiteStore) ListConversations(ctx context.Context, userID string, query *string, afterCursor *string, limit int, mode model.ConversationListMode, ancestry model.ConversationAncestryFilter, archived registrystore.ArchiveFilter) ([]registrystore.ConversationSummary, *string, error) {
	requestedLimit := limit
	queryStr := ""
	if query != nil {
		queryStr = strings.TrimSpace(*query)
	}

	tx := s.dbFor(ctx).
		Table("conversations c").
		Select(conversationSelectColumns+", cm.access_level").
		Joins("JOIN conversation_memberships cm ON cm.conversation_group_id = c.conversation_group_id AND cm.user_id = ?", userID).
		Joins("JOIN conversation_groups cg ON cg.id = c.conversation_group_id")
	tx = joinDirectConversationAncestry(tx)

	switch archived {
	case registrystore.ArchiveFilterInclude:
	case registrystore.ArchiveFilterOnly:
		tx = tx.Where("c.archived_at IS NOT NULL")
	default:
		tx = tx.Where("c.archived_at IS NULL")
	}

	switch mode {
	case model.ListModeRoots:
		tx = tx.Where("ca_direct.ancestor_conversation_id IS NULL")
	case model.ListModeLatestFork:
		subquery := "SELECT MAX(c2.updated_at) FROM conversations c2 WHERE c2.conversation_group_id = c.conversation_group_id"
		if archived == registrystore.ArchiveFilterOnly {
			subquery += " AND c2.archived_at IS NOT NULL"
		} else if archived != registrystore.ArchiveFilterInclude {
			subquery += " AND c2.archived_at IS NULL"
		}
		tx = tx.Where("c.updated_at = (" + subquery + ")")
	}
	switch ancestry {
	case model.ConversationAncestryChildren:
		tx = tx.Where("c.started_by_conversation_id IS NOT NULL")
	case model.ConversationAncestryAll:
	default:
		tx = tx.Where("c.started_by_conversation_id IS NULL")
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
		ID                      string                 `gorm:"column:id"`
		Title                   []byte                 `gorm:"column:title"`
		OwnerUserID             string                 `gorm:"column:owner_user_id"`
		Metadata                map[string]interface{} `gorm:"column:metadata;serializer:json"`
		ConversationGroupID     uuid.UUID              `gorm:"column:conversation_group_id"`
		ForkedAtEntryID         *uuid.UUID             `gorm:"column:forked_at_entry_id"`
		ForkedAtConversationID  *string                `gorm:"column:forked_at_conversation_id"`
		StartedByConversationID *string                `gorm:"column:started_by_conversation_id"`
		StartedByEntryID        *uuid.UUID             `gorm:"column:started_by_entry_id"`
		CreatedAt               time.Time              `gorm:"column:created_at"`
		UpdatedAt               time.Time              `gorm:"column:updated_at"`
		ArchivedAt              *time.Time             `gorm:"column:archived_at"`
		AccessLevel             model.AccessLevel      `gorm:"column:access_level"`
	}
	var rows []row
	if err := tx.Scan(&rows).Error; err != nil {
		return nil, nil, fmt.Errorf("failed to list conversations: %w", err)
	}

	if queryStr != "" {
		lq := strings.ToLower(queryStr)
		filtered := rows[:0]
		for _, r := range rows {
			title, err := s.decryptConversationTitle(r.ID, r.Title)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to decrypt conversation title: %w", err)
			}
			if strings.Contains(strings.ToLower(title), lq) {
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
		title, err := s.decryptConversationTitle(r.ID, r.Title)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to decrypt conversation title: %w", err)
		}
		summaries[i] = registrystore.ConversationSummary{
			ID:                      r.ID,
			Title:                   title,
			OwnerUserID:             r.OwnerUserID,
			Metadata:                r.Metadata,
			ConversationGroupID:     r.ConversationGroupID,
			ForkedAtEntryID:         r.ForkedAtEntryID,
			ForkedAtConversationID:  r.ForkedAtConversationID,
			StartedByConversationID: r.StartedByConversationID,
			StartedByEntryID:        r.StartedByEntryID,
			CreatedAt:               r.CreatedAt,
			UpdatedAt:               r.UpdatedAt,
			ArchivedAt:              r.ArchivedAt,
			AccessLevel:             r.AccessLevel,
		}
	}

	var cursor *string
	if hasMore && len(summaries) > 0 {
		c := string(summaries[len(summaries)-1].ID)
		cursor = &c
	}
	return summaries, cursor, nil
}

func (s *SQLiteStore) GetConversation(ctx context.Context, userID string, conversationID string) (*registrystore.ConversationDetail, error) {
	var conv model.Conversation
	if err := s.dbFor(ctx).Where("id = ?", conversationID).First(&conv).Error; err != nil {
		return nil, &registrystore.NotFoundError{Resource: "conversation", ID: string(conversationID)}
	}
	access, err := s.requireAccess(ctx, userID, conv.ConversationGroupID, model.AccessLevelReader)
	if err != nil {
		return nil, err
	}
	if err := s.hydrateConversationFork(ctx, &conv); err != nil {
		return nil, err
	}
	title, err := s.decryptConversationTitle(conv.ID, conv.Title)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt conversation title: %w", err)
	}

	return &registrystore.ConversationDetail{
		ConversationSummary: registrystore.ConversationSummary{
			ID:                      conv.ID,
			Title:                   title,
			OwnerUserID:             conv.OwnerUserID,
			ClientID:                conv.ClientID,
			AgentID:                 conv.AgentID,
			Metadata:                conv.Metadata,
			ConversationGroupID:     conv.ConversationGroupID,
			ForkedAtConversationID:  conv.ForkedAtConversationID,
			ForkedAtEntryID:         conv.ForkedAtEntryID,
			StartedByConversationID: conv.StartedByConversationID,
			StartedByEntryID:        conv.StartedByEntryID,
			CreatedAt:               conv.CreatedAt,
			UpdatedAt:               conv.UpdatedAt,
			ArchivedAt:              conv.ArchivedAt,
			AccessLevel:             access,
		},
	}, nil
}

func (s *SQLiteStore) UpdateConversation(ctx context.Context, userID string, conversationID string, title *string, metadata map[string]interface{}) (*registrystore.ConversationDetail, error) {
	db := s.writeDBFor(ctx, "sqlite store update conversation")
	var conv model.Conversation
	if err := db.Where("id = ? AND archived_at IS NULL", conversationID).First(&conv).Error; err != nil {
		return nil, &registrystore.NotFoundError{Resource: "conversation", ID: string(conversationID)}
	}
	if _, err := s.requireAccess(ctx, userID, conv.ConversationGroupID, model.AccessLevelWriter); err != nil {
		return nil, err
	}

	updates := map[string]interface{}{"updated_at": time.Now()}
	if title != nil {
		encTitle, err := s.encryptConversationTitle(string(conversationID), *title)
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

func (s *SQLiteStore) ArchiveConversation(ctx context.Context, userID string, conversationID string) error {
	db := s.writeDBFor(ctx, "sqlite store archive conversation")
	var conv model.Conversation
	if err := db.Where("id = ? AND archived_at IS NULL", conversationID).First(&conv).Error; err != nil {
		return &registrystore.NotFoundError{Resource: "conversation", ID: string(conversationID)}
	}
	if _, err := s.requireAccess(ctx, userID, conv.ConversationGroupID, model.AccessLevelOwner); err != nil {
		return err
	}

	now := time.Now()
	return db.Transaction(func(tx *gorm.DB) error {
		groupIDs, err := s.startedConversationGroupIDsForDelete(ctx, conv.ConversationGroupID)
		if err != nil {
			return err
		}
		// Archive the conversation group and its fork tree. Entries and memberships remain until eviction.
		if err := tx.Model(&model.ConversationGroup{}).
			Where("id IN ?", groupIDs).
			Update("archived_at", now).Error; err != nil {
			return fmt.Errorf("failed to archive group: %w", err)
		}
		if err := tx.Model(&model.Conversation{}).
			Where("conversation_group_id IN ? AND archived_at IS NULL", groupIDs).
			Update("archived_at", now).Error; err != nil {
			return fmt.Errorf("failed to archive conversations: %w", err)
		}
		return nil
	})
}

func (s *SQLiteStore) UnarchiveConversation(ctx context.Context, userID string, conversationID string) error {
	db := s.writeDBFor(ctx, "sqlite store unarchive conversation")
	var conv model.Conversation
	result := db.Where("id = ? AND archived_at IS NOT NULL", conversationID).Limit(1).Find(&conv)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return &registrystore.NotFoundError{Resource: "conversation", ID: string(conversationID)}
	}
	if _, err := s.requireAccess(ctx, userID, conv.ConversationGroupID, model.AccessLevelOwner); err != nil {
		return err
	}

	return db.Transaction(func(tx *gorm.DB) error {
		groupIDs, err := s.startedConversationGroupIDsForDelete(ctx, conv.ConversationGroupID)
		if err != nil {
			return err
		}
		if err := tx.Model(&model.ConversationGroup{}).
			Where("id IN ?", groupIDs).
			Update("archived_at", nil).Error; err != nil {
			return fmt.Errorf("failed to unarchive group: %w", err)
		}
		if err := tx.Model(&model.Conversation{}).
			Where("conversation_group_id IN ? AND archived_at IS NOT NULL", groupIDs).
			Update("archived_at", nil).Error; err != nil {
			return fmt.Errorf("failed to unarchive conversations: %w", err)
		}
		return nil
	})
}

// --- Memberships ---

func (s *SQLiteStore) ListMemberships(ctx context.Context, userID string, conversationID string, afterCursor *string, limit int) ([]model.ConversationMembership, *string, error) {
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

func (s *SQLiteStore) ShareConversation(ctx context.Context, userID string, conversationID string, targetUserID string, accessLevel model.AccessLevel) (*model.ConversationMembership, error) {
	groupID, err := s.getGroupID(ctx, userID, conversationID, model.AccessLevelManager)
	if err != nil {
		return nil, err
	}
	if accessLevel == model.AccessLevelOwner {
		return nil, &registrystore.ValidationError{Field: "accessLevel", Message: "cannot share with owner access; use ownership transfer"}
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
			return nil, &registrystore.ConflictError{Message: "user already has access to this conversation"}
		}
		return nil, fmt.Errorf("failed to share conversation: %w", result.Error)
	}
	return &membership, nil
}

func (s *SQLiteStore) UpdateMembership(ctx context.Context, userID string, conversationID string, memberUserID string, accessLevel model.AccessLevel) (*model.ConversationMembership, error) {
	groupID, err := s.getGroupID(ctx, userID, conversationID, model.AccessLevelManager)
	if err != nil {
		return nil, err
	}
	if accessLevel == model.AccessLevelOwner {
		return nil, &registrystore.ValidationError{Field: "accessLevel", Message: "cannot set owner access; use ownership transfer"}
	}

	db := s.writeDBFor(ctx, "sqlite store update membership")
	result := db.Model(&model.ConversationMembership{}).
		Where("conversation_group_id = ? AND user_id = ?", groupID, memberUserID).
		Update("access_level", accessLevel)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to update membership: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return nil, &registrystore.NotFoundError{Resource: "membership", ID: memberUserID}
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
		return nil, &registrystore.NotFoundError{Resource: "membership", ID: memberUserID}
	}
	return &m, nil
}

func (s *SQLiteStore) DeleteMembership(ctx context.Context, userID string, conversationID string, memberUserID string) error {
	db := s.writeDBFor(ctx, "sqlite store delete membership")
	groupID, err := s.getGroupID(ctx, userID, conversationID, model.AccessLevelManager)
	if err != nil {
		return err
	}
	// Cannot delete the owner
	var m model.ConversationMembership
	if err := db.Where("conversation_group_id = ? AND user_id = ?", groupID, memberUserID).First(&m).Error; err != nil {
		return &registrystore.NotFoundError{Resource: "membership", ID: memberUserID}
	}
	if m.AccessLevel == model.AccessLevelOwner {
		return &registrystore.ValidationError{Field: "userId", Message: "cannot remove the owner"}
	}

	// Java parity: removing the pending transfer recipient cancels the transfer.
	db.
		Where("conversation_group_id = ? AND to_user_id = ?", groupID, memberUserID).
		Delete(&model.OwnershipTransfer{})

	db.Where("conversation_group_id = ? AND user_id = ?", groupID, memberUserID).Delete(&model.ConversationMembership{})
	return nil
}

func (s *SQLiteStore) GetGroupMemberUserIDs(ctx context.Context, conversationGroupID uuid.UUID) ([]string, error) {
	var userIDs []string
	err := s.dbFor(ctx).
		Model(&model.ConversationMembership{}).
		Where("conversation_group_id = ?", conversationGroupID).
		Pluck("user_id", &userIDs).Error
	return userIDs, err
}

// --- Forks ---

func (s *SQLiteStore) ListForks(ctx context.Context, userID string, conversationID string, clientID *string) (*registrystore.ConversationForkNavigation, error) {
	return s.listForks(ctx, userID, conversationID, registrystore.ForkNavigationVisibility{ClientID: clientID})
}

func (s *SQLiteStore) listForks(ctx context.Context, userID string, conversationID string, visibility registrystore.ForkNavigationVisibility) (*registrystore.ConversationForkNavigation, error) {
	var conv model.Conversation
	result := s.dbFor(ctx).Where("id = ?", conversationID).Limit(1).Find(&conv)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, &registrystore.NotFoundError{Resource: "conversation", ID: string(conversationID)}
	}
	if _, err := s.requireAccess(ctx, userID, conv.ConversationGroupID, model.AccessLevelReader); err != nil {
		return nil, err
	}
	db := s.dbFor(ctx)
	var convs []model.Conversation
	if err := db.Where("conversation_group_id = ?", conv.ConversationGroupID).Order("created_at ASC, id ASC").Find(&convs).Error; err != nil {
		return nil, err
	}
	var directRows []model.ConversationAncestry
	if err := db.Where("conversation_group_id = ? AND depth = 1", conv.ConversationGroupID).Find(&directRows).Error; err != nil {
		return nil, err
	}
	directByChild := map[string]model.ConversationAncestry{}
	for _, row := range directRows {
		directByChild[row.DescendantConversationID] = row
	}
	var ancestry []model.ConversationAncestry
	if err := db.Where("conversation_group_id = ? AND descendant_conversation_id = ?", conv.ConversationGroupID, conversationID).Find(&ancestry).Error; err != nil {
		return nil, err
	}
	entryIDs := []uuid.UUID{}
	for _, row := range directRows {
		if row.ForkedAtEntryID != nil {
			entryIDs = append(entryIDs, *row.ForkedAtEntryID)
		}
	}
	for _, row := range ancestry {
		if row.BeforeEntryID != nil {
			entryIDs = append(entryIDs, *row.BeforeEntryID)
		}
	}
	var entries []model.Entry
	if len(entryIDs) > 0 {
		if err := db.Where("conversation_group_id = ? AND id IN ?", conv.ConversationGroupID, entryIDs).Find(&entries).Error; err != nil {
			return nil, err
		}
	}
	if err := decryptEntries(s, entries); err != nil {
		return nil, err
	}
	var firstEntries []model.Entry
	firstEntryPredicate := "e.channel = ?"
	firstEntryArgs := []any{conv.ConversationGroupID, model.ChannelHistory}
	if visibility.IncludeAllJournals {
		firstEntryPredicate = "e.channel IN (?, ?)"
		firstEntryArgs = []any{conv.ConversationGroupID, model.ChannelHistory, model.ChannelJournal}
	} else if visibility.ClientID != nil {
		firstEntryPredicate = "(e.channel = ? OR (e.channel = ? AND e.client_id = ?))"
		firstEntryArgs = []any{conv.ConversationGroupID, model.ChannelHistory, model.ChannelJournal, *visibility.ClientID}
	}
	firstEntrySQL := fmt.Sprintf(`SELECT * FROM (
		SELECT e.*, ROW_NUMBER() OVER (PARTITION BY e.conversation_id ORDER BY e.created_at ASC, CASE WHEN e.seq IS NULL THEN 0 ELSE 1 END ASC, e.seq ASC, e.id ASC) AS fork_row
		FROM entries e WHERE e.conversation_group_id = ? AND %s
	) ranked WHERE fork_row = 1`, firstEntryPredicate)
	if err := db.Raw(firstEntrySQL, firstEntryArgs...).Scan(&firstEntries).Error; err != nil {
		return nil, err
	}
	if err := decryptEntries(s, firstEntries); err != nil {
		return nil, err
	}
	entryMap := map[uuid.UUID]model.Entry{}
	for _, entry := range entries {
		entryMap[entry.ID] = entry
	}
	firstByConversation := map[string]model.Entry{}
	for _, entry := range firstEntries {
		entryMap[entry.ID] = entry
		firstByConversation[entry.ConversationID] = entry
	}
	records := make([]registrystore.ForkNavigationConversation, 0, len(convs))
	for _, c := range convs {
		title, err := s.decryptConversationTitle(c.ID, c.Title)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt conversation title: %w", err)
		}
		record := registrystore.ForkNavigationConversation{ID: c.ID, Title: title, CreatedAt: c.CreatedAt}
		if direct, ok := directByChild[c.ID]; ok {
			parent := direct.AncestorConversationID
			record.ForkedAtConversationID = &parent
			record.ForkedAtEntryID = direct.ForkedAtEntryID
		}
		if entry, ok := firstByConversation[c.ID]; ok {
			id, at := entry.ID, entry.CreatedAt
			record.FirstEntryID, record.FirstEntryCreatedAt, record.FirstEntryPreview = &id, &at, registrystore.ForkEntryPreview(entry.Content)
		}
		records = append(records, record)
	}
	return registrystore.BuildForkNavigation(records, ancestry, entryMap, visibility)
}

func (s *SQLiteStore) ListChildConversations(ctx context.Context, userID string, conversationID string, afterCursor *string, limit int) ([]registrystore.ConversationSummary, *string, error) {
	var conv model.Conversation
	result := s.dbFor(ctx).Where("id = ?", conversationID).Limit(1).Find(&conv)
	if result.Error != nil {
		return nil, nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil, &registrystore.NotFoundError{Resource: "conversation", ID: string(conversationID)}
	}
	if _, err := s.requireAccess(ctx, userID, conv.ConversationGroupID, model.AccessLevelReader); err != nil {
		return nil, nil, err
	}
	tx := s.dbFor(ctx).
		Table("conversations c").
		Select(conversationSelectColumns+", cm.access_level").
		Joins("JOIN conversation_memberships cm ON cm.conversation_group_id = c.conversation_group_id AND cm.user_id = ?", userID).
		Joins("JOIN conversation_groups cg ON cg.id = c.conversation_group_id").
		Where("c.started_by_conversation_id = ?", conversationID)
	tx = joinDirectConversationAncestry(tx)
	return s.listChildConversationsForBase(ctx, tx, afterCursor, limit)
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
		return nil, &registrystore.NotFoundError{Resource: "transfer", ID: transferID.String()}
	}
	if t.FromUserID != userID && t.ToUserID != userID {
		return nil, &registrystore.NotFoundError{Resource: "transfer", ID: transferID.String()}
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
func (s *SQLiteStore) resolveConversationID(ctx context.Context, groupID uuid.UUID) string {
	var conv model.Conversation
	if err := s.dbFor(ctx).Where("conversation_group_id = ? AND archived_at IS NULL", groupID).First(&conv).Error; err != nil {
		return ""
	}
	return conv.ID
}

func (s *SQLiteStore) CreateOwnershipTransfer(ctx context.Context, userID string, conversationID string, toUserID string) (*registrystore.OwnershipTransferDto, error) {
	db := s.writeDBFor(ctx, "sqlite store create ownership transfer")
	var conv model.Conversation
	if err := db.Where("id = ? AND archived_at IS NULL", conversationID).First(&conv).Error; err != nil {
		return nil, &registrystore.NotFoundError{Resource: "conversation", ID: string(conversationID)}
	}
	if _, err := s.requireAccess(ctx, userID, conv.ConversationGroupID, model.AccessLevelOwner); err != nil {
		return nil, err
	}
	if userID == toUserID {
		return nil, &registrystore.ValidationError{Field: "newOwnerUserId", Message: "cannot transfer to yourself"}
	}
	// Parity with Java behavior: recipient must already be a conversation member.
	var recipient model.ConversationMembership
	if err := db.
		Where("conversation_group_id = ? AND user_id = ?", conv.ConversationGroupID, toUserID).
		First(&recipient).Error; err != nil {
		return nil, &registrystore.ValidationError{Field: "newOwnerUserId", Message: "recipient must already be a member"}
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
				return nil, &registrystore.ConflictError{
					Message: "a transfer is already pending for this conversation",
					Code:    "TRANSFER_ALREADY_PENDING",
					Details: map[string]interface{}{"existingTransferId": existing.ID.String()},
				}
			}
			return nil, &registrystore.ConflictError{Message: "a transfer is already pending for this conversation", Code: "TRANSFER_ALREADY_PENDING"}
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
		return &registrystore.NotFoundError{Resource: "transfer", ID: transferID.String()}
	}
	if t.ToUserID != userID {
		return &registrystore.ForbiddenError{}
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
			Where("conversation_group_id = ? AND archived_at IS NULL", t.ConversationGroupID).
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
		return &registrystore.NotFoundError{Resource: "transfer", ID: transferID.String()}
	}
	if t.FromUserID != userID && t.ToUserID != userID {
		return &registrystore.ForbiddenError{}
	}
	db.Where("id = ?", transferID).Delete(&model.OwnershipTransfer{})
	return nil
}

// --- Entries ---

func (s *SQLiteStore) GetEntries(ctx context.Context, userID string, conversationID string, query registrystore.EntryListQuery) (*registrystore.PagedEntries, error) {
	var conv model.Conversation
	result := s.dbFor(ctx).Where("id = ?", conversationID).Limit(1).Find(&conv)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, &registrystore.NotFoundError{Resource: "conversation", ID: string(conversationID)}
	}
	if _, err := s.requireAccess(ctx, userID, conv.ConversationGroupID, model.AccessLevelReader); err != nil {
		return nil, err
	}
	if err := s.hydrateConversationFork(ctx, &conv); err != nil {
		return nil, err
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}

	afterEntryID := query.AfterCursor
	beforeEntryID := query.BeforeCursor
	tail := query.Tail
	upToEntryID := query.UpToEntryID
	clientID := query.ClientID
	agentID := query.AgentID
	allForks := query.AllForks
	fromSeq := query.FromSeq
	channel := query.Channel
	epochFilter := query.EpochFilter

	// channel==nil means "all channels" (agent without filter).
	// Determine effective channel for filtering.
	var effectiveChannel model.Channel
	if channel != nil {
		effectiveChannel = *channel
	}

	if (effectiveChannel == model.ChannelContext || effectiveChannel == model.ChannelJournal) && clientID == nil {
		return nil, &registrystore.ForbiddenError{}
	}
	if (effectiveChannel == model.ChannelContext || effectiveChannel == model.ChannelJournal) && conv.ClientID != "" && clientID != nil && conv.ClientID != *clientID {
		return nil, &registrystore.ForbiddenError{}
	}

	if allForks && effectiveChannel == model.ChannelHistory {
		page, afterCursor, beforeCursor, err := s.boundedGroupHistory(ctx, conv.ConversationGroupID, fromSeq, upToEntryID, afterEntryID, beforeEntryID, tail, limit)
		if err != nil {
			return nil, err
		}
		if err := decryptEntries(s, page); err != nil {
			return nil, err
		}
		return &registrystore.PagedEntries{Data: page, AfterCursor: afterCursor, BeforeCursor: beforeCursor}, nil
	}

	if allForks && effectiveChannel == model.ChannelContext {
		page, afterCursor, beforeCursor, err := s.boundedGroupContext(ctx, conv.ConversationGroupID, clientID, epochFilter, fromSeq, upToEntryID, afterEntryID, beforeEntryID, tail, limit)
		if err != nil {
			return nil, err
		}
		if err := decryptEntries(s, page); err != nil {
			return nil, err
		}
		return &registrystore.PagedEntries{Data: page, AfterCursor: afterCursor, BeforeCursor: beforeCursor}, nil
	}

	if allForks && effectiveChannel == model.ChannelJournal {
		page, afterCursor, beforeCursor, err := s.boundedGroupChannel(ctx, conv.ConversationGroupID, model.ChannelJournal, clientID, fromSeq, upToEntryID, afterEntryID, beforeEntryID, tail, limit)
		if err != nil {
			return nil, err
		}
		if err := decryptEntries(s, page); err != nil {
			return nil, err
		}
		return &registrystore.PagedEntries{Data: page, AfterCursor: afterCursor, BeforeCursor: beforeCursor}, nil
	}

	if allForks && effectiveChannel == "" {
		page, afterCursor, beforeCursor, err := s.boundedGroupAllChannels(ctx, conv.ConversationGroupID, clientID, true, epochFilter, fromSeq, upToEntryID, afterEntryID, beforeEntryID, tail, limit)
		if err != nil {
			return nil, err
		}
		if err := decryptEntries(s, page); err != nil {
			return nil, err
		}
		return &registrystore.PagedEntries{Data: page, AfterCursor: afterCursor, BeforeCursor: beforeCursor}, nil
	}

	if allForks {
		entries, err := s.listEntriesForGroup(ctx, conv.ConversationGroupID)
		if err != nil {
			return nil, err
		}
		visible := entries
		entries = filterEntriesForAllForks(entries, effectiveChannel, clientID, agentID, epochFilter)
		entries, err = registrystore.TrimEntriesToVisiblePrefix(entries, visible, upToEntryID)
		if err != nil {
			return nil, err
		}
		if fromSeq != nil {
			entries = filterEntriesByFromSeq(entries, *fromSeq)
		}
		page, afterCursor, beforeCursor, err := registrystore.PaginateEntries(entries, afterEntryID, beforeEntryID, tail, limit)
		if err != nil {
			return nil, &registrystore.BadRequestError{Message: err.Error()}
		}
		if err := decryptEntries(s, page); err != nil {
			return nil, err
		}
		return &registrystore.PagedEntries{Data: page, AfterCursor: afterCursor, BeforeCursor: beforeCursor}, nil
	}

	ancestry, err := s.buildAncestryStack(ctx, conv)
	if err != nil {
		return nil, err
	}

	if effectiveChannel == model.ChannelHistory && !allForks {
		page, afterCursor, beforeCursor, err := s.boundedVisibleHistory(ctx, conv, fromSeq, upToEntryID, afterEntryID, beforeEntryID, tail, limit)
		if err != nil {
			return nil, err
		}
		if err := decryptEntries(s, page); err != nil {
			return nil, err
		}
		return &registrystore.PagedEntries{Data: page, AfterCursor: afterCursor, BeforeCursor: beforeCursor}, nil
	}

	if effectiveChannel == model.ChannelContext && !allForks {
		page, afterCursor, beforeCursor, err := s.boundedVisibleContext(ctx, conv, clientID, epochFilter, fromSeq, upToEntryID, afterEntryID, beforeEntryID, tail, limit)
		if err != nil {
			return nil, err
		}
		if err := decryptEntries(s, page); err != nil {
			return nil, err
		}
		return &registrystore.PagedEntries{Data: page, AfterCursor: afterCursor, BeforeCursor: beforeCursor}, nil
	}

	if effectiveChannel == model.ChannelJournal && !allForks {
		page, afterCursor, beforeCursor, err := s.boundedVisibleChannel(ctx, conv, model.ChannelJournal, clientID, fromSeq, upToEntryID, afterEntryID, beforeEntryID, tail, limit)
		if err != nil {
			return nil, err
		}
		if err := decryptEntries(s, page); err != nil {
			return nil, err
		}
		return &registrystore.PagedEntries{Data: page, AfterCursor: afterCursor, BeforeCursor: beforeCursor}, nil
	}

	if effectiveChannel == "" && !allForks {
		page, afterCursor, beforeCursor, err := s.boundedVisibleAllChannels(ctx, conv, clientID, true, epochFilter, fromSeq, upToEntryID, afterEntryID, beforeEntryID, tail, limit)
		if err != nil {
			return nil, err
		}
		if err := decryptEntries(s, page); err != nil {
			return nil, err
		}
		return &registrystore.PagedEntries{Data: page, AfterCursor: afterCursor, BeforeCursor: beforeCursor}, nil
	}

	var visible []model.Entry
	ensureVisible := func() error {
		if visible != nil {
			return nil
		}
		var err error
		visible, err = s.listVisibleEntriesForConversation(ctx, conv)
		if err != nil {
			return err
		}
		return nil
	}

	var filtered []model.Entry
	if effectiveChannel == model.ChannelContext {
		// Context-only: filter context entries by epoch/clientID.
		// Use the cache for the common latest-epoch case.
		if epochFilter == nil || epochFilter.Mode == registrystore.MemoryEpochModeLatest {
			filtered, err = s.fetchLatestMemoryEntries(ctx, conv, ancestry, *clientID, valueOrEmpty(agentID))
			if err != nil {
				return nil, err
			}
		} else {
			if err := ensureVisible(); err != nil {
				return nil, err
			}
			filtered = filterVisibleMemoryEntriesWithEpoch(visible, *clientID, valueOrEmpty(agentID), epochFilter)
		}
	} else {
		if err := ensureVisible(); err != nil {
			return nil, err
		}
		if effectiveChannel == "" {
			// All channels (agent without filter): include scoped channels
			// only when they belong to the calling client.
			filtered = filterEntriesForAllChannels(visible, clientID)
		} else {
			// Single channel filter (or default history).
			filtered = visible
			if effectiveChannel != "" {
				tmp := make([]model.Entry, 0, len(filtered))
				for _, entry := range filtered {
					if entry.Channel != effectiveChannel {
						continue
					}
					// Journal entries are client-scoped: only return entries written by this client.
					if effectiveChannel == model.ChannelJournal && clientID != nil {
						if entry.ClientID == nil || *entry.ClientID != *clientID {
							continue
						}
					}
					tmp = append(tmp, entry)
				}
				filtered = tmp
			}
		}
	}

	if upToEntryID != nil {
		if err := ensureVisible(); err != nil {
			return nil, err
		}
		filtered, err = registrystore.TrimEntriesToVisiblePrefix(filtered, visible, upToEntryID)
		if err != nil {
			return nil, err
		}
	}

	// fromSeq: filter to seq >= fromSeq, exclude null-seq entries, sort by seq ASC.
	if fromSeq != nil {
		filtered = filterEntriesByFromSeq(filtered, *fromSeq)
	}

	page, afterCursor, beforeCursor, err := registrystore.PaginateEntries(filtered, afterEntryID, beforeEntryID, tail, limit)
	if err != nil {
		return nil, &registrystore.BadRequestError{Message: err.Error()}
	}
	if err := decryptEntries(s, page); err != nil {
		return nil, err
	}
	return &registrystore.PagedEntries{Data: page, AfterCursor: afterCursor, BeforeCursor: beforeCursor}, nil
}

func (s *SQLiteStore) GetEntryGroupID(ctx context.Context, entryID uuid.UUID) (uuid.UUID, error) {
	var entry model.Entry
	result := s.dbFor(ctx).Select("conversation_group_id").Where("id = ?", entryID).Limit(1).Find(&entry)
	if result.Error != nil {
		return uuid.Nil, result.Error
	}
	if result.RowsAffected == 0 {
		return uuid.Nil, &registrystore.NotFoundError{Resource: "entry", ID: entryID.String()}
	}
	return entry.ConversationGroupID, nil
}

func (s *SQLiteStore) AdminGetEntryByID(ctx context.Context, entryID uuid.UUID) (*model.Entry, error) {
	var entry model.Entry
	result := s.dbFor(ctx).Where("id = ?", entryID).First(&entry)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, &registrystore.NotFoundError{Resource: "entry", ID: entryID.String()}
		}
		return nil, result.Error
	}

	if decrypted, err := s.decryptEntryContent(entry.ID, entry.Content); err == nil {
		entry.Content = decrypted
	} else {
		return nil, fmt.Errorf("failed to decrypt entry content: %w", err)
	}

	return &entry, nil
}

func (s *SQLiteStore) AppendEntries(ctx context.Context, userID string, conversationID string, entries []registrystore.CreateEntryRequest, clientID *string, agentID *string, epoch *int64) ([]model.Entry, error) {
	if err := registrystore.ValidateEntryEpochChannels(entries, epoch); err != nil {
		return nil, &registrystore.ValidationError{Field: "epoch", Message: err.Error()}
	}
	db := s.writeDBFor(ctx, "sqlite store append entries")
	var conv model.Conversation
	convResult := db.Where("id = ? AND archived_at IS NULL", conversationID).Limit(1).Find(&conv)
	if convResult.Error != nil {
		return nil, convResult.Error
	}
	if convResult.RowsAffected == 0 {
		// Auto-create conversation if it doesn't exist (Java parity).
		// Check first entry for fork metadata.
		var forkedAtConvID *string
		var forkedAtEntryID *uuid.UUID
		var startedByConversationID *string
		var startedByEntryID *uuid.UUID
		if len(entries) > 0 {
			forkedAtConvID = entries[0].ForkedAtConversationID
			forkedAtEntryID = entries[0].ForkedAtEntryID
			startedByConversationID = entries[0].StartedByConversationID
			startedByEntryID = entries[0].StartedByEntryID
		}
		title := inferTitleFromEntries(entries)
		resolvedClientID := ""
		if clientID != nil {
			resolvedClientID = *clientID
		}
		detail, err := s.createConversationWithID(ctx, userID, resolvedClientID, conversationID, title, nil, agentID, forkedAtConvID, forkedAtEntryID, startedByConversationID, startedByEntryID)
		if err != nil {
			// Concurrent writers can race to auto-create the same root conversation.
			// If another request won the insert, load the conversation and continue.
			sqliteErr, ok := sqliteUniqueViolation(err)
			if !ok {
				return nil, err
			}
			log.Warn("append auto-create race detected",
				"userID", userID,
				"conversationID", string(conversationID),
				"sqliteCode", sqliteErr.Code,
				"sqliteExtendedCode", sqliteErr.ExtendedCode,
				"detail", sqliteErr.Error(),
				"forkedAtConversationID", conversationIDPtrString(forkedAtConvID),
				"forkedAtEntryID", uuidPtrString(forkedAtEntryID),
				"startedByConversationID", conversationIDPtrString(startedByConversationID),
				"startedByEntryID", uuidPtrString(startedByEntryID),
			)
			loaded := false
			for attempt := 0; attempt < 10; attempt++ {
				convResult = db.
					Where("id = ? AND archived_at IS NULL", conversationID).
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
			encTitle, err := s.encryptConversationTitle(detail.ID, detail.Title)
			if err != nil {
				return nil, fmt.Errorf("failed to encrypt title: %w", err)
			}
			conv = model.Conversation{
				ID:                      detail.ID,
				ConversationGroupID:     detail.ConversationGroupID,
				OwnerUserID:             detail.OwnerUserID,
				Title:                   encTitle,
				StartedByConversationID: detail.StartedByConversationID,
				StartedByEntryID:        detail.StartedByEntryID,
				CreatedAt:               detail.CreatedAt,
				UpdatedAt:               detail.UpdatedAt,
			}
		}
	}
	if _, err := s.requireAccess(ctx, userID, conv.ConversationGroupID, model.AccessLevelWriter); err != nil {
		return nil, err
	}
	if clientID != nil {
		for _, req := range entries {
			ch := model.Channel(strings.ToLower(req.Channel))
			if (ch == model.ChannelContext || ch == model.ChannelJournal) && conv.ClientID != "" && conv.ClientID != *clientID {
				return nil, &registrystore.ForbiddenError{}
			}
		}
	}

	now := time.Now()
	result := make([]model.Entry, len(entries))
	for i, req := range entries {
		ch := model.Channel(strings.ToLower(req.Channel))
		if ch == "" {
			ch = model.ChannelHistory
		}

		entryEpoch := registrystore.EpochForChannel(ch, epoch)
		entryID := uuid.New()

		encContent, err := s.encryptEntryContent(entryID, req.Content)
		if err != nil {
			return nil, fmt.Errorf("failed to encrypt entry content: %w", err)
		}
		entry := model.Entry{
			ID:                  entryID,
			ConversationID:      conversationID,
			ConversationGroupID: conv.ConversationGroupID,
			UserID:              &userID,
			ClientID:            clientID,
			AgentID:             agentID,
			Channel:             ch,
			Epoch:               entryEpoch,
			Seq:                 req.Seq,
			ContentType:         req.ContentType,
			Content:             encContent,
			IndexedContent:      req.IndexedContent,
			CreatedAt:           now,
		}
		if err := db.Create(&entry).Error; err != nil {
			if _, ok := sqliteUniqueViolation(err); ok {
				return nil, &registrystore.ConflictError{Message: "duplicate seq value in this conversation"}
			}
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
					encTitle, err := s.encryptConversationTitle(conversationID, title)
					if err != nil {
						return nil, fmt.Errorf("failed to encrypt title: %w", err)
					}
					db.Model(&model.Conversation{}).Where("id = ?", conversationID).Update("title", encTitle)
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
			if e.Channel == model.ChannelContext {
				if ancestry, err := s.buildAncestryStack(ctx, conv); err == nil {
					s.warmEntriesCache(ctx, conv, ancestry, *clientID, valueOrEmpty(agentID))
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

func (s *SQLiteStore) SyncAgentEntry(ctx context.Context, userID string, conversationID string, entry registrystore.CreateEntryRequest, clientID string, agentID *string) (*registrystore.SyncResult, error) {
	db := s.writeDBFor(ctx, "sqlite store sync agent entry")
	incomingContent := parseContentArray(entry.Content)

	autoCreated := false
	var conv model.Conversation
	result := db.Where("id = ? AND archived_at IS NULL", conversationID).Limit(1).Find(&conv)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		// Auto-create conversation if it does not exist and content is non-empty.
		if len(incomingContent) == 0 {
			return &registrystore.SyncResult{NoOp: true}, nil
		}
		var err error
		conv, err = s.autoCreateConversation(ctx, userID, clientID, conversationID, agentID)
		if err != nil {
			return nil, err
		}
		autoCreated = true
	}
	if _, err := s.requireAccess(ctx, userID, conv.ConversationGroupID, model.AccessLevelWriter); err != nil {
		return nil, err
	}
	if conv.ClientID != "" && conv.ClientID != clientID {
		return nil, &registrystore.ForbiddenError{}
	}
	if autoCreated && s.entriesCache != nil {
		if err := s.entriesCache.Remove(ctx, conversationID, scopedAgentCacheKey(clientID, valueOrEmpty(agentID))); err != nil {
			return nil, err
		}
	}

	ancestry, err := s.buildAncestryStack(ctx, conv)
	if err != nil {
		return nil, err
	}
	latestEpochEntries, err := s.fetchLatestMemoryEntries(ctx, conv, ancestry, clientID, valueOrEmpty(agentID))
	if err != nil {
		return nil, err
	}

	existingContent, err := flattenMemoryContent(s, latestEpochEntries)
	if err != nil {
		return nil, err
	}

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

	var appendContent json.RawMessage
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
	entryID := uuid.New()
	encContent, err := s.encryptEntryContent(entryID, appendContent)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt entry content: %w", err)
	}
	newEntry := model.Entry{
		ID:                  entryID,
		ConversationID:      conversationID,
		ConversationGroupID: conv.ConversationGroupID,
		UserID:              &userID,
		ClientID:            &clientID,
		AgentID:             agentID,
		Channel:             model.ChannelContext,
		Epoch:               &epochToUse,
		Seq:                 entry.Seq,
		ContentType:         entry.ContentType,
		Content:             encContent,
		IndexedContent:      entry.IndexedContent,
		CreatedAt:           now,
	}
	if err := db.Create(&newEntry).Error; err != nil {
		if _, ok := sqliteUniqueViolation(err); ok {
			return nil, &registrystore.ConflictError{Message: "duplicate seq value in this conversation"}
		}
		return nil, fmt.Errorf("failed to sync entry: %w", err)
	}
	newEntry.Content = appendContent
	s.warmEntriesCache(ctx, conv, ancestry, clientID, valueOrEmpty(agentID))
	return &registrystore.SyncResult{Entry: &newEntry, Epoch: &epochToUse, NoOp: false, EpochIncremented: epochIncremented}, nil
}

// autoCreateConversation creates a conversation with a given ID for sync auto-creation.
func (s *SQLiteStore) autoCreateConversation(ctx context.Context, userID string, clientID string, conversationID string, agentID *string) (model.Conversation, error) {
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
			"conversationID", string(conversationID),
			"conversationGroupID", groupID.String(),
		)
		return model.Conversation{}, fmt.Errorf("failed to create conversation group: %w", err)
	}

	conv := model.Conversation{
		ID:                  conversationID,
		ConversationGroupID: groupID,
		OwnerUserID:         userID,
		ClientID:            clientID,
		AgentID:             agentID,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if err := db.Create(&conv).Error; err != nil {
		// Clean up the orphaned group before handling the error.
		_ = db.Delete(&group).Error
		logDuplicateKey("autoCreateConversation:createConversation", err,
			"userID", userID,
			"conversationID", string(conversationID),
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
	if err := s.createConversationAncestry(ctx, db, groupID, conversationID, nil, nil, nil); err != nil {
		_ = db.Delete(&conv).Error
		_ = db.Delete(&group).Error
		return model.Conversation{}, err
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
		decrypted, err := s.decryptEntryContent(entries[i].ID, entries[i].Content)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to decrypt entry content: %w", err)
		}
		entries[i].Content = decrypted
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
		decrypted, err := s.decryptEntryContent(entries[i].ID, entries[i].Content)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt entry content: %w", err)
		}
		entries[i].Content = decrypted
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
		ConversationID    string    `gorm:"column:conversation_id"`
		ConversationTitle []byte    `gorm:"column:conversation_title"`
		IndexedContent    string    `gorm:"column:indexed_content"`
	}
	var rows []row
	err := s.dbFor(ctx).Raw(`
		SELECT e.id as entry_id, e.conversation_id, c.title as conversation_title, e.indexed_content
		FROM entries e
		JOIN conversations c ON c.id = e.conversation_id AND c.archived_at IS NULL
		JOIN conversation_memberships cm ON cm.conversation_group_id = c.conversation_group_id AND cm.user_id = ?
		WHERE e.id IN ?
	`, userID, entryIDs).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("fetch search result details failed: %w", err)
	}
	results := make([]registrystore.SearchResult, len(rows))
	for i, r := range rows {
		title, err := s.decryptConversationTitle(r.ConversationID, r.ConversationTitle)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt conversation title: %w", err)
		}
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
		JOIN conversations c ON c.id = e.conversation_id AND c.conversation_group_id = e.conversation_group_id AND c.archived_at IS NULL
		JOIN conversation_memberships cm ON cm.conversation_group_id = c.conversation_group_id AND cm.user_id = ?
		WHERE entries_fts MATCH ?
		ORDER BY bm25(entries_fts) ASC, e.id ASC
	`
	type searchRow struct {
		EntryID             uuid.UUID `gorm:"column:entry_id"`
		ConversationID      string    `gorm:"column:conversation_id"`
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
	seenConversation := map[string]struct{}{}
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
			title, err := s.decryptConversationTitle(r.ConversationID, r.ConversationTitle)
			if err != nil {
				return nil, fmt.Errorf("failed to decrypt conversation title: %w", err)
			}
			item.ConversationTitle = &title
		}
		if includeEntry {
			var entry model.Entry
			result := s.dbFor(ctx).
				Where("id = ? AND conversation_group_id = ?", r.EntryID, r.ConversationGroupID).
				Limit(1).
				Find(&entry)
			if result.Error == nil && result.RowsAffected > 0 {
				decrypted, err := s.decryptEntryContent(entry.ID, entry.Content)
				if err != nil {
					return nil, fmt.Errorf("failed to decrypt entry content: %w", err)
				}
				entry.Content = decrypted
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
	const selectColumns = conversationSelectColumns + ", 'owner' as access_level"

	type row struct {
		ID                      string                 `gorm:"column:id"`
		Title                   []byte                 `gorm:"column:title"`
		OwnerUserID             string                 `gorm:"column:owner_user_id"`
		ClientID                string                 `gorm:"column:client_id"`
		AgentID                 *string                `gorm:"column:agent_id"`
		Metadata                map[string]interface{} `gorm:"column:metadata;serializer:json"`
		ConversationGroupID     uuid.UUID              `gorm:"column:conversation_group_id"`
		ForkedAtEntryID         *uuid.UUID             `gorm:"column:forked_at_entry_id"`
		ForkedAtConversationID  *string                `gorm:"column:forked_at_conversation_id"`
		StartedByConversationID *string                `gorm:"column:started_by_conversation_id"`
		StartedByEntryID        *uuid.UUID             `gorm:"column:started_by_entry_id"`
		CreatedAt               time.Time              `gorm:"column:created_at"`
		UpdatedAt               time.Time              `gorm:"column:updated_at"`
		ArchivedAt              *time.Time             `gorm:"column:archived_at"`
		AccessLevel             model.AccessLevel      `gorm:"column:access_level"`
	}

	base := joinDirectConversationAncestry(s.dbFor(ctx).Table("conversations c"))

	switch query.Archived {
	case registrystore.ArchiveFilterInclude:
	case registrystore.ArchiveFilterOnly:
		base = base.Where("c.archived_at IS NOT NULL")
	default:
		base = base.Where("c.archived_at IS NULL")
	}
	if query.UserID != nil {
		base = base.Where("c.owner_user_id = ?", *query.UserID)
	}
	if query.ArchivedAfter != nil {
		base = base.Where("c.archived_at >= ?", *query.ArchivedAfter)
	}
	if query.ArchivedBefore != nil {
		base = base.Where("c.archived_at < ?", *query.ArchivedBefore)
	}
	switch query.Ancestry {
	case model.ConversationAncestryChildren:
		base = base.Where("c.started_by_conversation_id IS NOT NULL")
	case model.ConversationAncestryAll:
	default:
		base = base.Where("c.started_by_conversation_id IS NULL")
	}

	var tx *gorm.DB
	switch query.Mode {
	case model.ListModeRoots:
		tx = base.
			Where("ca_direct.ancestor_conversation_id IS NULL").
			Select(selectColumns)
	case model.ListModeLatestFork:
		ranked := base.Select(selectColumns + ", ROW_NUMBER() OVER (PARTITION BY c.conversation_group_id ORDER BY c.updated_at DESC, c.created_at DESC, c.id DESC) AS group_rank")
		tx = s.dbFor(ctx).
			Table("(?) AS ranked", ranked).
			Select("id, title, owner_user_id, client_id, agent_id, metadata, conversation_group_id, forked_at_entry_id, forked_at_conversation_id, started_by_conversation_id, started_by_entry_id, created_at, updated_at, archived_at, access_level").
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
		title, err := s.decryptConversationTitle(r.ID, r.Title)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to decrypt conversation title: %w", err)
		}
		summaries[i] = registrystore.ConversationSummary{
			ID:                      r.ID,
			Title:                   title,
			OwnerUserID:             r.OwnerUserID,
			ClientID:                r.ClientID,
			AgentID:                 r.AgentID,
			Metadata:                r.Metadata,
			ConversationGroupID:     r.ConversationGroupID,
			ForkedAtEntryID:         r.ForkedAtEntryID,
			ForkedAtConversationID:  r.ForkedAtConversationID,
			StartedByConversationID: r.StartedByConversationID,
			StartedByEntryID:        r.StartedByEntryID,
			CreatedAt:               r.CreatedAt,
			UpdatedAt:               r.UpdatedAt,
			ArchivedAt:              r.ArchivedAt,
			AccessLevel:             r.AccessLevel,
		}
	}

	var cursor *string
	if hasMore && len(summaries) > 0 {
		c := string(summaries[len(summaries)-1].ID)
		cursor = &c
	}
	return summaries, cursor, nil
}

func (s *SQLiteStore) AdminGetConversation(ctx context.Context, conversationID string) (*registrystore.ConversationDetail, error) {
	var conv model.Conversation
	if err := s.dbFor(ctx).Where("id = ?", conversationID).First(&conv).Error; err != nil {
		return nil, &registrystore.NotFoundError{Resource: "conversation", ID: string(conversationID)}
	}
	if err := s.hydrateConversationFork(ctx, &conv); err != nil {
		return nil, err
	}
	title, err := s.decryptConversationTitle(conv.ID, conv.Title)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt conversation title: %w", err)
	}
	return &registrystore.ConversationDetail{
		ConversationSummary: registrystore.ConversationSummary{
			ID:                      conv.ID,
			Title:                   title,
			OwnerUserID:             conv.OwnerUserID,
			ClientID:                conv.ClientID,
			AgentID:                 conv.AgentID,
			Metadata:                conv.Metadata,
			ConversationGroupID:     conv.ConversationGroupID,
			ForkedAtConversationID:  conv.ForkedAtConversationID,
			ForkedAtEntryID:         conv.ForkedAtEntryID,
			StartedByConversationID: conv.StartedByConversationID,
			StartedByEntryID:        conv.StartedByEntryID,
			CreatedAt:               conv.CreatedAt,
			UpdatedAt:               conv.UpdatedAt,
			ArchivedAt:              conv.ArchivedAt,
			AccessLevel:             model.AccessLevelOwner,
		},
	}, nil
}

func (s *SQLiteStore) AdminSetConversationArchived(ctx context.Context, conversationID string, archived bool) error {
	db := s.writeDBFor(ctx, "sqlite store admin set conversation archived")
	var conv model.Conversation
	if err := db.Where("id = ?", conversationID).First(&conv).Error; err != nil {
		return &registrystore.NotFoundError{Resource: "conversation", ID: string(conversationID)}
	}
	if archived {
		now := time.Now()
		groupIDs, err := s.startedConversationGroupIDsForDelete(ctx, conv.ConversationGroupID)
		if err != nil {
			return err
		}
		db.Model(&model.ConversationGroup{}).Where("id IN ?", groupIDs).Update("archived_at", now)
		db.Model(&model.Conversation{}).Where("conversation_group_id IN ? AND archived_at IS NULL", groupIDs).Update("archived_at", now)
		return nil
	}
	if conv.ArchivedAt == nil {
		return &registrystore.ConflictError{Message: "conversation is not archived"}
	}
	db.Model(&model.ConversationGroup{}).Where("id = ?", conv.ConversationGroupID).Update("archived_at", nil)
	db.Model(&model.Conversation{}).Where("conversation_group_id = ?", conv.ConversationGroupID).Update("archived_at", nil)
	return nil
}

func (s *SQLiteStore) AdminGetEntries(ctx context.Context, conversationID string, query registrystore.AdminMessageQuery) (*registrystore.PagedEntries, error) {
	var conv model.Conversation
	if err := s.dbFor(ctx).Where("id = ?", conversationID).First(&conv).Error; err != nil {
		return nil, &registrystore.NotFoundError{Resource: "conversation", ID: string(conversationID)}
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}

	var err error
	if query.AllForks && query.Channel != nil && *query.Channel == model.ChannelHistory {
		page, afterCursor, beforeCursor, err := s.boundedGroupHistory(ctx, conv.ConversationGroupID, query.FromSeq, query.UpToEntryID, query.AfterCursor, query.BeforeCursor, query.Tail, limit)
		if err != nil {
			return nil, err
		}
		if err := decryptEntries(s, page); err != nil {
			return nil, err
		}
		return &registrystore.PagedEntries{Data: page, AfterCursor: afterCursor, BeforeCursor: beforeCursor}, nil
	}

	if query.AllForks && query.Channel != nil && *query.Channel == model.ChannelContext {
		epochFilter := query.EpochFilter
		if epochFilter == nil {
			epochFilter = &registrystore.MemoryEpochFilter{Mode: registrystore.MemoryEpochModeAll}
		}
		page, afterCursor, beforeCursor, err := s.boundedGroupContext(ctx, conv.ConversationGroupID, nil, epochFilter, query.FromSeq, query.UpToEntryID, query.AfterCursor, query.BeforeCursor, query.Tail, limit)
		if err != nil {
			return nil, err
		}
		if err := decryptEntries(s, page); err != nil {
			return nil, err
		}
		return &registrystore.PagedEntries{Data: page, AfterCursor: afterCursor, BeforeCursor: beforeCursor}, nil
	}

	if query.AllForks && query.Channel != nil && *query.Channel == model.ChannelJournal {
		page, afterCursor, beforeCursor, err := s.boundedGroupChannel(ctx, conv.ConversationGroupID, model.ChannelJournal, nil, query.FromSeq, query.UpToEntryID, query.AfterCursor, query.BeforeCursor, query.Tail, limit)
		if err != nil {
			return nil, err
		}
		if err := decryptEntries(s, page); err != nil {
			return nil, err
		}
		return &registrystore.PagedEntries{Data: page, AfterCursor: afterCursor, BeforeCursor: beforeCursor}, nil
	}

	if query.AllForks && query.Channel == nil {
		page, afterCursor, beforeCursor, err := s.boundedGroupAllChannels(ctx, conv.ConversationGroupID, nil, false, query.EpochFilter, query.FromSeq, query.UpToEntryID, query.AfterCursor, query.BeforeCursor, query.Tail, limit)
		if err != nil {
			return nil, err
		}
		if err := decryptEntries(s, page); err != nil {
			return nil, err
		}
		return &registrystore.PagedEntries{Data: page, AfterCursor: afterCursor, BeforeCursor: beforeCursor}, nil
	}

	if !query.AllForks && query.Channel != nil && *query.Channel == model.ChannelHistory {
		page, afterCursor, beforeCursor, err := s.boundedVisibleHistory(ctx, conv, query.FromSeq, query.UpToEntryID, query.AfterCursor, query.BeforeCursor, query.Tail, limit)
		if err != nil {
			return nil, err
		}
		if err := decryptEntries(s, page); err != nil {
			return nil, err
		}
		return &registrystore.PagedEntries{Data: page, AfterCursor: afterCursor, BeforeCursor: beforeCursor}, nil
	}

	if !query.AllForks && query.Channel != nil && *query.Channel == model.ChannelContext {
		epochFilter := query.EpochFilter
		if epochFilter == nil {
			epochFilter = &registrystore.MemoryEpochFilter{Mode: registrystore.MemoryEpochModeAll}
		}
		page, afterCursor, beforeCursor, err := s.boundedVisibleContext(ctx, conv, nil, epochFilter, query.FromSeq, query.UpToEntryID, query.AfterCursor, query.BeforeCursor, query.Tail, limit)
		if err != nil {
			return nil, err
		}
		if err := decryptEntries(s, page); err != nil {
			return nil, err
		}
		return &registrystore.PagedEntries{Data: page, AfterCursor: afterCursor, BeforeCursor: beforeCursor}, nil
	}

	if !query.AllForks && query.Channel != nil && *query.Channel == model.ChannelJournal {
		page, afterCursor, beforeCursor, err := s.boundedVisibleChannel(ctx, conv, model.ChannelJournal, nil, query.FromSeq, query.UpToEntryID, query.AfterCursor, query.BeforeCursor, query.Tail, limit)
		if err != nil {
			return nil, err
		}
		if err := decryptEntries(s, page); err != nil {
			return nil, err
		}
		return &registrystore.PagedEntries{Data: page, AfterCursor: afterCursor, BeforeCursor: beforeCursor}, nil
	}

	if !query.AllForks && query.Channel == nil {
		page, afterCursor, beforeCursor, err := s.boundedVisibleAllChannels(ctx, conv, nil, false, query.EpochFilter, query.FromSeq, query.UpToEntryID, query.AfterCursor, query.BeforeCursor, query.Tail, limit)
		if err != nil {
			return nil, err
		}
		if err := decryptEntries(s, page); err != nil {
			return nil, err
		}
		return &registrystore.PagedEntries{Data: page, AfterCursor: afterCursor, BeforeCursor: beforeCursor}, nil
	}

	var filtered []model.Entry
	var visible []model.Entry
	if query.AllForks {
		allEntries, err := s.listEntriesForGroup(ctx, conv.ConversationGroupID)
		if err != nil {
			return nil, err
		}
		filtered = allEntries
		visible = allEntries
	} else {
		visible, err = s.listVisibleEntriesForConversation(ctx, conv)
		if err != nil {
			return nil, err
		}
		filtered = visible
	}
	if query.Channel != nil {
		ch := *query.Channel
		tmp := make([]model.Entry, 0, len(filtered))
		for _, entry := range filtered {
			if entry.Channel == ch {
				tmp = append(tmp, entry)
			}
		}
		filtered = tmp
	}
	if query.EpochFilter != nil {
		filtered = filterEntriesByEpoch(filtered, query.EpochFilter)
	}
	filtered, err = registrystore.TrimEntriesToVisiblePrefix(filtered, visible, query.UpToEntryID)
	if err != nil {
		return nil, err
	}
	if query.FromSeq != nil {
		filtered = filterEntriesByFromSeq(filtered, *query.FromSeq)
	}

	page, afterCursor, beforeCursor, err := registrystore.PaginateEntries(filtered, query.AfterCursor, query.BeforeCursor, query.Tail, limit)
	if err != nil {
		return nil, &registrystore.BadRequestError{Message: err.Error()}
	}
	if err := decryptEntries(s, page); err != nil {
		return nil, err
	}
	return &registrystore.PagedEntries{Data: page, AfterCursor: afterCursor, BeforeCursor: beforeCursor}, nil
}

func (s *SQLiteStore) AdminListMemberships(ctx context.Context, conversationID string, afterCursor *string, limit int) ([]model.ConversationMembership, *string, error) {
	var conv model.Conversation
	if err := s.dbFor(ctx).Where("id = ?", conversationID).First(&conv).Error; err != nil {
		return nil, nil, &registrystore.NotFoundError{Resource: "conversation", ID: string(conversationID)}
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

func (s *SQLiteStore) AdminListForks(ctx context.Context, conversationID string) (*registrystore.ConversationForkNavigation, error) {
	var conv model.Conversation
	result := s.dbFor(ctx).Where("id = ?", conversationID).Limit(1).Find(&conv)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, &registrystore.NotFoundError{Resource: "conversation", ID: string(conversationID)}
	}
	return s.listForks(ctx, conv.OwnerUserID, conversationID, registrystore.ForkNavigationVisibility{IncludeAllJournals: true})
}

func (s *SQLiteStore) AdminListChildConversations(ctx context.Context, conversationID string, afterCursor *string, limit int) ([]registrystore.ConversationSummary, *string, error) {
	var conv model.Conversation
	findResult := s.dbFor(ctx).Where("id = ?", conversationID).Limit(1).Find(&conv)
	if findResult.Error != nil {
		return nil, nil, findResult.Error
	}
	if findResult.RowsAffected == 0 {
		return nil, nil, &registrystore.NotFoundError{Resource: "conversation", ID: string(conversationID)}
	}
	tx := s.dbFor(ctx).
		Table("conversations c").
		Select(conversationSelectColumns+", 'owner' as access_level").
		Where("c.started_by_conversation_id = ?", conversationID)
	tx = joinDirectConversationAncestry(tx)
	return s.listChildConversationsForBase(ctx, tx, afterCursor, limit)
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
	if !query.IncludeArchived {
		sql += " AND c.archived_at IS NULL"
	}

	if query.UserID != nil {
		sql += " AND c.owner_user_id = ?"
		args = append(args, *query.UserID)
	}
	sql += " ORDER BY bm25(entries_fts) ASC, e.id ASC"

	type searchRow struct {
		EntryID             uuid.UUID `gorm:"column:entry_id"`
		ConversationID      string    `gorm:"column:conversation_id"`
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
			title, err := s.decryptConversationTitle(r.ConversationID, r.ConversationTitle)
			if err != nil {
				return nil, fmt.Errorf("failed to decrypt conversation title: %w", err)
			}
			results[i].ConversationTitle = &title
		}
		if query.IncludeEntry {
			var entry model.Entry
			result := s.dbFor(ctx).
				Where("id = ? AND conversation_group_id = ?", r.EntryID, r.ConversationGroupID).
				Limit(1).
				Find(&entry)
			if result.Error == nil && result.RowsAffected > 0 {
				decrypted, err := s.decryptEntryContent(entry.ID, entry.Content)
				if err != nil {
					return nil, fmt.Errorf("failed to decrypt entry content: %w", err)
				}
				entry.Content = decrypted
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
		Select("a.*, (SELECT COUNT(*) FROM attachments a2 WHERE a2.storage_key = a.storage_key AND a2.archived_at IS NULL) AS ref_count")

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
		return nil, nil, &registrystore.ValidationError{Field: "status", Message: "invalid status"}
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
		Select("a.*, (SELECT COUNT(*) FROM attachments a2 WHERE a2.storage_key = a.storage_key AND a2.archived_at IS NULL) AS ref_count").
		Where("a.id = ?", attachmentID).
		Take(&r).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, &registrystore.NotFoundError{Resource: "attachment", ID: attachmentID.String()}
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
		return &registrystore.NotFoundError{Resource: "attachment", ID: attachmentID.String()}
	}
	return nil
}

// --- Eviction ---

func (s *SQLiteStore) FindEvictableGroupIDs(ctx context.Context, cutoff time.Time, limit int) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := s.dbFor(ctx).
		Model(&model.ConversationGroup{}).
		Where("archived_at IS NOT NULL AND archived_at < ?", cutoff).
		Limit(limit).
		Pluck("id", &ids).Error
	return ids, err
}

func (s *SQLiteStore) CountEvictableGroups(ctx context.Context, cutoff time.Time) (int64, error) {
	var count int64
	err := s.dbFor(ctx).
		Model(&model.ConversationGroup{}).
		Where("archived_at IS NOT NULL AND archived_at < ?", cutoff).
		Count(&count).Error
	return count, err
}

func (s *SQLiteStore) LoadDeletedConversationGroups(ctx context.Context, groupIDs []uuid.UUID) ([]registrystore.DeletedConversationGroup, error) {
	if len(groupIDs) == 0 {
		return nil, nil
	}

	type conversationRow struct {
		ConversationGroupID uuid.UUID `gorm:"column:conversation_group_id"`
		ID                  string    `gorm:"column:id"`
	}
	var conversations []conversationRow
	if err := s.dbFor(ctx).
		Model(&model.Conversation{}).
		Select("conversation_group_id, id").
		Where("conversation_group_id IN ?", groupIDs).
		Order("created_at ASC, id ASC").
		Scan(&conversations).Error; err != nil {
		return nil, err
	}

	var memberships []model.ConversationMembership
	if err := s.dbFor(ctx).
		Where("conversation_group_id IN ?", groupIDs).
		Order("created_at ASC, user_id ASC").
		Find(&memberships).Error; err != nil {
		return nil, err
	}

	groupMap := make(map[uuid.UUID]*registrystore.DeletedConversationGroup, len(groupIDs))
	for _, groupID := range groupIDs {
		groupMap[groupID] = &registrystore.DeletedConversationGroup{ConversationGroupID: groupID}
	}
	for _, row := range conversations {
		groupMap[row.ConversationGroupID].ConversationIDs = append(groupMap[row.ConversationGroupID].ConversationIDs, row.ID)
	}
	for _, membership := range memberships {
		groupMap[membership.ConversationGroupID].MemberUserIDs = append(groupMap[membership.ConversationGroupID].MemberUserIDs, membership.UserID)
	}

	result := make([]registrystore.DeletedConversationGroup, 0, len(groupIDs))
	for _, groupID := range groupIDs {
		if group, ok := groupMap[groupID]; ok {
			result = append(result, *group)
		}
	}
	return result, nil
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
		Select("a.*, (SELECT COUNT(*) FROM attachments a2 WHERE a2.storage_key = a.storage_key AND a2.archived_at IS NULL) AS ref_count").
		Where("a.storage_key = ? AND a.archived_at IS NULL", storageKey).
		Take(&r).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, &registrystore.NotFoundError{Resource: "attachment", ID: storageKey}
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
		return "", &registrystore.ForbiddenError{}
	}
	if !m.AccessLevel.IsAtLeast(minLevel) {
		return "", &registrystore.ForbiddenError{}
	}
	return m.AccessLevel, nil
}

func scopedAgentCacheKey(clientID, agentID string) string {
	return clientID + "\x00" + agentID
}

func valueOrEmpty(ptr *string) string {
	if ptr == nil {
		return ""
	}
	return *ptr
}

const conversationSelectColumns = "c.id, c.title, c.owner_user_id, c.client_id, c.agent_id, c.metadata, c.conversation_group_id, ca_direct.forked_at_entry_id, ca_direct.ancestor_conversation_id AS forked_at_conversation_id, c.started_by_conversation_id, c.started_by_entry_id, c.created_at, c.updated_at, c.archived_at"

func joinDirectConversationAncestry(tx *gorm.DB) *gorm.DB {
	return tx.Joins("LEFT JOIN conversation_ancestry ca_direct ON ca_direct.conversation_group_id = c.conversation_group_id AND ca_direct.descendant_conversation_id = c.id AND ca_direct.depth = 1")
}

func (s *SQLiteStore) createConversationAncestry(ctx context.Context, db *gorm.DB, groupID uuid.UUID, convID string, sourceConv *model.Conversation, forkedAtEntryID *uuid.UUID, anchorOwnerDepth *int) error {
	var parentRows []model.ConversationAncestry
	if sourceConv != nil {
		if err := db.WithContext(ctx).
			Where("conversation_group_id = ? AND descendant_conversation_id = ?", groupID, sourceConv.ID).
			Order("depth ASC").
			Find(&parentRows).Error; err != nil {
			return fmt.Errorf("failed to load parent conversation ancestry: %w", err)
		}
		if len(parentRows) == 0 {
			return fmt.Errorf("parent conversation ancestry is missing: %s", sourceConv.ID)
		}
	}

	parentSegments := make([]model.ConversationAncestrySegment, 0, len(parentRows))
	for _, parentRow := range parentRows {
		parentSegments = append(parentSegments, model.ConversationAncestrySegment{
			ConversationID:  parentRow.AncestorConversationID,
			Depth:           parentRow.Depth,
			BeforeEntryID:   parentRow.BeforeEntryID,
			ForkedAtEntryID: parentRow.ForkedAtEntryID,
		})
	}
	segments, err := model.BuildConversationAncestrySegments(convID, parentSegments, forkedAtEntryID, anchorOwnerDepth)
	if err != nil {
		return err
	}
	rows := make([]model.ConversationAncestry, 0, len(segments))
	for _, segment := range segments {
		rows = append(rows, model.ConversationAncestry{
			ConversationGroupID:      groupID,
			DescendantConversationID: convID,
			AncestorConversationID:   segment.ConversationID,
			Depth:                    segment.Depth,
			BeforeEntryID:            segment.BeforeEntryID,
			ForkedAtEntryID:          segment.ForkedAtEntryID,
		})
	}
	if err := db.WithContext(ctx).Create(&rows).Error; err != nil {
		return fmt.Errorf("failed to create conversation ancestry rows: %w", err)
	}
	return nil
}

func (s *SQLiteStore) hydrateConversationFork(ctx context.Context, conv *model.Conversation) error {
	var direct model.ConversationAncestry
	result := s.dbFor(ctx).
		Where("conversation_group_id = ? AND descendant_conversation_id = ? AND depth = 1", conv.ConversationGroupID, conv.ID).
		Limit(1).
		Find(&direct)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		conv.ForkedAtConversationID = nil
		conv.ForkedAtEntryID = nil
		return nil
	}
	parentID := direct.AncestorConversationID
	conv.ForkedAtConversationID = &parentID
	conv.ForkedAtEntryID = direct.ForkedAtEntryID
	return nil
}

func (s *SQLiteStore) entryVisibleInConversationAncestry(ctx context.Context, conv model.Conversation, entryID uuid.UUID) (bool, error) {
	var entry model.Entry
	result := s.dbFor(ctx).
		Where("id = ? AND conversation_group_id = ?", entryID, conv.ConversationGroupID).
		Limit(1).
		Find(&entry)
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 0 {
		return false, nil
	}
	depth, err := s.visibleAncestryDepthForEntry(ctx, conv, entry)
	return depth != nil, err
}

func (s *SQLiteStore) startedConversationGroupIDsForDelete(ctx context.Context, rootGroupID uuid.UUID) ([]uuid.UUID, error) {
	type row struct {
		GroupID uuid.UUID `gorm:"column:conversation_group_id"`
	}
	var rows []row
	query := `
		WITH RECURSIVE lineage(id) AS (
			SELECT id FROM conversations WHERE conversation_group_id = ?
			UNION
			SELECT c.id
			FROM conversations c
			JOIN lineage l ON c.started_by_conversation_id = l.id
		)
		SELECT DISTINCT conversation_group_id
		FROM conversations
		WHERE id IN (SELECT id FROM lineage)
	`
	if err := s.dbFor(ctx).Raw(query, rootGroupID).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to resolve started conversation descendants: %w", err)
	}
	groupIDs := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		groupIDs = append(groupIDs, row.GroupID)
	}
	if len(groupIDs) == 0 {
		groupIDs = append(groupIDs, rootGroupID)
	}
	return groupIDs, nil
}

func (s *SQLiteStore) listChildConversationsForBase(ctx context.Context, tx *gorm.DB, afterCursor *string, limit int) ([]registrystore.ConversationSummary, *string, error) {
	type row struct {
		ID                      string                 `gorm:"column:id"`
		Title                   []byte                 `gorm:"column:title"`
		OwnerUserID             string                 `gorm:"column:owner_user_id"`
		ClientID                string                 `gorm:"column:client_id"`
		AgentID                 *string                `gorm:"column:agent_id"`
		Metadata                map[string]interface{} `gorm:"column:metadata;serializer:json"`
		ConversationGroupID     uuid.UUID              `gorm:"column:conversation_group_id"`
		ForkedAtEntryID         *uuid.UUID             `gorm:"column:forked_at_entry_id"`
		ForkedAtConversationID  *string                `gorm:"column:forked_at_conversation_id"`
		StartedByConversationID *string                `gorm:"column:started_by_conversation_id"`
		StartedByEntryID        *uuid.UUID             `gorm:"column:started_by_entry_id"`
		CreatedAt               time.Time              `gorm:"column:created_at"`
		UpdatedAt               time.Time              `gorm:"column:updated_at"`
		ArchivedAt              *time.Time             `gorm:"column:archived_at"`
		AccessLevel             model.AccessLevel      `gorm:"column:access_level"`
	}
	if afterCursor != nil {
		tx = tx.Where("(c.created_at, c.id) > ((SELECT created_at FROM conversations WHERE id = ?), ?)", *afterCursor, *afterCursor)
	}
	tx = tx.Order("c.created_at ASC, c.id ASC").Limit(limit + 1)
	var rows []row
	if err := tx.Scan(&rows).Error; err != nil {
		return nil, nil, fmt.Errorf("failed to list child conversations: %w", err)
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	summaries := make([]registrystore.ConversationSummary, len(rows))
	for i, r := range rows {
		title, err := s.decryptConversationTitle(r.ID, r.Title)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to decrypt conversation title: %w", err)
		}
		summaries[i] = registrystore.ConversationSummary{
			ID:                      r.ID,
			Title:                   title,
			OwnerUserID:             r.OwnerUserID,
			ClientID:                r.ClientID,
			AgentID:                 r.AgentID,
			Metadata:                r.Metadata,
			ConversationGroupID:     r.ConversationGroupID,
			ForkedAtEntryID:         r.ForkedAtEntryID,
			ForkedAtConversationID:  r.ForkedAtConversationID,
			StartedByConversationID: r.StartedByConversationID,
			StartedByEntryID:        r.StartedByEntryID,
			CreatedAt:               r.CreatedAt,
			UpdatedAt:               r.UpdatedAt,
			ArchivedAt:              r.ArchivedAt,
			AccessLevel:             r.AccessLevel,
		}
	}
	var cursor *string
	if hasMore && len(summaries) > 0 {
		c := string(summaries[len(summaries)-1].ID)
		cursor = &c
	}
	return summaries, cursor, nil
}

type forkAncestor struct {
	ConversationID string
	StopAtEntryID  *uuid.UUID
}

// fetchLatestMemoryEntries returns the latest-epoch context entries for the given
// conversation and clientID, using MemoryEntriesCache as a read-through layer.
func (s *SQLiteStore) fetchLatestMemoryEntries(ctx context.Context, conv model.Conversation, ancestry []forkAncestor, clientID, agentID string) ([]model.Entry, error) {
	cacheKey := scopedAgentCacheKey(clientID, agentID)
	if s.entriesCache != nil && s.entriesCache.Available() {
		cached, err := s.entriesCache.Get(ctx, conv.ID, cacheKey)
		if err == nil && cached != nil {
			if security.CacheHitsTotal != nil {
				security.CacheHitsTotal.Inc()
			}
			return cached.Entries, nil
		}
	}

	entries, err := s.listLatestVisibleContextEntries(ctx, conv, clientID)
	if err != nil {
		return nil, err
	}

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
			if serr := s.entriesCache.Set(ctx, conv.ID, cacheKey, registrycache.CachedMemoryEntries{Entries: entries, Epoch: epoch}, 0); serr != nil {
				log.Warn("entries cache set error", "err", serr)
			}
		}
	}
	return entries, nil
}

// warmEntriesCache re-fetches the latest context entries from the DB and updates the cache.
// Called after a successful SyncAgentEntry write to keep the cache warm.
func (s *SQLiteStore) warmEntriesCache(ctx context.Context, conv model.Conversation, ancestry []forkAncestor, clientID, agentID string) {
	if s.entriesCache == nil || !s.entriesCache.Available() {
		return
	}
	cacheKey := scopedAgentCacheKey(clientID, agentID)
	entries, err := s.listLatestVisibleContextEntries(ctx, conv, clientID)
	if err != nil {
		log.Warn("warmEntriesCache: failed to list entries", "err", err)
		return
	}
	if len(entries) == 0 {
		if rerr := s.entriesCache.Remove(ctx, conv.ID, cacheKey); rerr != nil {
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
	if serr := s.entriesCache.Set(ctx, conv.ID, cacheKey, registrycache.CachedMemoryEntries{Entries: entries, Epoch: epoch}, 0); serr != nil {
		log.Warn("warmEntriesCache: cache set error", "err", serr)
	}
}

func (s *SQLiteStore) listEntriesForGroup(ctx context.Context, groupID uuid.UUID) ([]model.Entry, error) {
	var entries []model.Entry
	if err := s.dbFor(ctx).
		Where("conversation_group_id = ?", groupID).
		Order("created_at ASC, seq ASC NULLS FIRST, id ASC").
		Find(&entries).Error; err != nil {
		return nil, fmt.Errorf("failed to list entries: %w", err)
	}
	return entries, nil
}

func (s *SQLiteStore) listVisibleEntriesForConversation(ctx context.Context, conv model.Conversation) ([]model.Entry, error) {
	var entries []model.Entry
	err := s.dbFor(ctx).
		Table("entries e").
		Select("e.*").
		Joins("JOIN conversation_ancestry ca ON ca.conversation_group_id = e.conversation_group_id AND ca.descendant_conversation_id = ? AND ca.ancestor_conversation_id = e.conversation_id", conv.ID).
		Joins("LEFT JOIN entries boundary ON boundary.conversation_group_id = ca.conversation_group_id AND boundary.id = ca.before_entry_id").
		Where("e.conversation_group_id = ?", conv.ConversationGroupID).
		Where(visibleAncestryEntrySQL("e", "ca", "boundary")).
		Order("e.created_at ASC, e.seq ASC NULLS FIRST, e.id ASC").
		Find(&entries).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list visible entries: %w", err)
	}
	return entries, nil
}

func (s *SQLiteStore) visibleContextEntriesQuery(ctx context.Context, conv model.Conversation, clientID string) *gorm.DB {
	return s.visibleChannelEntriesQuery(ctx, conv, model.ChannelContext, &clientID)
}

func (s *SQLiteStore) visibleChannelEntriesQuery(ctx context.Context, conv model.Conversation, channel model.Channel, clientID *string) *gorm.DB {
	tx := s.dbFor(ctx).
		Table("entries e").
		Select("e.*").
		Joins("JOIN conversation_ancestry ca ON ca.conversation_group_id = e.conversation_group_id AND ca.descendant_conversation_id = ? AND ca.ancestor_conversation_id = e.conversation_id", conv.ID).
		Joins("LEFT JOIN entries boundary ON boundary.conversation_group_id = ca.conversation_group_id AND boundary.id = ca.before_entry_id").
		Where("e.conversation_group_id = ? AND e.channel = ?", conv.ConversationGroupID, channel).
		Where(visibleAncestryEntrySQL("e", "ca", "boundary"))
	if clientID != nil {
		tx = tx.Where("e.client_id = ?", *clientID)
	}
	return tx
}

func (s *SQLiteStore) visibleEntriesQuery(ctx context.Context, conv model.Conversation) *gorm.DB {
	return s.dbFor(ctx).
		Table("entries e").
		Select("e.*").
		Joins("JOIN conversation_ancestry ca ON ca.conversation_group_id = e.conversation_group_id AND ca.descendant_conversation_id = ? AND ca.ancestor_conversation_id = e.conversation_id", conv.ID).
		Joins("LEFT JOIN entries boundary ON boundary.conversation_group_id = ca.conversation_group_id AND boundary.id = ca.before_entry_id").
		Where("e.conversation_group_id = ?", conv.ConversationGroupID).
		Where(visibleAncestryEntrySQL("e", "ca", "boundary"))
}

func (s *SQLiteStore) visibleEntryByID(ctx context.Context, conv model.Conversation, entryID string) (model.Entry, bool, error) {
	var entry model.Entry
	result := s.visibleEntriesQuery(ctx, conv).
		Where("e.id = ?", entryID).
		Limit(1).
		Find(&entry)
	if result.Error != nil {
		return model.Entry{}, false, result.Error
	}
	return entry, result.RowsAffected > 0, nil
}

func (s *SQLiteStore) visibleAllChannelsQuery(ctx context.Context, conv model.Conversation, clientID *string, suppressScopedWithoutClient bool) *gorm.DB {
	tx := s.visibleEntriesQuery(ctx, conv)
	if !suppressScopedWithoutClient {
		return tx
	}
	scopedChannels := []string{string(model.ChannelContext), string(model.ChannelJournal)}
	if clientID == nil {
		return tx.Where("e.channel NOT IN ?", scopedChannels)
	}
	return tx.Where("(e.channel NOT IN ? OR e.client_id = ?)", scopedChannels, *clientID)
}

func (s *SQLiteStore) listLatestVisibleContextEntries(ctx context.Context, conv model.Conversation, clientID string) ([]model.Entry, error) {
	base := s.visibleContextEntriesQuery(ctx, conv, clientID)
	var epochRow struct {
		MaxEpoch *int64 `gorm:"column:max_epoch"`
	}
	if err := base.Session(&gorm.Session{}).
		Select("MAX(COALESCE(e.epoch, 0)) AS max_epoch").
		Scan(&epochRow).Error; err != nil {
		return nil, fmt.Errorf("failed to get latest context epoch: %w", err)
	}
	if epochRow.MaxEpoch == nil {
		return []model.Entry{}, nil
	}
	var entries []model.Entry
	err := base.Session(&gorm.Session{}).
		Where("COALESCE(e.epoch, 0) = ?", *epochRow.MaxEpoch).
		Order("e.created_at ASC, e.seq ASC NULLS FIRST, e.id ASC").
		Find(&entries).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list latest context entries: %w", err)
	}
	return entries, nil
}

func visibleAncestryEntrySQL(entryAlias, ancestryAlias, boundaryAlias string) string {
	return fmt.Sprintf(`(
		%s.depth = 0
		OR (
			%s.before_entry_id IS NOT NULL
			AND (
				%s.created_at < %s.created_at
				OR (
					%s.created_at = %s.created_at
					AND (
						(%s.seq IS NULL AND %s.seq IS NULL AND %s.id < %s.id)
						OR (
							%s.seq IS NOT NULL
							AND (
								%s.seq IS NULL
								OR %s.seq < %s.seq
								OR (%s.seq = %s.seq AND %s.id < %s.id)
							)
						)
					)
				)
			)
		)
	)`, ancestryAlias,
		ancestryAlias,
		entryAlias, boundaryAlias,
		entryAlias, boundaryAlias,
		entryAlias, boundaryAlias, entryAlias, boundaryAlias,
		boundaryAlias,
		entryAlias,
		entryAlias, boundaryAlias,
		entryAlias, boundaryAlias, entryAlias, boundaryAlias,
	)
}

func (s *SQLiteStore) visibleHistoryEntriesQuery(ctx context.Context, conv model.Conversation) *gorm.DB {
	return s.dbFor(ctx).
		Table("entries e").
		Select("e.*").
		Joins("JOIN conversation_ancestry ca ON ca.conversation_group_id = e.conversation_group_id AND ca.descendant_conversation_id = ? AND ca.ancestor_conversation_id = e.conversation_id", conv.ID).
		Joins("LEFT JOIN entries boundary ON boundary.conversation_group_id = ca.conversation_group_id AND boundary.id = ca.before_entry_id").
		Where("e.conversation_group_id = ? AND e.channel = ?", conv.ConversationGroupID, model.ChannelHistory).
		Where(visibleAncestryEntrySQL("e", "ca", "boundary"))
}

func (s *SQLiteStore) entryExists(ctx context.Context, entryID string) (bool, error) {
	var entry model.Entry
	result := s.dbFor(ctx).
		Select("id").
		Where("id = ?", entryID).
		Limit(1).
		Find(&entry)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func (s *SQLiteStore) cursorEntryError(ctx context.Context, cursorName, entryID string) error {
	exists, err := s.entryExists(ctx, entryID)
	if err != nil {
		return err
	}
	if !exists {
		return &registrystore.BadRequestError{Message: cursorName + " entry not found"}
	}
	return &registrystore.BadRequestError{Message: cursorName + " entry not found in visible results"}
}

func (s *SQLiteStore) groupHistoryEntriesQuery(ctx context.Context, groupID uuid.UUID, fromSeq *uint32) *gorm.DB {
	tx := s.dbFor(ctx).
		Table("entries e").
		Select("e.*").
		Where("e.conversation_group_id = ? AND e.channel = ?", groupID, model.ChannelHistory)
	if fromSeq != nil {
		tx = tx.Where("e.seq IS NOT NULL AND e.seq >= ?", *fromSeq)
	}
	return tx
}

func (s *SQLiteStore) groupEntriesQuery(ctx context.Context, groupID uuid.UUID) *gorm.DB {
	return s.dbFor(ctx).
		Table("entries e").
		Select("e.*").
		Where("e.conversation_group_id = ?", groupID)
}

func (s *SQLiteStore) groupEntryByID(ctx context.Context, groupID uuid.UUID, entryID string) (model.Entry, bool, error) {
	var entry model.Entry
	result := s.groupEntriesQuery(ctx, groupID).
		Where("e.id = ?", entryID).
		Limit(1).
		Find(&entry)
	if result.Error != nil {
		return model.Entry{}, false, result.Error
	}
	return entry, result.RowsAffected > 0, nil
}

func (s *SQLiteStore) groupChannelEntriesQuery(ctx context.Context, groupID uuid.UUID, channel model.Channel, clientID *string) *gorm.DB {
	tx := s.groupEntriesQuery(ctx, groupID).Where("e.channel = ?", channel)
	if clientID != nil {
		tx = tx.Where("e.client_id = ?", *clientID)
	}
	return tx
}

func (s *SQLiteStore) groupAllChannelsQuery(ctx context.Context, groupID uuid.UUID, clientID *string, suppressScopedWithoutClient bool) *gorm.DB {
	tx := s.groupEntriesQuery(ctx, groupID)
	if !suppressScopedWithoutClient {
		return tx
	}
	scopedChannels := []string{string(model.ChannelContext), string(model.ChannelJournal)}
	if clientID == nil {
		return tx.Where("e.channel NOT IN ?", scopedChannels)
	}
	return tx.Where("(e.channel NOT IN ? OR e.client_id = ?)", scopedChannels, *clientID)
}

func (s *SQLiteStore) runBoundedSQLQuery(ctx context.Context, base *gorm.DB, fromSeq *uint32, upToEntryID, afterEntryID, beforeEntryID *string, tail bool, limit int, upToLookup sqlentry.LookupFunc, transform func(*gorm.DB) (*gorm.DB, error), scanErr string) ([]model.Entry, *string, *string, error) {
	return sqlentry.RunBoundedQuery(ctx, sqlentry.BoundedQuery{
		Base:             base,
		FromSeq:          fromSeq,
		UpToEntryID:      upToEntryID,
		AfterEntryID:     afterEntryID,
		BeforeEntryID:    beforeEntryID,
		Tail:             tail,
		Limit:            limit,
		MaxLimit:         config.MaxPageSizeFromContext(ctx),
		UpToLookup:       upToLookup,
		BaseTransform:    transform,
		CursorEntryError: s.cursorEntryError,
		LimitError: func(max int) error {
			return &registrystore.BadRequestError{Message: fmt.Sprintf("limit must be between 1 and %d", max)}
		},
		EntryNotFound: func(entryID string) error {
			return &registrystore.NotFoundError{Resource: "entry", ID: entryID}
		},
		EntryIDValue: sqlentry.UUIDStringValue,
		ScanErr:      scanErr,
	})
}

func (s *SQLiteStore) boundedVisibleHistory(ctx context.Context, conv model.Conversation, fromSeq *uint32, upToEntryID, afterEntryID, beforeEntryID *string, tail bool, limit int) ([]model.Entry, *string, *string, error) {
	base := s.visibleHistoryEntriesQuery(ctx, conv)
	return s.runBoundedSQLQuery(ctx, base, fromSeq, upToEntryID, afterEntryID, beforeEntryID, tail, limit, func(entryID string) (model.Entry, bool, error) {
		return s.visibleEntryByID(ctx, conv, entryID)
	}, nil, "bounded history scan failed")
}

func (s *SQLiteStore) boundedVisibleContext(ctx context.Context, conv model.Conversation, clientID *string, epochFilter *registrystore.MemoryEpochFilter, fromSeq *uint32, upToEntryID, afterEntryID, beforeEntryID *string, tail bool, limit int) ([]model.Entry, *string, *string, error) {
	base := s.visibleChannelEntriesQuery(ctx, conv, model.ChannelContext, clientID)
	return s.runBoundedSQLQuery(ctx, base, fromSeq, upToEntryID, afterEntryID, beforeEntryID, tail, limit, func(entryID string) (model.Entry, bool, error) {
		return s.visibleEntryByID(ctx, conv, entryID)
	}, func(base *gorm.DB) (*gorm.DB, error) {
		return sqlentry.ApplyEpochFilter(base, epochFilter, true, "failed to get latest context epoch")
	}, "bounded context scan failed")
}

func (s *SQLiteStore) boundedVisibleChannel(ctx context.Context, conv model.Conversation, channel model.Channel, clientID *string, fromSeq *uint32, upToEntryID, afterEntryID, beforeEntryID *string, tail bool, limit int) ([]model.Entry, *string, *string, error) {
	base := s.visibleChannelEntriesQuery(ctx, conv, channel, clientID)
	return s.runBoundedSQLQuery(ctx, base, fromSeq, upToEntryID, afterEntryID, beforeEntryID, tail, limit, func(entryID string) (model.Entry, bool, error) {
		return s.visibleEntryByID(ctx, conv, entryID)
	}, nil, "bounded channel scan failed")
}

func (s *SQLiteStore) boundedVisibleAllChannels(ctx context.Context, conv model.Conversation, clientID *string, suppressScopedWithoutClient bool, epochFilter *registrystore.MemoryEpochFilter, fromSeq *uint32, upToEntryID, afterEntryID, beforeEntryID *string, tail bool, limit int) ([]model.Entry, *string, *string, error) {
	base := s.visibleAllChannelsQuery(ctx, conv, clientID, suppressScopedWithoutClient)
	return s.runBoundedSQLQuery(ctx, base, fromSeq, upToEntryID, afterEntryID, beforeEntryID, tail, limit, func(entryID string) (model.Entry, bool, error) {
		return s.visibleEntryByID(ctx, conv, entryID)
	}, func(base *gorm.DB) (*gorm.DB, error) {
		return applySQLEpochFilterToBase(base, epochFilter)
	}, "bounded all-channel scan failed")
}

func (s *SQLiteStore) boundedGroupHistory(ctx context.Context, groupID uuid.UUID, fromSeq *uint32, upToEntryID, afterEntryID, beforeEntryID *string, tail bool, limit int) ([]model.Entry, *string, *string, error) {
	base := s.groupHistoryEntriesQuery(ctx, groupID, nil)
	return s.runBoundedSQLQuery(ctx, base, fromSeq, upToEntryID, afterEntryID, beforeEntryID, tail, limit, func(entryID string) (model.Entry, bool, error) {
		return s.groupEntryByID(ctx, groupID, entryID)
	}, nil, "bounded group history scan failed")
}

func (s *SQLiteStore) boundedGroupContext(ctx context.Context, groupID uuid.UUID, clientID *string, epochFilter *registrystore.MemoryEpochFilter, fromSeq *uint32, upToEntryID, afterEntryID, beforeEntryID *string, tail bool, limit int) ([]model.Entry, *string, *string, error) {
	base := s.groupChannelEntriesQuery(ctx, groupID, model.ChannelContext, clientID)
	return s.runBoundedSQLQuery(ctx, base, fromSeq, upToEntryID, afterEntryID, beforeEntryID, tail, limit, func(entryID string) (model.Entry, bool, error) {
		return s.groupEntryByID(ctx, groupID, entryID)
	}, func(base *gorm.DB) (*gorm.DB, error) {
		return sqlentry.ApplyEpochFilter(base, epochFilter, true, "failed to get latest context epoch")
	}, "bounded group context scan failed")
}

func (s *SQLiteStore) boundedGroupChannel(ctx context.Context, groupID uuid.UUID, channel model.Channel, clientID *string, fromSeq *uint32, upToEntryID, afterEntryID, beforeEntryID *string, tail bool, limit int) ([]model.Entry, *string, *string, error) {
	base := s.groupChannelEntriesQuery(ctx, groupID, channel, clientID)
	return s.runBoundedSQLQuery(ctx, base, fromSeq, upToEntryID, afterEntryID, beforeEntryID, tail, limit, func(entryID string) (model.Entry, bool, error) {
		return s.groupEntryByID(ctx, groupID, entryID)
	}, nil, "bounded group channel scan failed")
}

func (s *SQLiteStore) boundedGroupAllChannels(ctx context.Context, groupID uuid.UUID, clientID *string, suppressScopedWithoutClient bool, epochFilter *registrystore.MemoryEpochFilter, fromSeq *uint32, upToEntryID, afterEntryID, beforeEntryID *string, tail bool, limit int) ([]model.Entry, *string, *string, error) {
	base := s.groupAllChannelsQuery(ctx, groupID, clientID, suppressScopedWithoutClient)
	return s.runBoundedSQLQuery(ctx, base, fromSeq, upToEntryID, afterEntryID, beforeEntryID, tail, limit, func(entryID string) (model.Entry, bool, error) {
		return s.groupEntryByID(ctx, groupID, entryID)
	}, func(base *gorm.DB) (*gorm.DB, error) {
		return applySQLEpochFilterToBase(base, epochFilter)
	}, "bounded group all-channel scan failed")
}

func (s *SQLiteStore) buildAncestryStack(ctx context.Context, target model.Conversation) ([]forkAncestor, error) {
	var rows []model.ConversationAncestry
	if err := s.dbFor(ctx).
		Where("conversation_group_id = ? AND descendant_conversation_id = ?", target.ConversationGroupID, target.ID).
		Order("depth DESC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to load fork ancestry: %w", err)
	}
	stack := make([]forkAncestor, 0, len(rows))
	for _, row := range rows {
		stack = append(stack, forkAncestor{
			ConversationID: row.AncestorConversationID,
			StopAtEntryID:  row.BeforeEntryID,
		})
	}
	return stack, nil
}

func (s *SQLiteStore) visibleAncestryDepthForEntry(ctx context.Context, target model.Conversation, entry model.Entry) (*int, error) {
	var rows []model.ConversationAncestry
	if err := s.dbFor(ctx).
		Where("conversation_group_id = ? AND descendant_conversation_id = ?", target.ConversationGroupID, target.ID).
		Order("depth ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to load fork ancestry: %w", err)
	}
	for _, row := range rows {
		if row.AncestorConversationID != entry.ConversationID {
			continue
		}
		if row.Depth > 0 {
			if row.BeforeEntryID == nil {
				return nil, nil
			}
			stopAt, err := s.entryOrderKey(ctx, *row.BeforeEntryID)
			if err != nil {
				return nil, err
			}
			if !entryOrderLess(entry, stopAt) {
				return nil, nil
			}
		}
		depth := row.Depth
		return &depth, nil
	}
	return nil, nil
}

// boundedHistoryBackward performs a bounded DESC-order read of history entries
// across the ancestry stack, collecting at most limit+1 rows then reversing
// to return a page in ascending order.
func (s *SQLiteStore) boundedHistoryBackward(ctx context.Context, ancestry []forkAncestor, beforeEntryID *string, tail bool, limit int) ([]model.Entry, *string, *string, error) {
	if limit <= 0 || limit > config.MaxPageSizeFromContext(ctx) {
		return nil, nil, nil, &registrystore.BadRequestError{Message: fmt.Sprintf("limit must be between 1 and %d", config.MaxPageSizeFromContext(ctx))}
	}
	need := limit + 1
	collected := make([]model.Entry, 0, need)
	startSegment := len(ancestry) - 1

	var anchorCreatedAt *time.Time
	var anchorSeq *uint32
	var anchorID *uuid.UUID
	var anchorConvID string

	if beforeEntryID != nil {
		var anchor model.Entry
		result := s.dbFor(ctx).
			Where("id = ?", *beforeEntryID).
			Select("id, conversation_id, channel, created_at, seq").
			Limit(1).Find(&anchor)
		if result.Error != nil {
			return nil, nil, nil, result.Error
		}
		if result.RowsAffected == 0 {
			return nil, nil, nil, &registrystore.BadRequestError{Message: "beforeCursor entry not found"}
		}
		anchorCreatedAt = &anchor.CreatedAt
		anchorSeq = anchor.Seq
		anchorID = &anchor.ID
		anchorConvID = anchor.ConversationID

		if anchor.Channel != model.ChannelHistory {
			return nil, nil, nil, &registrystore.BadRequestError{Message: "beforeCursor entry not found in visible results"}
		}

		onPath := false
		for i, a := range ancestry {
			if a.ConversationID == anchorConvID {
				if i < len(ancestry)-1 && a.StopAtEntryID == nil {
					return nil, nil, nil, &registrystore.BadRequestError{Message: "beforeCursor entry not found in visible results"}
				}
				if i < len(ancestry)-1 {
					stopAt, err := s.entryOrderKey(ctx, *a.StopAtEntryID)
					if err != nil {
						return nil, nil, nil, err
					}
					if !entryOrderLess(anchor, stopAt) {
						return nil, nil, nil, &registrystore.BadRequestError{Message: "beforeCursor entry not found in visible results"}
					}
				}
				startSegment = i
				onPath = true
				break
			}
		}
		if !onPath {
			return nil, nil, nil, &registrystore.BadRequestError{Message: "beforeCursor entry not found in visible results"}
		}
	}

	for i := startSegment; i >= 0 && len(collected) < need; i-- {
		seg := ancestry[i]
		isTarget := i == len(ancestry)-1

		if !isTarget && seg.StopAtEntryID == nil {
			continue
		}

		tx := s.dbFor(ctx).
			Where("conversation_id = ? AND channel = ?", seg.ConversationID, model.ChannelHistory).
			Order("created_at DESC, seq DESC NULLS LAST, id DESC")

		if !isTarget && seg.StopAtEntryID != nil {
			var stopAt model.Entry
			stopResult := s.dbFor(ctx).
				Where("id = ?", seg.StopAtEntryID).
				Select("id, created_at, seq").
				Limit(1).Find(&stopAt)
			if stopResult.Error != nil {
				return nil, nil, nil, stopResult.Error
			}
			if stopResult.RowsAffected > 0 {
				tx = whereEntryOrderBefore(tx, stopAt)
			}
		}

		if beforeEntryID != nil && seg.ConversationID == anchorConvID {
			tx = whereEntryOrderBefore(tx, model.Entry{ID: *anchorID, CreatedAt: *anchorCreatedAt, Seq: anchorSeq})
		}

		remaining := need - len(collected)
		tx = tx.Limit(remaining)

		var batch []model.Entry
		if err := tx.Find(&batch).Error; err != nil {
			return nil, nil, nil, fmt.Errorf("bounded history scan failed: %w", err)
		}
		collected = append(collected, batch...)
	}

	for lo, hi := 0, len(collected)-1; lo < hi; lo, hi = lo+1, hi-1 {
		collected[lo], collected[hi] = collected[hi], collected[lo]
	}

	hasMore := len(collected) > limit
	if hasMore {
		collected = collected[1:] // drop the oldest probe entry
		// beforeCursor = first entry of the page (signals older entries exist).
		c := collected[0].ID.String()
		beforeCursor := &c
		// afterCursor: when beforeEntryID was set, there are newer entries beyond the anchor.
		var afterCursor *string
		if beforeEntryID != nil && len(collected) > 0 {
			ac := collected[len(collected)-1].ID.String()
			afterCursor = &ac
		}
		return collected, afterCursor, beforeCursor, nil
	}
	var afterCursor *string
	if beforeEntryID != nil && len(collected) > 0 {
		c := collected[len(collected)-1].ID.String()
		afterCursor = &c
	}
	return collected, afterCursor, nil, nil
}

func (s *SQLiteStore) entryOrderKey(ctx context.Context, entryID uuid.UUID) (model.Entry, error) {
	var entry model.Entry
	result := s.dbFor(ctx).Where("id = ?", entryID).Select("id, created_at, seq").Limit(1).Find(&entry)
	if result.Error != nil {
		return model.Entry{}, result.Error
	}
	if result.RowsAffected == 0 {
		return model.Entry{}, &registrystore.NotFoundError{Resource: "entry", ID: entryID.String()}
	}
	return entry, nil
}

func entryOrderLess(left, right model.Entry) bool {
	if !left.CreatedAt.Equal(right.CreatedAt) {
		return left.CreatedAt.Before(right.CreatedAt)
	}
	if left.Seq == nil || right.Seq == nil {
		if left.Seq == nil && right.Seq != nil {
			return true
		}
		if left.Seq != nil && right.Seq == nil {
			return false
		}
	} else if *left.Seq != *right.Seq {
		return *left.Seq < *right.Seq
	}
	return left.ID.String() < right.ID.String()
}

func whereEntryOrderBefore(tx *gorm.DB, bound model.Entry) *gorm.DB {
	if bound.Seq == nil {
		return tx.Where(
			"created_at < ? OR (created_at = ? AND seq IS NULL AND id < ?)",
			bound.CreatedAt, bound.CreatedAt, bound.ID.String(),
		)
	}
	return tx.Where(
		"created_at < ? OR (created_at = ? AND (seq IS NULL OR seq < ? OR (seq = ? AND id < ?)))",
		bound.CreatedAt, bound.CreatedAt, *bound.Seq, *bound.Seq, bound.ID.String(),
	)
}

func normalizeEpochFilter(filter *registrystore.MemoryEpochFilter) registrystore.MemoryEpochFilter {
	if filter == nil || filter.Mode == "" {
		return registrystore.MemoryEpochFilter{Mode: registrystore.MemoryEpochModeLatest}
	}
	return *filter
}

func applySQLEpochFilterToBase(base *gorm.DB, epochFilter *registrystore.MemoryEpochFilter) (*gorm.DB, error) {
	if epochFilter == nil {
		return base, nil
	}
	epoch := normalizeEpochFilter(epochFilter)
	switch epoch.Mode {
	case registrystore.MemoryEpochModeAll:
		return base, nil
	case registrystore.MemoryEpochModeEpoch:
		if epoch.Epoch == nil {
			return base.Where("e.channel <> ?", model.ChannelContext), nil
		}
		return base.Where("e.channel <> ? OR COALESCE(e.epoch, 0) = ?", model.ChannelContext, *epoch.Epoch), nil
	default:
		var epochRow struct {
			MaxEpoch *int64 `gorm:"column:max_epoch"`
		}
		if err := base.Session(&gorm.Session{}).
			Where("e.channel = ?", model.ChannelContext).
			Select("MAX(COALESCE(e.epoch, 0)) AS max_epoch").
			Scan(&epochRow).Error; err != nil {
			return nil, fmt.Errorf("failed to get latest entry epoch: %w", err)
		}
		if epochRow.MaxEpoch == nil {
			return base, nil
		}
		return base.Where("e.channel <> ? OR COALESCE(e.epoch, 0) = ?", model.ChannelContext, *epochRow.MaxEpoch), nil
	}
}

func filterEntriesForAllForks(entries []model.Entry, channel model.Channel, clientID *string, agentID *string, epochFilter *registrystore.MemoryEpochFilter) []model.Entry {
	if channel == "" {
		return filterEntriesForAllChannels(entries, clientID)
	}

	filtered := make([]model.Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.Channel != channel {
			continue
		}
		if (channel == model.ChannelContext || channel == model.ChannelJournal) && clientID != nil {
			if entry.ClientID == nil || *entry.ClientID != *clientID {
				continue
			}
		}
		filtered = append(filtered, entry)
	}

	if channel != model.ChannelContext {
		return filtered // journal has no epoch semantics; return as-is
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

func filterVisibleMemoryEntriesWithEpoch(entries []model.Entry, clientID, agentID string, epochFilter *registrystore.MemoryEpochFilter) []model.Entry {
	epoch := normalizeEpochFilter(epochFilter)
	result := make([]model.Entry, 0, len(entries))
	maxEpochSeen := int64(0)
	maxEpochInitialized := false

	for _, entry := range entries {
		if entry.Channel != model.ChannelContext || entry.ClientID == nil || *entry.ClientID != clientID {
			continue
		}
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
	return result
}

func filterEntriesForAllChannels(entries []model.Entry, clientID *string) []model.Entry {
	filtered := make([]model.Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.Channel == model.ChannelContext || entry.Channel == model.ChannelJournal {
			if clientID == nil || entry.ClientID == nil || *entry.ClientID != *clientID {
				continue
			}
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func filterEntriesByEpoch(entries []model.Entry, epochFilter *registrystore.MemoryEpochFilter) []model.Entry {
	epoch := normalizeEpochFilter(epochFilter)
	result := make([]model.Entry, 0, len(entries))
	maxEpochSeen := int64(0)
	maxEpochInitialized := false
	for _, entry := range entries {
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
	return result
}

// filterEntriesByFromSeq filters entries to those with seq >= fromSeq and sorts by seq ASC.
// Entries without a seq (nil) are excluded.
func filterEntriesByFromSeq(entries []model.Entry, fromSeq uint32) []model.Entry {
	filtered := make([]model.Entry, 0, len(entries))
	for _, e := range entries {
		if e.Seq != nil && *e.Seq >= fromSeq {
			filtered = append(filtered, e)
		}
	}
	// Sort by seq ASC
	sort.Slice(filtered, func(i, j int) bool {
		return *filtered[i].Seq < *filtered[j].Seq
	})
	return filtered
}

func decryptEntries(s *SQLiteStore, entries []model.Entry) error {
	for i := range entries {
		decrypted, err := s.decryptEntryContent(entries[i].ID, entries[i].Content)
		if err != nil {
			return fmt.Errorf("failed to decrypt entry content: %w", err)
		}
		entries[i].Content = decrypted
	}
	return nil
}

func flattenMemoryContent(s *SQLiteStore, entries []model.Entry) ([]any, error) {
	result := make([]any, 0)
	for _, entry := range entries {
		content, err := s.decryptEntryContent(entry.ID, entry.Content)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt entry content: %w", err)
		}
		result = append(result, parseContentArray(content)...)
	}
	return result, nil
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

func (s *SQLiteStore) CreateAttachment(ctx context.Context, userID string, conversationID string, attachment model.Attachment) (*model.Attachment, error) {
	db := s.writeDBFor(ctx, "sqlite store create attachment")
	// conversationID is optional; when not provided, create an unlinked attachment
	// owned by the uploader.
	if conversationID != "" {
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
	if err := db.Where("id = ? AND archived_at IS NULL", attachmentID).First(&attachment).Error; err != nil {
		return nil, &registrystore.NotFoundError{Resource: "attachment", ID: attachmentID.String()}
	}
	if attachment.UserID != userID {
		return nil, &registrystore.ForbiddenError{}
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

	if err := db.Where("id = ? AND archived_at IS NULL", attachmentID).First(&attachment).Error; err != nil {
		return nil, &registrystore.NotFoundError{Resource: "attachment", ID: attachmentID.String()}
	}
	return &attachment, nil
}

func (s *SQLiteStore) GetAttachment(ctx context.Context, userID string, conversationID string, attachmentID uuid.UUID) (*model.Attachment, error) {
	var attachment model.Attachment
	if err := s.dbFor(ctx).Where("id = ? AND archived_at IS NULL", attachmentID).First(&attachment).Error; err != nil {
		return nil, &registrystore.NotFoundError{Resource: "attachment", ID: attachmentID.String()}
	}

	// Unlinked attachments are only visible to the uploader.
	if attachment.EntryID == nil {
		if attachment.UserID != userID {
			return nil, &registrystore.ForbiddenError{}
		}
		return &attachment, nil
	}

	tx := s.dbFor(ctx).Where("id = ?", *attachment.EntryID)
	if conversationID != "" {
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
		return nil, &registrystore.NotFoundError{Resource: "attachment", ID: attachmentID.String()}
	}

	var sawForbidden bool
	for _, entry := range entries {
		if _, err := s.requireAccess(ctx, userID, entry.ConversationGroupID, model.AccessLevelReader); err == nil {
			return &attachment, nil
		} else {
			var forbidden *registrystore.ForbiddenError
			if errors.As(err, &forbidden) {
				sawForbidden = true
				continue
			}
			return nil, err
		}
	}
	if sawForbidden {
		return nil, &registrystore.ForbiddenError{}
	}
	return nil, &registrystore.NotFoundError{Resource: "attachment", ID: attachmentID.String()}
}

func (s *SQLiteStore) DeleteAttachment(ctx context.Context, userID string, conversationID string, attachmentID uuid.UUID) error {
	db := s.writeDBFor(ctx, "sqlite store delete attachment")
	attachment, err := s.GetAttachment(ctx, userID, conversationID, attachmentID)
	if err != nil {
		return err
	}

	// Only the uploader can delete, and only before attachment is linked to an entry.
	if attachment.UserID != userID {
		return &registrystore.ForbiddenError{}
	}
	if attachment.EntryID != nil {
		return &registrystore.ConflictError{Message: "linked attachments cannot be deleted"}
	}

	result := db.Where("id = ?", attachmentID).Delete(&model.Attachment{})
	if result.Error != nil {
		return fmt.Errorf("delete attachment failed: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return &registrystore.NotFoundError{Resource: "attachment", ID: attachmentID.String()}
	}
	return nil
}

func (s *SQLiteStore) getGroupID(ctx context.Context, userID string, conversationID string, minLevel model.AccessLevel) (uuid.UUID, error) {
	var conv model.Conversation
	if err := s.dbFor(ctx).Where("id = ? AND archived_at IS NULL", conversationID).First(&conv).Error; err != nil {
		return uuid.Nil, &registrystore.NotFoundError{Resource: "conversation", ID: string(conversationID)}
	}
	if _, err := s.requireAccess(ctx, userID, conv.ConversationGroupID, minLevel); err != nil {
		return uuid.Nil, err
	}
	return conv.ConversationGroupID, nil
}
