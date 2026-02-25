package mongo

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
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func init() {
	registrystore.Register(registrystore.Plugin{
		Name: "mongo",
		Loader: func(ctx context.Context) (registrystore.MemoryStore, error) {
			cfg := config.FromContext(ctx)
			opts := options.Client().ApplyURI(cfg.DBURL)
			if cfg.DBMaxOpenConns > 0 {
				opts.SetMaxPoolSize(uint64(cfg.DBMaxOpenConns))
			}
			if cfg.DBMaxIdleConns > 0 {
				opts.SetMinPoolSize(uint64(cfg.DBMaxIdleConns))
			}
			client, err := mongo.Connect(opts)
			if err != nil {
				return nil, fmt.Errorf("failed to connect to MongoDB: %w", err)
			}
			if err := client.Ping(ctx, nil); err != nil {
				return nil, fmt.Errorf("failed to ping MongoDB: %w", err)
			}

			dbName := "memory_service"
			store := &MongoStore{
				client:       client,
				db:           client.Database(dbName),
				entriesCache: registrycache.EntriesCacheFromContext(ctx),
			}
			if cfg.EncryptionKey != "" {
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

	registrymigrate.Register(registrymigrate.Plugin{Order: 100, Migrator: &mongoMigrator{}})
}

type mongoMigrator struct{}

func (m *mongoMigrator) Name() string { return "mongo-schema" }
func (m *mongoMigrator) Migrate(ctx context.Context) error {
	cfg := config.FromContext(ctx)
	if cfg != nil && !cfg.DatastoreMigrateAtStart {
		return nil
	}
	if cfg.DatastoreType != "mongo" {
		return nil // skip if not using mongo
	}

	log.Info("Running migration", "name", m.Name())
	client, err := mongo.Connect(options.Client().ApplyURI(cfg.DBURL))
	if err != nil {
		return fmt.Errorf("mongo migration: failed to connect: %w", err)
	}
	defer client.Disconnect(ctx)

	db := client.Database("memory_service")

	// Create collections with indexes
	collections := map[string][]mongo.IndexModel{
		"conversation_groups": {
			{Keys: bson.D{{Key: "deleted_at", Value: 1}}},
		},
		"conversations": {
			{Keys: bson.D{{Key: "conversation_group_id", Value: 1}}},
			{Keys: bson.D{{Key: "owner_user_id", Value: 1}}},
			{Keys: bson.D{{Key: "deleted_at", Value: 1}}},
			{Keys: bson.D{{Key: "conversation_group_id", Value: 1}, {Key: "created_at", Value: 1}}},
		},
		"conversation_memberships": {
			{
				Keys:    bson.D{{Key: "conversation_group_id", Value: 1}, {Key: "user_id", Value: 1}},
				Options: options.Index().SetUnique(true),
			},
		},
		"entries": {
			{Keys: bson.D{{Key: "conversation_group_id", Value: 1}, {Key: "conversation_id", Value: 1}}},
			{Keys: bson.D{{Key: "conversation_group_id", Value: 1}, {Key: "created_at", Value: 1}}},
			{Keys: bson.D{{Key: "channel", Value: 1}}},
			{Keys: bson.D{{Key: "indexed_at", Value: 1}}},
			{Keys: bson.D{{Key: "indexed_content", Value: "text"}}},
		},
		"conversation_ownership_transfers": {
			{Keys: bson.D{{Key: "from_user_id", Value: 1}}},
			{Keys: bson.D{{Key: "to_user_id", Value: 1}}},
			{
				Keys:    bson.D{{Key: "conversation_group_id", Value: 1}},
				Options: options.Index().SetUnique(true).SetName("unique_transfer_per_conversation"),
			},
		},
		"attachments": {
			{Keys: bson.D{{Key: "entry_id", Value: 1}}},
			{Keys: bson.D{{Key: "user_id", Value: 1}}},
			{Keys: bson.D{{Key: "deleted_at", Value: 1}}},
		},
		"tasks": {
			{Keys: bson.D{{Key: "retry_at", Value: 1}, {Key: "created_at", Value: 1}}},
			{Keys: bson.D{{Key: "processing_at", Value: 1}}},
			{
				Keys:    bson.D{{Key: "task_name", Value: 1}},
				Options: options.Index().SetUnique(true).SetSparse(true),
			},
		},
	}

	for name, indexes := range collections {
		// Ensure collection exists
		db.CreateCollection(ctx, name)
		if len(indexes) > 0 {
			if _, err := db.Collection(name).Indexes().CreateMany(ctx, indexes); err != nil {
				return fmt.Errorf("mongo migration: failed to create indexes for %s: %w", name, err)
			}
		}
	}

	log.Info("MongoDB schema migration complete")
	return nil
}

// MongoStore implements MemoryStore using MongoDB.
type MongoStore struct {
	client       *mongo.Client
	db           *mongo.Database
	gcms         []cipher.AEAD
	entriesCache registrycache.MemoryEntriesCache
}

// ForceImport is a no-op variable that can be referenced to ensure this package's init() runs.
var ForceImport = 0

// --- Encryption helpers ---

func (s *MongoStore) encrypt(plaintext []byte) []byte {
	if len(s.gcms) == 0 || plaintext == nil {
		return plaintext
	}
	gcm := s.gcms[0]
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		panic(fmt.Sprintf("failed to generate nonce: %v", err))
	}
	return gcm.Seal(nonce, nonce, plaintext, nil)
}

func (s *MongoStore) decrypt(ciphertext []byte) ([]byte, error) {
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
		nonce, ct := ciphertext[:nonceSize], ciphertext[nonceSize:]
		plaintext, err := gcm.Open(nil, nonce, ct, nil)
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

func (s *MongoStore) decryptString(data []byte) string {
	plain, err := s.decrypt(data)
	if err != nil {
		return string(data)
	}
	return string(plain)
}

// --- MongoDB document types ---

type groupDoc struct {
	ID        string     `bson:"_id"`
	CreatedAt time.Time  `bson:"created_at"`
	DeletedAt *time.Time `bson:"deleted_at,omitempty"`
}

type convDoc struct {
	ID                     string         `bson:"_id"`
	Title                  []byte         `bson:"title"`
	OwnerUserID            string         `bson:"owner_user_id"`
	Metadata               map[string]any `bson:"metadata"`
	ConversationGroupID    string         `bson:"conversation_group_id"`
	ForkedAtConversationID *string        `bson:"forked_at_conversation_id,omitempty"`
	ForkedAtEntryID        *string        `bson:"forked_at_entry_id,omitempty"`
	CreatedAt              time.Time      `bson:"created_at"`
	UpdatedAt              time.Time      `bson:"updated_at"`
	DeletedAt              *time.Time     `bson:"deleted_at,omitempty"`
}

type memberDoc struct {
	ConversationGroupID string            `bson:"conversation_group_id"`
	UserID              string            `bson:"user_id"`
	AccessLevel         model.AccessLevel `bson:"access_level"`
	CreatedAt           time.Time         `bson:"created_at"`
}

type entryDoc struct {
	ID                  string     `bson:"_id"`
	ConversationID      string     `bson:"conversation_id"`
	ConversationGroupID string     `bson:"conversation_group_id"`
	UserID              *string    `bson:"user_id,omitempty"`
	ClientID            *string    `bson:"client_id,omitempty"`
	Channel             string     `bson:"channel"`
	Epoch               *int64     `bson:"epoch,omitempty"`
	ContentType         string     `bson:"content_type"`
	Content             []byte     `bson:"content"`
	IndexedContent      *string    `bson:"indexed_content,omitempty"`
	IndexedAt           *time.Time `bson:"indexed_at,omitempty"`
	Role                *string    `bson:"role,omitempty"`
	CreatedAt           time.Time  `bson:"created_at"`
}

// entrySearchDoc is a flat struct (not using bson:",inline") to avoid mongo-driver v2
// inline decoding issues with _id fields in embedded structs.
type entrySearchDoc struct {
	ID                  string     `bson:"_id"`
	ConversationID      string     `bson:"conversation_id"`
	ConversationGroupID string     `bson:"conversation_group_id"`
	UserID              *string    `bson:"user_id,omitempty"`
	ClientID            *string    `bson:"client_id,omitempty"`
	Channel             string     `bson:"channel"`
	Epoch               *int64     `bson:"epoch,omitempty"`
	ContentType         string     `bson:"content_type"`
	Content             []byte     `bson:"content"`
	IndexedContent      *string    `bson:"indexed_content,omitempty"`
	IndexedAt           *time.Time `bson:"indexed_at,omitempty"`
	Role                *string    `bson:"role,omitempty"`
	CreatedAt           time.Time  `bson:"created_at"`
	TextScore           float64    `bson:"score"`
}

func (d entrySearchDoc) asEntryDoc() entryDoc {
	return entryDoc{
		ID:                  d.ID,
		ConversationID:      d.ConversationID,
		ConversationGroupID: d.ConversationGroupID,
		UserID:              d.UserID,
		ClientID:            d.ClientID,
		Channel:             d.Channel,
		Epoch:               d.Epoch,
		ContentType:         d.ContentType,
		Content:             d.Content,
		IndexedContent:      d.IndexedContent,
		IndexedAt:           d.IndexedAt,
		Role:                d.Role,
		CreatedAt:           d.CreatedAt,
	}
}

type transferDoc struct {
	ID                  string    `bson:"_id"`
	ConversationGroupID string    `bson:"conversation_group_id"`
	FromUserID          string    `bson:"from_user_id"`
	ToUserID            string    `bson:"to_user_id"`
	CreatedAt           time.Time `bson:"created_at"`
}

type attachmentDoc struct {
	ID          string     `bson:"_id"`
	StorageKey  *string    `bson:"storage_key,omitempty"`
	Filename    *string    `bson:"filename,omitempty"`
	ContentType string     `bson:"content_type"`
	Size        *int64     `bson:"size,omitempty"`
	SHA256      *string    `bson:"sha256,omitempty"`
	UserID      string     `bson:"user_id"`
	EntryID     *string    `bson:"entry_id,omitempty"`
	Status      string     `bson:"status"`
	SourceURL   *string    `bson:"source_url,omitempty"`
	ExpiresAt   *time.Time `bson:"expires_at,omitempty"`
	CreatedAt   time.Time  `bson:"created_at"`
	DeletedAt   *time.Time `bson:"deleted_at,omitempty"`
}

// --- Collection accessors ---

func (s *MongoStore) groups() *mongo.Collection        { return s.db.Collection("conversation_groups") }
func (s *MongoStore) conversations() *mongo.Collection { return s.db.Collection("conversations") }
func (s *MongoStore) memberships() *mongo.Collection {
	return s.db.Collection("conversation_memberships")
}
func (s *MongoStore) entries() *mongo.Collection { return s.db.Collection("entries") }
func (s *MongoStore) transfers() *mongo.Collection {
	return s.db.Collection("conversation_ownership_transfers")
}
func (s *MongoStore) attachments() *mongo.Collection { return s.db.Collection("attachments") }

// --- UUID helpers ---

func uuidToStr(id uuid.UUID) string { return id.String() }
func strToUUID(s string) uuid.UUID  { u, _ := uuid.Parse(s); return u }
func ptrUUIDToStr(id *uuid.UUID) *string {
	if id == nil {
		return nil
	}
	s := id.String()
	return &s
}
func ptrStrToUUID(s *string) *uuid.UUID {
	if s == nil {
		return nil
	}
	u := strToUUID(*s)
	return &u
}

// --- Access control ---

func (s *MongoStore) requireAccess(ctx context.Context, userID string, groupID string, minLevel model.AccessLevel) (model.AccessLevel, error) {
	var doc memberDoc
	err := s.memberships().FindOne(ctx, bson.M{
		"conversation_group_id": groupID,
		"user_id":               userID,
	}).Decode(&doc)
	if err != nil {
		return "", &registrystore.ForbiddenError{}
	}
	if !doc.AccessLevel.IsAtLeast(minLevel) {
		return "", &registrystore.ForbiddenError{}
	}
	return doc.AccessLevel, nil
}

func (s *MongoStore) getGroupID(ctx context.Context, userID string, conversationID uuid.UUID, minLevel model.AccessLevel) (string, error) {
	var doc convDoc
	err := s.conversations().FindOne(ctx, bson.M{
		"_id":        uuidToStr(conversationID),
		"deleted_at": bson.M{"$exists": false},
	}).Decode(&doc)
	if err != nil {
		return "", &registrystore.NotFoundError{Resource: "conversation", ID: conversationID.String()}
	}
	if _, err := s.requireAccess(ctx, userID, doc.ConversationGroupID, minLevel); err != nil {
		return "", err
	}
	return doc.ConversationGroupID, nil
}

// resolveConversationID finds the primary (non-deleted) conversation ID for a group.
func (s *MongoStore) resolveConversationID(ctx context.Context, groupID string) uuid.UUID {
	var conv convDoc
	err := s.conversations().FindOne(ctx, bson.M{
		"conversation_group_id": groupID,
		"deleted_at":            bson.M{"$exists": false},
	}).Decode(&conv)
	if err != nil {
		return uuid.Nil
	}
	return strToUUID(conv.ID)
}

// --- Conversations ---

func (s *MongoStore) CreateConversation(ctx context.Context, userID string, title string, metadata map[string]any, forkedAtConversationID *uuid.UUID, forkedAtEntryID *uuid.UUID) (*registrystore.ConversationDetail, error) {
	return s.createConversation(ctx, userID, uuid.New(), title, metadata, forkedAtConversationID, forkedAtEntryID)
}

func (s *MongoStore) CreateConversationWithID(ctx context.Context, userID string, convID uuid.UUID, title string, metadata map[string]any, forkedAtConversationID *uuid.UUID, forkedAtEntryID *uuid.UUID) (*registrystore.ConversationDetail, error) {
	return s.createConversation(ctx, userID, convID, title, metadata, forkedAtConversationID, forkedAtEntryID)
}

func (s *MongoStore) createConversation(ctx context.Context, userID string, convID uuid.UUID, title string, metadata map[string]any, forkedAtConversationID *uuid.UUID, forkedAtEntryID *uuid.UUID) (*registrystore.ConversationDetail, error) {
	groupID := uuid.New()
	now := time.Now()

	if metadata == nil {
		metadata = map[string]any{}
	}

	var actualGroupID string
	if forkedAtConversationID != nil {
		var sourceConv convDoc
		err := s.conversations().FindOne(ctx, bson.M{
			"_id":        uuidToStr(*forkedAtConversationID),
			"deleted_at": bson.M{"$exists": false},
		}).Decode(&sourceConv)
		if err != nil {
			return nil, &registrystore.NotFoundError{Resource: "conversation", ID: forkedAtConversationID.String()}
		}
		if _, err := s.requireAccess(ctx, userID, sourceConv.ConversationGroupID, model.AccessLevelReader); err != nil {
			return nil, err
		}
		if forkedAtEntryID != nil {
			var entry entryDoc
			err := s.entries().FindOne(ctx, bson.M{
				"_id":                   uuidToStr(*forkedAtEntryID),
				"conversation_group_id": sourceConv.ConversationGroupID,
			}).Decode(&entry)
			if err != nil {
				return nil, &registrystore.NotFoundError{Resource: "entry", ID: forkedAtEntryID.String()}
			}
			if model.Channel(entry.Channel) != model.ChannelHistory {
				return nil, &registrystore.ValidationError{Field: "forkedAtEntryId", Message: "can only fork at HISTORY entries"}
			}
			// Java parity: forkedAtEntryId stored is the entry BEFORE the fork point.
			var prevEntry entryDoc
			err = s.entries().FindOne(ctx, bson.M{
				"conversation_group_id": sourceConv.ConversationGroupID,
				"created_at":            bson.M{"$lt": entry.CreatedAt},
			}, options.FindOne().SetSort(bson.D{{Key: "created_at", Value: -1}})).Decode(&prevEntry)
			if err == nil {
				prevID := strToUUID(prevEntry.ID)
				forkedAtEntryID = &prevID
			}
			// else: no previous entry — fork is at the very first entry.
			// Keep the original entry ID as the stop point.
		}
		actualGroupID = sourceConv.ConversationGroupID
	} else {
		actualGroupID = uuidToStr(groupID)
		_, err := s.groups().InsertOne(ctx, groupDoc{
			ID:        actualGroupID,
			CreatedAt: now,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create conversation group: %w", err)
		}
	}

	doc := convDoc{
		ID:                     uuidToStr(convID),
		Title:                  s.encrypt([]byte(title)),
		OwnerUserID:            userID,
		Metadata:               metadata,
		ConversationGroupID:    actualGroupID,
		ForkedAtConversationID: ptrUUIDToStr(forkedAtConversationID),
		ForkedAtEntryID:        ptrUUIDToStr(forkedAtEntryID),
		CreatedAt:              now,
		UpdatedAt:              now,
	}
	if _, err := s.conversations().InsertOne(ctx, doc); err != nil {
		return nil, fmt.Errorf("failed to create conversation: %w", err)
	}

	if forkedAtConversationID == nil {
		_, err := s.memberships().InsertOne(ctx, memberDoc{
			ConversationGroupID: actualGroupID,
			UserID:              userID,
			AccessLevel:         model.AccessLevelOwner,
			CreatedAt:           now,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create membership: %w", err)
		}
	}

	return &registrystore.ConversationDetail{
		ConversationSummary: registrystore.ConversationSummary{
			ID:                     convID,
			Title:                  title,
			OwnerUserID:            userID,
			Metadata:               metadata,
			ConversationGroupID:    strToUUID(actualGroupID),
			ForkedAtConversationID: forkedAtConversationID,
			ForkedAtEntryID:        forkedAtEntryID,
			CreatedAt:              now,
			UpdatedAt:              now,
			AccessLevel:            model.AccessLevelOwner,
		},
	}, nil
}

func (s *MongoStore) ListConversations(ctx context.Context, userID string, query *string, afterCursor *string, limit int, mode model.ConversationListMode) ([]registrystore.ConversationSummary, *string, error) {
	// Find all groups the user has membership in
	cursor, err := s.memberships().Find(ctx, bson.M{"user_id": userID})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to find memberships: %w", err)
	}
	var mems []memberDoc
	if err := cursor.All(ctx, &mems); err != nil {
		return nil, nil, fmt.Errorf("failed to decode memberships: %w", err)
	}

	accessMap := map[string]model.AccessLevel{}
	groupIDs := make([]string, 0, len(mems))
	for _, m := range mems {
		groupIDs = append(groupIDs, m.ConversationGroupID)
		accessMap[m.ConversationGroupID] = m.AccessLevel
	}

	if len(groupIDs) == 0 {
		return []registrystore.ConversationSummary{}, nil, nil
	}

	filter := bson.M{
		"conversation_group_id": bson.M{"$in": groupIDs},
		"deleted_at":            bson.M{"$exists": false},
	}

	switch mode {
	case model.ListModeRoots:
		filter["forked_at_conversation_id"] = bson.M{"$exists": false}
	case model.ListModeLatestFork:
		return s.listConversationsLatestFork(ctx, filter, accessMap, afterCursor, limit)
	}

	if afterCursor != nil {
		var cursorDoc convDoc
		err := s.conversations().FindOne(ctx, bson.M{"_id": *afterCursor}).Decode(&cursorDoc)
		if err == nil {
			filter["created_at"] = bson.M{"$gt": cursorDoc.CreatedAt}
		}
	}

	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}).SetLimit(int64(limit + 1))
	cur, err := s.conversations().Find(ctx, filter, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list conversations: %w", err)
	}
	var docs []convDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, nil, fmt.Errorf("failed to decode conversations: %w", err)
	}

	hasMore := len(docs) > limit
	if hasMore {
		docs = docs[:limit]
	}

	summaries := make([]registrystore.ConversationSummary, len(docs))
	for i, d := range docs {
		al := accessMap[d.ConversationGroupID]
		summaries[i] = registrystore.ConversationSummary{
			ID:                     strToUUID(d.ID),
			Title:                  s.decryptString(d.Title),
			OwnerUserID:            d.OwnerUserID,
			Metadata:               d.Metadata,
			ConversationGroupID:    strToUUID(d.ConversationGroupID),
			ForkedAtConversationID: ptrStrToUUID(d.ForkedAtConversationID),
			ForkedAtEntryID:        ptrStrToUUID(d.ForkedAtEntryID),
			CreatedAt:              d.CreatedAt,
			UpdatedAt:              d.UpdatedAt,
			DeletedAt:              d.DeletedAt,
			AccessLevel:            al,
		}
	}

	var nextCursor *string
	if hasMore && len(summaries) > 0 {
		c := summaries[len(summaries)-1].ID.String()
		nextCursor = &c
	}
	return summaries, nextCursor, nil
}

// listConversationsLatestFork returns only the most recently updated conversation per group.
func (s *MongoStore) listConversationsLatestFork(ctx context.Context, baseFilter bson.M, accessMap map[string]model.AccessLevel, afterCursor *string, limit int) ([]registrystore.ConversationSummary, *string, error) {
	// Load all candidates, then keep only the one with max updated_at per group.
	opts := options.Find().SetSort(bson.D{{Key: "updated_at", Value: -1}, {Key: "created_at", Value: 1}})
	cur, err := s.conversations().Find(ctx, baseFilter, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list conversations (latest-fork): %w", err)
	}
	var docs []convDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, nil, fmt.Errorf("failed to decode conversations: %w", err)
	}

	// Keep only the most recently updated per group.
	seen := map[string]bool{}
	var filtered []convDoc
	for _, d := range docs {
		if seen[d.ConversationGroupID] {
			continue
		}
		seen[d.ConversationGroupID] = true
		filtered = append(filtered, d)
	}

	// Sort by created_at ASC for pagination consistency.
	for i := 0; i < len(filtered); i++ {
		for j := i + 1; j < len(filtered); j++ {
			if filtered[j].CreatedAt.Before(filtered[i].CreatedAt) {
				filtered[i], filtered[j] = filtered[j], filtered[i]
			}
		}
	}

	// Apply cursor-based pagination.
	start := 0
	if afterCursor != nil {
		for i, d := range filtered {
			if d.ID == *afterCursor {
				start = i + 1
				break
			}
		}
	}
	if start > len(filtered) {
		start = len(filtered)
	}
	filtered = filtered[start:]

	hasMore := len(filtered) > limit
	if hasMore {
		filtered = filtered[:limit]
	}

	summaries := make([]registrystore.ConversationSummary, len(filtered))
	for i, d := range filtered {
		al := accessMap[d.ConversationGroupID]
		summaries[i] = registrystore.ConversationSummary{
			ID:                     strToUUID(d.ID),
			Title:                  s.decryptString(d.Title),
			OwnerUserID:            d.OwnerUserID,
			Metadata:               d.Metadata,
			ConversationGroupID:    strToUUID(d.ConversationGroupID),
			ForkedAtConversationID: ptrStrToUUID(d.ForkedAtConversationID),
			ForkedAtEntryID:        ptrStrToUUID(d.ForkedAtEntryID),
			CreatedAt:              d.CreatedAt,
			UpdatedAt:              d.UpdatedAt,
			AccessLevel:            al,
		}
	}

	var nextCursor *string
	if hasMore && len(summaries) > 0 {
		c := summaries[len(summaries)-1].ID.String()
		nextCursor = &c
	}
	return summaries, nextCursor, nil
}

func (s *MongoStore) GetConversation(ctx context.Context, userID string, conversationID uuid.UUID) (*registrystore.ConversationDetail, error) {
	var doc convDoc
	err := s.conversations().FindOne(ctx, bson.M{
		"_id":        uuidToStr(conversationID),
		"deleted_at": bson.M{"$exists": false},
	}).Decode(&doc)
	if err != nil {
		return nil, &registrystore.NotFoundError{Resource: "conversation", ID: conversationID.String()}
	}
	access, err := s.requireAccess(ctx, userID, doc.ConversationGroupID, model.AccessLevelReader)
	if err != nil {
		return nil, err
	}

	return &registrystore.ConversationDetail{
		ConversationSummary: registrystore.ConversationSummary{
			ID:                     strToUUID(doc.ID),
			Title:                  s.decryptString(doc.Title),
			OwnerUserID:            doc.OwnerUserID,
			Metadata:               doc.Metadata,
			ConversationGroupID:    strToUUID(doc.ConversationGroupID),
			ForkedAtConversationID: ptrStrToUUID(doc.ForkedAtConversationID),
			ForkedAtEntryID:        ptrStrToUUID(doc.ForkedAtEntryID),
			CreatedAt:              doc.CreatedAt,
			UpdatedAt:              doc.UpdatedAt,
			AccessLevel:            access,
		},
	}, nil
}

func (s *MongoStore) UpdateConversation(ctx context.Context, userID string, conversationID uuid.UUID, title *string, metadata map[string]any) (*registrystore.ConversationDetail, error) {
	var doc convDoc
	err := s.conversations().FindOne(ctx, bson.M{
		"_id":        uuidToStr(conversationID),
		"deleted_at": bson.M{"$exists": false},
	}).Decode(&doc)
	if err != nil {
		return nil, &registrystore.NotFoundError{Resource: "conversation", ID: conversationID.String()}
	}
	if _, err := s.requireAccess(ctx, userID, doc.ConversationGroupID, model.AccessLevelWriter); err != nil {
		return nil, err
	}

	update := bson.M{"$set": bson.M{"updated_at": time.Now()}}
	sets := update["$set"].(bson.M)
	if title != nil {
		sets["title"] = s.encrypt([]byte(*title))
	}
	if metadata != nil {
		sets["metadata"] = metadata
	}

	_, err = s.conversations().UpdateByID(ctx, uuidToStr(conversationID), update)
	if err != nil {
		return nil, fmt.Errorf("failed to update conversation: %w", err)
	}
	return s.GetConversation(ctx, userID, conversationID)
}

func (s *MongoStore) DeleteConversation(ctx context.Context, userID string, conversationID uuid.UUID) error {
	var doc convDoc
	err := s.conversations().FindOne(ctx, bson.M{
		"_id":        uuidToStr(conversationID),
		"deleted_at": bson.M{"$exists": false},
	}).Decode(&doc)
	if err != nil {
		return &registrystore.NotFoundError{Resource: "conversation", ID: conversationID.String()}
	}
	if _, err := s.requireAccess(ctx, userID, doc.ConversationGroupID, model.AccessLevelOwner); err != nil {
		return err
	}

	now := time.Now()
	s.groups().UpdateByID(ctx, doc.ConversationGroupID, bson.M{"$set": bson.M{"deleted_at": now}})
	s.conversations().UpdateMany(ctx,
		bson.M{"conversation_group_id": doc.ConversationGroupID, "deleted_at": bson.M{"$exists": false}},
		bson.M{"$set": bson.M{"deleted_at": now}},
	)

	// Java/Postgres parity: hard-delete memberships, entries, and transfers in the group.
	s.memberships().DeleteMany(ctx, bson.M{"conversation_group_id": doc.ConversationGroupID})
	s.entries().DeleteMany(ctx, bson.M{"conversation_group_id": doc.ConversationGroupID})
	s.transfers().DeleteMany(ctx, bson.M{"conversation_group_id": doc.ConversationGroupID})
	return nil
}

// --- Memberships ---

func (s *MongoStore) ListMemberships(ctx context.Context, userID string, conversationID uuid.UUID, afterCursor *string, limit int) ([]model.ConversationMembership, *string, error) {
	groupID, err := s.getGroupID(ctx, userID, conversationID, model.AccessLevelReader)
	if err != nil {
		return nil, nil, err
	}

	filter := bson.M{"conversation_group_id": groupID}
	if afterCursor != nil {
		var cursorDoc memberDoc
		err := s.memberships().FindOne(ctx, bson.M{
			"conversation_group_id": groupID,
			"user_id":               *afterCursor,
		}).Decode(&cursorDoc)
		if err == nil {
			filter["created_at"] = bson.M{"$gt": cursorDoc.CreatedAt}
		}
	}

	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}).SetLimit(int64(limit + 1))
	cur, err := s.memberships().Find(ctx, filter, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list memberships: %w", err)
	}
	var docs []memberDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, nil, fmt.Errorf("failed to decode memberships: %w", err)
	}

	hasMore := len(docs) > limit
	if hasMore {
		docs = docs[:limit]
	}

	result := make([]model.ConversationMembership, len(docs))
	for i, d := range docs {
		result[i] = model.ConversationMembership{
			ConversationGroupID: strToUUID(d.ConversationGroupID),
			UserID:              d.UserID,
			AccessLevel:         d.AccessLevel,
			CreatedAt:           d.CreatedAt,
		}
	}

	var nextCursor *string
	if hasMore && len(result) > 0 {
		c := result[len(result)-1].UserID
		nextCursor = &c
	}
	return result, nextCursor, nil
}

func (s *MongoStore) ShareConversation(ctx context.Context, userID string, conversationID uuid.UUID, targetUserID string, accessLevel model.AccessLevel) (*model.ConversationMembership, error) {
	groupID, err := s.getGroupID(ctx, userID, conversationID, model.AccessLevelManager)
	if err != nil {
		return nil, err
	}
	if accessLevel == model.AccessLevelOwner {
		return nil, &registrystore.ValidationError{Field: "accessLevel", Message: "cannot share with owner access; use ownership transfer"}
	}

	doc := memberDoc{
		ConversationGroupID: groupID,
		UserID:              targetUserID,
		AccessLevel:         accessLevel,
		CreatedAt:           time.Now(),
	}
	_, err = s.memberships().InsertOne(ctx, doc)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return nil, &registrystore.ConflictError{Message: "user already has access to this conversation"}
		}
		return nil, fmt.Errorf("failed to share conversation: %w", err)
	}
	return &model.ConversationMembership{
		ConversationGroupID: strToUUID(groupID),
		UserID:              targetUserID,
		AccessLevel:         accessLevel,
		CreatedAt:           doc.CreatedAt,
	}, nil
}

func (s *MongoStore) UpdateMembership(ctx context.Context, userID string, conversationID uuid.UUID, memberUserID string, accessLevel model.AccessLevel) (*model.ConversationMembership, error) {
	groupID, err := s.getGroupID(ctx, userID, conversationID, model.AccessLevelManager)
	if err != nil {
		return nil, err
	}
	if accessLevel == model.AccessLevelOwner {
		return nil, &registrystore.ValidationError{Field: "accessLevel", Message: "cannot set owner access; use ownership transfer"}
	}

	result, err := s.memberships().UpdateOne(ctx,
		bson.M{"conversation_group_id": groupID, "user_id": memberUserID},
		bson.M{"$set": bson.M{"access_level": accessLevel}},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to update membership: %w", err)
	}
	if result.MatchedCount == 0 {
		return nil, &registrystore.NotFoundError{Resource: "membership", ID: memberUserID}
	}

	var doc memberDoc
	s.memberships().FindOne(ctx, bson.M{"conversation_group_id": groupID, "user_id": memberUserID}).Decode(&doc)
	return &model.ConversationMembership{
		ConversationGroupID: strToUUID(doc.ConversationGroupID),
		UserID:              doc.UserID,
		AccessLevel:         doc.AccessLevel,
		CreatedAt:           doc.CreatedAt,
	}, nil
}

func (s *MongoStore) DeleteMembership(ctx context.Context, userID string, conversationID uuid.UUID, memberUserID string) error {
	groupID, err := s.getGroupID(ctx, userID, conversationID, model.AccessLevelManager)
	if err != nil {
		return err
	}

	var doc memberDoc
	err = s.memberships().FindOne(ctx, bson.M{
		"conversation_group_id": groupID,
		"user_id":               memberUserID,
	}).Decode(&doc)
	if err != nil {
		return &registrystore.NotFoundError{Resource: "membership", ID: memberUserID}
	}
	if doc.AccessLevel == model.AccessLevelOwner {
		return &registrystore.ValidationError{Field: "userId", Message: "cannot remove the owner"}
	}

	s.memberships().DeleteOne(ctx, bson.M{
		"conversation_group_id": groupID,
		"user_id":               memberUserID,
	})

	// Parity with Java: removing the transfer recipient cancels pending transfers.
	s.transfers().DeleteMany(ctx, bson.M{
		"conversation_group_id": groupID,
		"to_user_id":            memberUserID,
	})
	return nil
}

// --- Forks ---

func (s *MongoStore) ListForks(ctx context.Context, userID string, conversationID uuid.UUID, afterCursor *string, limit int) ([]registrystore.ConversationForkSummary, *string, error) {
	groupID, err := s.getGroupID(ctx, userID, conversationID, model.AccessLevelReader)
	if err != nil {
		return nil, nil, err
	}

	filter := bson.M{
		"conversation_group_id": groupID,
		"deleted_at":            bson.M{"$exists": false},
	}
	if afterCursor != nil {
		var cursorDoc convDoc
		err := s.conversations().FindOne(ctx, bson.M{"_id": *afterCursor}).Decode(&cursorDoc)
		if err == nil {
			filter["created_at"] = bson.M{"$gt": cursorDoc.CreatedAt}
		}
	}

	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}).SetLimit(int64(limit + 1))
	cur, err := s.conversations().Find(ctx, filter, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list forks: %w", err)
	}
	var docs []convDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, nil, fmt.Errorf("failed to decode forks: %w", err)
	}

	hasMore := len(docs) > limit
	if hasMore {
		docs = docs[:limit]
	}

	forks := make([]registrystore.ConversationForkSummary, len(docs))
	for i, d := range docs {
		forks[i] = registrystore.ConversationForkSummary{
			ID:                     strToUUID(d.ID),
			Title:                  s.decryptString(d.Title),
			ForkedAtEntryID:        ptrStrToUUID(d.ForkedAtEntryID),
			ForkedAtConversationID: ptrStrToUUID(d.ForkedAtConversationID),
			CreatedAt:              d.CreatedAt,
		}
	}

	var nextCursor *string
	if hasMore && len(forks) > 0 {
		c := forks[len(forks)-1].ID.String()
		nextCursor = &c
	}
	return forks, nextCursor, nil
}

// --- Ownership Transfers ---

func (s *MongoStore) ListPendingTransfers(ctx context.Context, userID string, role string, afterCursor *string, limit int) ([]registrystore.OwnershipTransferDto, *string, error) {
	filter := bson.M{}
	switch role {
	case "sender":
		filter["from_user_id"] = userID
	case "recipient":
		filter["to_user_id"] = userID
	default:
		filter["$or"] = bson.A{
			bson.M{"from_user_id": userID},
			bson.M{"to_user_id": userID},
		}
	}

	if afterCursor != nil {
		var cursorDoc transferDoc
		err := s.transfers().FindOne(ctx, bson.M{"_id": *afterCursor}).Decode(&cursorDoc)
		if err == nil {
			filter["created_at"] = bson.M{"$gt": cursorDoc.CreatedAt}
		}
	}

	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}).SetLimit(int64(limit + 1))
	cur, err := s.transfers().Find(ctx, filter, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list transfers: %w", err)
	}
	var docs []transferDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, nil, fmt.Errorf("failed to decode transfers: %w", err)
	}

	hasMore := len(docs) > limit
	if hasMore {
		docs = docs[:limit]
	}

	dtos := make([]registrystore.OwnershipTransferDto, len(docs))
	for i, d := range docs {
		dtos[i] = registrystore.OwnershipTransferDto{
			ID:                  strToUUID(d.ID),
			ConversationGroupID: strToUUID(d.ConversationGroupID),
			ConversationID:      s.resolveConversationID(ctx, d.ConversationGroupID),
			FromUserID:          d.FromUserID,
			ToUserID:            d.ToUserID,
			CreatedAt:           d.CreatedAt,
		}
	}

	var nextCursor *string
	if hasMore && len(dtos) > 0 {
		c := dtos[len(dtos)-1].ID.String()
		nextCursor = &c
	}
	return dtos, nextCursor, nil
}

func (s *MongoStore) GetTransfer(ctx context.Context, userID string, transferID uuid.UUID) (*registrystore.OwnershipTransferDto, error) {
	var doc transferDoc
	err := s.transfers().FindOne(ctx, bson.M{"_id": uuidToStr(transferID)}).Decode(&doc)
	if err != nil {
		return nil, &registrystore.NotFoundError{Resource: "transfer", ID: transferID.String()}
	}
	if doc.FromUserID != userID && doc.ToUserID != userID {
		return nil, &registrystore.NotFoundError{Resource: "transfer", ID: transferID.String()}
	}
	return &registrystore.OwnershipTransferDto{
		ID:                  strToUUID(doc.ID),
		ConversationGroupID: strToUUID(doc.ConversationGroupID),
		ConversationID:      s.resolveConversationID(ctx, doc.ConversationGroupID),
		FromUserID:          doc.FromUserID,
		ToUserID:            doc.ToUserID,
		CreatedAt:           doc.CreatedAt,
	}, nil
}

func (s *MongoStore) CreateOwnershipTransfer(ctx context.Context, userID string, conversationID uuid.UUID, toUserID string) (*registrystore.OwnershipTransferDto, error) {
	var conv convDoc
	err := s.conversations().FindOne(ctx, bson.M{
		"_id":        uuidToStr(conversationID),
		"deleted_at": bson.M{"$exists": false},
	}).Decode(&conv)
	if err != nil {
		return nil, &registrystore.NotFoundError{Resource: "conversation", ID: conversationID.String()}
	}
	if _, err := s.requireAccess(ctx, userID, conv.ConversationGroupID, model.AccessLevelOwner); err != nil {
		return nil, err
	}
	if userID == toUserID {
		return nil, &registrystore.ValidationError{Field: "newOwnerUserId", Message: "cannot transfer to yourself"}
	}
	// Parity with Java behavior: recipient must already be a conversation member.
	memberCount, err := s.memberships().CountDocuments(ctx, bson.M{
		"conversation_group_id": conv.ConversationGroupID,
		"user_id":               toUserID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to validate transfer recipient membership: %w", err)
	}
	if memberCount == 0 {
		return nil, &registrystore.ValidationError{Field: "newOwnerUserId", Message: "recipient must already be a member"}
	}

	// Java parity: query-before-insert duplicate check.
	var existingTransfer transferDoc
	if err := s.transfers().FindOne(ctx, bson.M{"conversation_group_id": conv.ConversationGroupID}).Decode(&existingTransfer); err == nil {
		return nil, &registrystore.ConflictError{
			Message: "a transfer is already pending for this conversation",
			Code:    "TRANSFER_ALREADY_PENDING",
			Details: map[string]interface{}{"existingTransferId": existingTransfer.ID},
		}
	}

	doc := transferDoc{
		ID:                  uuidToStr(uuid.New()),
		ConversationGroupID: conv.ConversationGroupID,
		FromUserID:          userID,
		ToUserID:            toUserID,
		CreatedAt:           time.Now(),
	}
	_, err = s.transfers().InsertOne(ctx, doc)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return nil, &registrystore.ConflictError{Message: "a transfer is already pending for this conversation"}
		}
		return nil, fmt.Errorf("failed to create transfer: %w", err)
	}
	return &registrystore.OwnershipTransferDto{
		ID:                  strToUUID(doc.ID),
		ConversationGroupID: strToUUID(doc.ConversationGroupID),
		ConversationID:      s.resolveConversationID(ctx, doc.ConversationGroupID),
		FromUserID:          doc.FromUserID,
		ToUserID:            doc.ToUserID,
		CreatedAt:           doc.CreatedAt,
	}, nil
}

func (s *MongoStore) AcceptTransfer(ctx context.Context, userID string, transferID uuid.UUID) error {
	var doc transferDoc
	err := s.transfers().FindOne(ctx, bson.M{"_id": uuidToStr(transferID)}).Decode(&doc)
	if err != nil {
		return &registrystore.NotFoundError{Resource: "transfer", ID: transferID.String()}
	}
	if doc.ToUserID != userID {
		return &registrystore.ForbiddenError{}
	}

	// Update old owner to manager
	s.memberships().UpdateOne(ctx,
		bson.M{"conversation_group_id": doc.ConversationGroupID, "user_id": doc.FromUserID},
		bson.M{"$set": bson.M{"access_level": model.AccessLevelManager}},
	)

	// Upsert new owner
	count, _ := s.memberships().CountDocuments(ctx, bson.M{
		"conversation_group_id": doc.ConversationGroupID,
		"user_id":               doc.ToUserID,
	})
	if count == 0 {
		s.memberships().InsertOne(ctx, memberDoc{
			ConversationGroupID: doc.ConversationGroupID,
			UserID:              doc.ToUserID,
			AccessLevel:         model.AccessLevelOwner,
			CreatedAt:           time.Now(),
		})
	} else {
		s.memberships().UpdateOne(ctx,
			bson.M{"conversation_group_id": doc.ConversationGroupID, "user_id": doc.ToUserID},
			bson.M{"$set": bson.M{"access_level": model.AccessLevelOwner}},
		)
	}

	// Update conversation owner
	s.conversations().UpdateMany(ctx,
		bson.M{"conversation_group_id": doc.ConversationGroupID, "deleted_at": bson.M{"$exists": false}},
		bson.M{"$set": bson.M{"owner_user_id": doc.ToUserID}},
	)

	// Delete transfer
	s.transfers().DeleteOne(ctx, bson.M{"_id": uuidToStr(transferID)})
	return nil
}

func (s *MongoStore) DeleteTransfer(ctx context.Context, userID string, transferID uuid.UUID) error {
	var doc transferDoc
	err := s.transfers().FindOne(ctx, bson.M{"_id": uuidToStr(transferID)}).Decode(&doc)
	if err != nil {
		return &registrystore.NotFoundError{Resource: "transfer", ID: transferID.String()}
	}
	if doc.FromUserID != userID && doc.ToUserID != userID {
		return &registrystore.ForbiddenError{}
	}
	s.transfers().DeleteOne(ctx, bson.M{"_id": uuidToStr(transferID)})
	return nil
}

// --- Entries ---

func (s *MongoStore) GetEntries(ctx context.Context, userID string, conversationID uuid.UUID, afterEntryID *string, limit int, channel *model.Channel, epochFilter *registrystore.MemoryEpochFilter, clientID *string, allForks bool) (*registrystore.PagedEntries, error) {
	var conv convDoc
	err := s.conversations().FindOne(ctx, bson.M{
		"_id":        uuidToStr(conversationID),
		"deleted_at": bson.M{"$exists": false},
	}).Decode(&conv)
	if err != nil {
		return nil, &registrystore.NotFoundError{Resource: "conversation", ID: conversationID.String()}
	}
	if _, err := s.requireAccess(ctx, userID, conv.ConversationGroupID, model.AccessLevelReader); err != nil {
		return nil, err
	}

	if limit <= 0 {
		limit = 50
	}

	// channel==nil means "all channels" (agent without filter).
	var effectiveChannel model.Channel
	if channel != nil {
		effectiveChannel = *channel
	}
	if effectiveChannel == model.ChannelMemory && clientID == nil {
		return nil, &registrystore.ForbiddenError{}
	}

	if allForks {
		docs, err := s.loadEntriesForGroup(ctx, conv.ConversationGroupID)
		if err != nil {
			return nil, err
		}
		filtered := filterEntriesForAllForksDocs(docs, effectiveChannel, clientID, epochFilter)
		filtered, nextCursor := paginateEntryDocs(filtered, afterEntryID, limit)
		entries := make([]model.Entry, len(filtered))
		for i, d := range filtered {
			content, _ := s.decrypt(d.Content)
			entries[i] = s.entryDocToModel(d)
			entries[i].Content = content
		}
		return &registrystore.PagedEntries{Data: entries, AfterCursor: nextCursor}, nil
	}

	ancestry, err := s.buildAncestryStack(ctx, conv)
	if err != nil {
		return nil, err
	}

	if effectiveChannel == model.ChannelMemory {
		if epochFilter == nil || epochFilter.Mode == registrystore.MemoryEpochModeLatest {
			// Use cache for the common latest-epoch case.
			cachedEntries, err := s.fetchLatestMemoryEntries(ctx, conv, ancestry, *clientID)
			if err != nil {
				return nil, err
			}
			page, nextCursor := paginateEntriesModel(cachedEntries, afterEntryID, limit)
			for i := range page {
				if dec, err := s.decrypt(page[i].Content); err == nil {
					page[i].Content = dec
				}
			}
			return &registrystore.PagedEntries{Data: page, AfterCursor: nextCursor}, nil
		}
		docs, err := s.loadEntriesForGroup(ctx, conv.ConversationGroupID)
		if err != nil {
			return nil, err
		}
		filtered := filterMemoryEntriesWithEpochDocs(docs, ancestry, *clientID, epochFilter)
		filtered, nextCursor := paginateEntryDocs(filtered, afterEntryID, limit)
		entries := make([]model.Entry, len(filtered))
		for i, d := range filtered {
			content, _ := s.decrypt(d.Content)
			entries[i] = s.entryDocToModel(d)
			entries[i].Content = content
		}
		return &registrystore.PagedEntries{Data: entries, AfterCursor: nextCursor}, nil
	}

	docs, err := s.loadEntriesForGroup(ctx, conv.ConversationGroupID)
	if err != nil {
		return nil, err
	}
	var filtered []entryDoc
	if effectiveChannel == "" && clientID != nil {
		filtered = filterEntriesByAncestryDocs(docs, ancestry)
	} else {
		filtered = filterEntriesByAncestryDocs(docs, ancestry)
		if effectiveChannel != "" {
			tmp := filtered[:0]
			for _, entry := range filtered {
				if strings.EqualFold(entry.Channel, string(effectiveChannel)) {
					tmp = append(tmp, entry)
				}
			}
			filtered = tmp
		}
	}
	filtered, nextCursor := paginateEntryDocs(filtered, afterEntryID, limit)
	entries := make([]model.Entry, len(filtered))
	for i, d := range filtered {
		content, _ := s.decrypt(d.Content)
		entries[i] = s.entryDocToModel(d)
		entries[i].Content = content
	}
	return &registrystore.PagedEntries{Data: entries, AfterCursor: nextCursor}, nil
}

func (s *MongoStore) GetEntryGroupID(ctx context.Context, entryID uuid.UUID) (uuid.UUID, error) {
	var entry entryDoc
	err := s.entries().FindOne(ctx, bson.M{"_id": uuidToStr(entryID)}).Decode(&entry)
	if err != nil {
		return uuid.Nil, &registrystore.NotFoundError{Resource: "entry", ID: entryID.String()}
	}
	return strToUUID(entry.ConversationGroupID), nil
}

func (s *MongoStore) AppendEntries(ctx context.Context, userID string, conversationID uuid.UUID, entries []registrystore.CreateEntryRequest, clientID *string, epoch *int64) ([]model.Entry, error) {
	var conv convDoc
	err := s.conversations().FindOne(ctx, bson.M{
		"_id":        uuidToStr(conversationID),
		"deleted_at": bson.M{"$exists": false},
	}).Decode(&conv)
	if err != nil {
		// Auto-create conversation if it doesn't exist (Java/Postgres parity).
		// Check first entry for fork metadata.
		var forkedAtConvID *uuid.UUID
		var forkedAtEntryID *uuid.UUID
		if len(entries) > 0 {
			forkedAtConvID = entries[0].ForkedAtConversationID
			forkedAtEntryID = entries[0].ForkedAtEntryID
		}
		detail, createErr := s.createConversation(ctx, userID, conversationID, "", nil, forkedAtConvID, forkedAtEntryID)
		if createErr != nil {
			return nil, createErr
		}
		conv = convDoc{
			ID:                  uuidToStr(detail.ID),
			ConversationGroupID: uuidToStr(detail.ConversationGroupID),
			OwnerUserID:         detail.OwnerUserID,
		}
	}
	if _, err := s.requireAccess(ctx, userID, conv.ConversationGroupID, model.AccessLevelWriter); err != nil {
		return nil, err
	}

	now := time.Now()
	result := make([]model.Entry, len(entries))
	for i, req := range entries {
		ch := strings.ToLower(req.Channel)
		if ch == "" {
			ch = string(model.ChannelHistory)
		}

		// Auto-assign epoch=1 for memory entries when no epoch specified.
		entryEpoch := epoch
		if model.Channel(ch) == model.ChannelMemory && entryEpoch == nil {
			var one int64 = 1
			entryEpoch = &one
		}

		doc := entryDoc{
			ID:                  uuidToStr(uuid.New()),
			ConversationID:      uuidToStr(conversationID),
			ConversationGroupID: conv.ConversationGroupID,
			UserID:              &userID,
			ClientID:            clientID,
			Channel:             ch,
			Epoch:               entryEpoch,
			ContentType:         req.ContentType,
			Content:             s.encrypt(req.Content),
			IndexedContent:      req.IndexedContent,
			Role:                req.Role,
			CreatedAt:           now,
		}
		if _, err := s.entries().InsertOne(ctx, doc); err != nil {
			return nil, fmt.Errorf("failed to append entry: %w", err)
		}
		result[i] = model.Entry{
			ID:                  strToUUID(doc.ID),
			ConversationID:      conversationID,
			ConversationGroupID: strToUUID(conv.ConversationGroupID),
			UserID:              &userID,
			ClientID:            clientID,
			Channel:             model.Channel(ch),
			Epoch:               entryEpoch,
			ContentType:         req.ContentType,
			Content:             req.Content,
			IndexedContent:      req.IndexedContent,
			CreatedAt:           now,
		}
	}

	// Derive conversation title from first history entry if title is empty.
	if len(conv.Title) == 0 {
		for _, e := range result {
			if e.Channel == model.ChannelHistory {
				title := deriveTitleFromContent(string(e.Content))
				if title != "" {
					s.conversations().UpdateByID(ctx, uuidToStr(conversationID), bson.M{"$set": bson.M{"title": s.encrypt([]byte(title)), "updated_at": now}})
				}
				break
			}
		}
	}

	s.conversations().UpdateByID(ctx, uuidToStr(conversationID), bson.M{"$set": bson.M{"updated_at": now}})

	// Warm entries cache if any memory channel entries were appended.
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

func deriveTitleFromContent(content string) string {
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

func (s *MongoStore) SyncAgentEntry(ctx context.Context, userID string, conversationID uuid.UUID, entry registrystore.CreateEntryRequest, clientID string) (*registrystore.SyncResult, error) {
	incomingContent := parseContentArray(entry.Content)

	autoCreated := false
	var conv convDoc
	err := s.conversations().FindOne(ctx, bson.M{
		"_id":        uuidToStr(conversationID),
		"deleted_at": bson.M{"$exists": false},
	}).Decode(&conv)
	if err != nil {
		// Auto-create conversation if it does not exist and content is non-empty.
		if len(incomingContent) == 0 {
			return &registrystore.SyncResult{NoOp: true}, nil
		}
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

	latestEntries, err := s.fetchLatestMemoryEntries(ctx, conv, ancestry, clientID)
	if err != nil {
		return nil, fmt.Errorf("failed to load entries for sync: %w", err)
	}

	existingContent := flattenMemoryContentEntries(s, latestEntries)

	// Compute the current latest epoch value.
	var latestEpoch *int64
	for _, e := range latestEntries {
		if e.Epoch == nil {
			continue
		}
		if latestEpoch == nil || *e.Epoch > *latestEpoch {
			v := *e.Epoch
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
	doc := entryDoc{
		ID:                  uuidToStr(uuid.New()),
		ConversationID:      uuidToStr(conversationID),
		ConversationGroupID: conv.ConversationGroupID,
		UserID:              &userID,
		ClientID:            &clientID,
		Channel:             string(model.ChannelMemory),
		Epoch:               &epochToUse,
		ContentType:         entry.ContentType,
		Content:             s.encrypt(appendContent),
		IndexedContent:      entry.IndexedContent,
		CreatedAt:           now,
	}
	if _, err := s.entries().InsertOne(ctx, doc); err != nil {
		return nil, fmt.Errorf("failed to sync entry: %w", err)
	}
	s.warmEntriesCache(ctx, conv, ancestry, clientID)
	e := s.entryDocToModel(doc)
	e.Content = appendContent
	return &registrystore.SyncResult{Entry: &e, Epoch: &epochToUse, NoOp: false, EpochIncremented: epochIncremented}, nil
}

// autoCreateConversation creates a conversation with a given ID for sync auto-creation.
func (s *MongoStore) autoCreateConversation(ctx context.Context, userID string, conversationID uuid.UUID) (convDoc, error) {
	now := time.Now()
	groupID := uuid.New().String()

	group := groupDoc{
		ID:        groupID,
		CreatedAt: now,
	}
	if _, err := s.groups().InsertOne(ctx, group); err != nil {
		return convDoc{}, fmt.Errorf("failed to create conversation group: %w", err)
	}

	conv := convDoc{
		ID:                  uuidToStr(conversationID),
		ConversationGroupID: groupID,
		OwnerUserID:         userID,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if _, err := s.conversations().InsertOne(ctx, conv); err != nil {
		return convDoc{}, fmt.Errorf("failed to create conversation: %w", err)
	}

	member := memberDoc{
		ConversationGroupID: groupID,
		UserID:              userID,
		AccessLevel:         model.AccessLevelOwner,
		CreatedAt:           now,
	}
	if _, err := s.memberships().InsertOne(ctx, member); err != nil {
		return convDoc{}, fmt.Errorf("failed to create membership: %w", err)
	}

	return conv, nil
}

// --- Indexing ---

func (s *MongoStore) IndexEntries(ctx context.Context, entries []registrystore.IndexEntryRequest) (*registrystore.IndexConversationsResponse, error) {
	count := 0
	for _, req := range entries {
		// Validate that the entry exists and belongs to the specified conversation's group.
		var conv convDoc
		err := s.conversations().FindOne(ctx, bson.M{"_id": uuidToStr(req.ConversationID)}).Decode(&conv)
		if err != nil {
			return nil, &registrystore.NotFoundError{Resource: "entry", ID: req.EntryID.String()}
		}
		result, err := s.entries().UpdateOne(ctx, bson.M{
			"_id":                   uuidToStr(req.EntryID),
			"conversation_group_id": conv.ConversationGroupID,
		}, bson.M{
			"$set": bson.M{"indexed_content": req.IndexedContent},
		})
		if err != nil {
			log.Error("Failed to index entry", "err", err, "entryId", req.EntryID)
			continue
		}
		if result.ModifiedCount == 0 && result.MatchedCount == 0 {
			return nil, &registrystore.NotFoundError{Resource: "entry", ID: req.EntryID.String()}
		}
		count++
	}
	return &registrystore.IndexConversationsResponse{Indexed: count}, nil
}

func (s *MongoStore) ListUnindexedEntries(ctx context.Context, limit int, afterCursor *string) ([]model.Entry, *string, error) {
	filter := bson.M{
		"channel":         string(model.ChannelHistory),
		"indexed_content": bson.M{"$exists": false},
	}
	if afterCursor != nil {
		var cursorEntry entryDoc
		err := s.entries().FindOne(ctx, bson.M{"_id": *afterCursor}).Decode(&cursorEntry)
		if err == nil {
			filter["created_at"] = bson.M{"$gt": cursorEntry.CreatedAt}
		}
	}

	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}).SetLimit(int64(limit + 1))
	cur, err := s.entries().Find(ctx, filter, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list unindexed entries: %w", err)
	}
	var docs []entryDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, nil, fmt.Errorf("failed to decode entries: %w", err)
	}

	hasMore := len(docs) > limit
	if hasMore {
		docs = docs[:limit]
	}

	entries := make([]model.Entry, len(docs))
	for i, d := range docs {
		entries[i] = s.entryDocToModel(d)
		if decrypted, err := s.decrypt(d.Content); err == nil {
			entries[i].Content = decrypted
		}
	}

	var nextCursor *string
	if hasMore && len(entries) > 0 {
		c := entries[len(entries)-1].ID.String()
		nextCursor = &c
	}
	return entries, nextCursor, nil
}

func (s *MongoStore) FindEntriesPendingVectorIndexing(ctx context.Context, limit int) ([]model.Entry, error) {
	filter := bson.M{
		"indexed_content": bson.M{"$exists": true, "$ne": nil},
		"indexed_at":      bson.M{"$exists": false},
	}
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}).SetLimit(int64(limit))
	cur, err := s.entries().Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to find entries pending vector indexing: %w", err)
	}
	var docs []entryDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("failed to decode entries: %w", err)
	}

	entries := make([]model.Entry, len(docs))
	for i, d := range docs {
		entries[i] = s.entryDocToModel(d)
		if decrypted, err := s.decrypt(d.Content); err == nil {
			entries[i].Content = decrypted
		}
	}
	return entries, nil
}

func (s *MongoStore) SetIndexedAt(ctx context.Context, entryID uuid.UUID, conversationGroupID uuid.UUID, indexedAt time.Time) error {
	_, err := s.entries().UpdateOne(ctx,
		bson.M{"_id": uuidToStr(entryID), "conversation_group_id": uuidToStr(conversationGroupID)},
		bson.M{"$set": bson.M{"indexed_at": indexedAt}},
	)
	return err
}

// --- Search ---

func (s *MongoStore) ListConversationGroupIDs(ctx context.Context, userID string) ([]uuid.UUID, error) {
	cur, err := s.memberships().Find(ctx, bson.M{"user_id": userID})
	if err != nil {
		return nil, fmt.Errorf("list group IDs: %w", err)
	}
	var mems []memberDoc
	if err := cur.All(ctx, &mems); err != nil {
		return nil, fmt.Errorf("decode memberships: %w", err)
	}
	seen := make(map[string]bool, len(mems))
	var ids []uuid.UUID
	for _, m := range mems {
		if !seen[m.ConversationGroupID] {
			seen[m.ConversationGroupID] = true
			ids = append(ids, strToUUID(m.ConversationGroupID))
		}
	}
	return ids, nil
}

func (s *MongoStore) FetchSearchResultDetails(ctx context.Context, userID string, entryIDs []uuid.UUID, includeEntry bool) ([]registrystore.SearchResult, error) {
	if len(entryIDs) == 0 {
		return nil, nil
	}
	strIDs := make([]string, len(entryIDs))
	for i, id := range entryIDs {
		strIDs[i] = id.String()
	}
	// Get user's accessible group IDs for access control.
	cur, err := s.memberships().Find(ctx, bson.M{"user_id": userID})
	if err != nil {
		return nil, fmt.Errorf("list memberships: %w", err)
	}
	var mems []memberDoc
	if err := cur.All(ctx, &mems); err != nil {
		return nil, fmt.Errorf("decode memberships: %w", err)
	}
	groupSet := make(map[string]bool, len(mems))
	for _, m := range mems {
		groupSet[m.ConversationGroupID] = true
	}
	entryCur, err := s.entries().Find(ctx, bson.M{"_id": bson.M{"$in": strIDs}})
	if err != nil {
		return nil, fmt.Errorf("fetch entries: %w", err)
	}
	var docs []entryDoc
	if err := entryCur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("decode entries: %w", err)
	}
	results := make([]registrystore.SearchResult, 0, len(docs))
	for _, d := range docs {
		if !groupSet[d.ConversationGroupID] {
			continue
		}
		r := registrystore.SearchResult{
			EntryID:        strToUUID(d.ID),
			ConversationID: strToUUID(d.ConversationID),
		}
		r.ConversationTitle = s.lookupConversationTitle(ctx, d.ConversationID)
		if d.IndexedContent != nil && *d.IndexedContent != "" {
			h := extractHighlight(*d.IndexedContent)
			r.Highlights = &h
		}
		if includeEntry {
			e := s.entryDocToModel(d)
			if decrypted, err := s.decrypt(d.Content); err == nil {
				e.Content = decrypted
			}
			r.Entry = &e
		}
		results = append(results, r)
	}
	return results, nil
}

func (s *MongoStore) SearchEntries(ctx context.Context, userID string, query string, limit int, includeEntry bool) (*registrystore.SearchResults, error) {
	cur, err := s.memberships().Find(ctx, bson.M{"user_id": userID})
	if err != nil {
		return nil, fmt.Errorf("failed to find memberships: %w", err)
	}
	var mems []memberDoc
	if err := cur.All(ctx, &mems); err != nil {
		return nil, fmt.Errorf("failed to decode memberships: %w", err)
	}

	groupIDs := make([]string, len(mems))
	for i, m := range mems {
		groupIDs[i] = m.ConversationGroupID
	}
	if len(groupIDs) == 0 {
		return &registrystore.SearchResults{Data: []registrystore.SearchResult{}}, nil
	}

	filter := bson.M{
		"$text":                 bson.M{"$search": query},
		"conversation_group_id": bson.M{"$in": groupIDs},
	}
	// Fetch limit+1 to detect whether more results exist (for afterCursor).
	// Project and sort by text relevance score.
	opts := options.Find().
		SetProjection(bson.M{"score": bson.M{"$meta": "textScore"}}).
		SetSort(bson.D{{Key: "score", Value: bson.M{"$meta": "textScore"}}}).
		SetLimit(int64(limit + 1))
	searchCur, err := s.entries().Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}
	var docs []entrySearchDoc
	if err := searchCur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("failed to decode search results: %w", err)
	}

	hasMore := len(docs) > limit
	if hasMore {
		docs = docs[:limit]
	}

	results := make([]registrystore.SearchResult, len(docs))
	for i, d := range docs {
		results[i] = registrystore.SearchResult{
			EntryID:        strToUUID(d.ID),
			ConversationID: strToUUID(d.ConversationID),
			Score:          d.TextScore,
			Kind:           "mongo",
		}
		results[i].ConversationTitle = s.lookupConversationTitle(ctx, d.ConversationID)
		if d.IndexedContent != nil && *d.IndexedContent != "" {
			h := extractHighlight(*d.IndexedContent)
			results[i].Highlights = &h
		}
		if includeEntry {
			e := s.entryDocToModel(d.asEntryDoc())
			if decrypted, err := s.decrypt(d.Content); err == nil {
				e.Content = decrypted
			}
			results[i].Entry = &e
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

func (s *MongoStore) AdminListConversations(ctx context.Context, query registrystore.AdminConversationQuery) ([]registrystore.ConversationSummary, *string, error) {
	filter := bson.M{}

	if !query.IncludeDeleted && !query.OnlyDeleted {
		filter["deleted_at"] = bson.M{"$exists": false}
	}
	if query.OnlyDeleted {
		filter["deleted_at"] = bson.M{"$exists": true}
	}
	if query.UserID != nil {
		filter["owner_user_id"] = *query.UserID
	}
	if query.DeletedAfter != nil {
		if existing, ok := filter["deleted_at"]; ok {
			if m, ok := existing.(bson.M); ok {
				m["$gte"] = *query.DeletedAfter
			}
		} else {
			filter["deleted_at"] = bson.M{"$gte": *query.DeletedAfter}
		}
	}
	if query.DeletedBefore != nil {
		if existing, ok := filter["deleted_at"]; ok {
			if m, ok := existing.(bson.M); ok {
				m["$lt"] = *query.DeletedBefore
			}
		} else {
			filter["deleted_at"] = bson.M{"$lt": *query.DeletedBefore}
		}
	}

	switch query.Mode {
	case model.ListModeRoots:
		filter["forked_at_conversation_id"] = bson.M{"$exists": false}
	case model.ListModeLatestFork:
		return s.adminListConversationsLatestFork(ctx, filter, query)
	}

	if query.AfterCursor != nil {
		var cursorDoc convDoc
		err := s.conversations().FindOne(ctx, bson.M{"_id": *query.AfterCursor}).Decode(&cursorDoc)
		if err == nil {
			filter["created_at"] = bson.M{"$gt": cursorDoc.CreatedAt}
		}
	}

	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}).SetLimit(int64(query.Limit + 1))
	cur, err := s.conversations().Find(ctx, filter, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to admin list conversations: %w", err)
	}
	var docs []convDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, nil, fmt.Errorf("failed to decode conversations: %w", err)
	}

	hasMore := len(docs) > query.Limit
	if hasMore {
		docs = docs[:query.Limit]
	}

	summaries := make([]registrystore.ConversationSummary, len(docs))
	for i, d := range docs {
		summaries[i] = registrystore.ConversationSummary{
			ID:                     strToUUID(d.ID),
			Title:                  s.decryptString(d.Title),
			OwnerUserID:            d.OwnerUserID,
			Metadata:               d.Metadata,
			ConversationGroupID:    strToUUID(d.ConversationGroupID),
			ForkedAtConversationID: ptrStrToUUID(d.ForkedAtConversationID),
			ForkedAtEntryID:        ptrStrToUUID(d.ForkedAtEntryID),
			CreatedAt:              d.CreatedAt,
			UpdatedAt:              d.UpdatedAt,
			DeletedAt:              d.DeletedAt,
			AccessLevel:            model.AccessLevelOwner,
		}
	}

	var nextCursor *string
	if hasMore && len(summaries) > 0 {
		c := summaries[len(summaries)-1].ID.String()
		nextCursor = &c
	}
	return summaries, nextCursor, nil
}

func (s *MongoStore) adminListConversationsLatestFork(ctx context.Context, baseFilter bson.M, query registrystore.AdminConversationQuery) ([]registrystore.ConversationSummary, *string, error) {
	opts := options.Find().SetSort(bson.D{{Key: "updated_at", Value: -1}, {Key: "created_at", Value: 1}})
	cur, err := s.conversations().Find(ctx, baseFilter, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to admin list conversations (latest-fork): %w", err)
	}
	var docs []convDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, nil, fmt.Errorf("failed to decode conversations: %w", err)
	}

	seen := map[string]bool{}
	var filtered []convDoc
	for _, d := range docs {
		if seen[d.ConversationGroupID] {
			continue
		}
		seen[d.ConversationGroupID] = true
		filtered = append(filtered, d)
	}

	// Sort by created_at ASC for pagination.
	for i := 0; i < len(filtered); i++ {
		for j := i + 1; j < len(filtered); j++ {
			if filtered[j].CreatedAt.Before(filtered[i].CreatedAt) {
				filtered[i], filtered[j] = filtered[j], filtered[i]
			}
		}
	}

	start := 0
	if query.AfterCursor != nil {
		for i, d := range filtered {
			if d.ID == *query.AfterCursor {
				start = i + 1
				break
			}
		}
	}
	if start > len(filtered) {
		start = len(filtered)
	}
	filtered = filtered[start:]

	hasMore := len(filtered) > query.Limit
	if hasMore {
		filtered = filtered[:query.Limit]
	}

	summaries := make([]registrystore.ConversationSummary, len(filtered))
	for i, d := range filtered {
		summaries[i] = registrystore.ConversationSummary{
			ID:                     strToUUID(d.ID),
			Title:                  s.decryptString(d.Title),
			OwnerUserID:            d.OwnerUserID,
			Metadata:               d.Metadata,
			ConversationGroupID:    strToUUID(d.ConversationGroupID),
			ForkedAtConversationID: ptrStrToUUID(d.ForkedAtConversationID),
			ForkedAtEntryID:        ptrStrToUUID(d.ForkedAtEntryID),
			CreatedAt:              d.CreatedAt,
			UpdatedAt:              d.UpdatedAt,
			DeletedAt:              d.DeletedAt,
			AccessLevel:            model.AccessLevelOwner,
		}
	}

	var nextCursor *string
	if hasMore && len(summaries) > 0 {
		c := summaries[len(summaries)-1].ID.String()
		nextCursor = &c
	}
	return summaries, nextCursor, nil
}

func (s *MongoStore) AdminGetConversation(ctx context.Context, conversationID uuid.UUID) (*registrystore.ConversationDetail, error) {
	var doc convDoc
	err := s.conversations().FindOne(ctx, bson.M{"_id": uuidToStr(conversationID)}).Decode(&doc)
	if err != nil {
		return nil, &registrystore.NotFoundError{Resource: "conversation", ID: conversationID.String()}
	}
	return &registrystore.ConversationDetail{
		ConversationSummary: registrystore.ConversationSummary{
			ID:                     strToUUID(doc.ID),
			Title:                  s.decryptString(doc.Title),
			OwnerUserID:            doc.OwnerUserID,
			Metadata:               doc.Metadata,
			ConversationGroupID:    strToUUID(doc.ConversationGroupID),
			ForkedAtConversationID: ptrStrToUUID(doc.ForkedAtConversationID),
			ForkedAtEntryID:        ptrStrToUUID(doc.ForkedAtEntryID),
			CreatedAt:              doc.CreatedAt,
			UpdatedAt:              doc.UpdatedAt,
			DeletedAt:              doc.DeletedAt,
			AccessLevel:            model.AccessLevelOwner,
		},
	}, nil
}

func (s *MongoStore) AdminDeleteConversation(ctx context.Context, conversationID uuid.UUID) error {
	var doc convDoc
	err := s.conversations().FindOne(ctx, bson.M{"_id": uuidToStr(conversationID)}).Decode(&doc)
	if err != nil {
		return &registrystore.NotFoundError{Resource: "conversation", ID: conversationID.String()}
	}
	now := time.Now()
	s.groups().UpdateByID(ctx, doc.ConversationGroupID, bson.M{"$set": bson.M{"deleted_at": now}})
	s.conversations().UpdateMany(ctx,
		bson.M{"conversation_group_id": doc.ConversationGroupID, "deleted_at": bson.M{"$exists": false}},
		bson.M{"$set": bson.M{"deleted_at": now}},
	)
	return nil
}

func (s *MongoStore) AdminRestoreConversation(ctx context.Context, conversationID uuid.UUID) error {
	var doc convDoc
	err := s.conversations().FindOne(ctx, bson.M{"_id": uuidToStr(conversationID)}).Decode(&doc)
	if err != nil {
		return &registrystore.NotFoundError{Resource: "conversation", ID: conversationID.String()}
	}
	if doc.DeletedAt == nil {
		return &registrystore.ConflictError{Message: "conversation is not deleted"}
	}
	s.groups().UpdateByID(ctx, doc.ConversationGroupID, bson.M{"$unset": bson.M{"deleted_at": ""}})
	s.conversations().UpdateMany(ctx,
		bson.M{"conversation_group_id": doc.ConversationGroupID},
		bson.M{"$unset": bson.M{"deleted_at": ""}},
	)
	return nil
}

func (s *MongoStore) AdminGetEntries(ctx context.Context, conversationID uuid.UUID, query registrystore.AdminMessageQuery) (*registrystore.PagedEntries, error) {
	var conv convDoc
	err := s.conversations().FindOne(ctx, bson.M{"_id": uuidToStr(conversationID)}).Decode(&conv)
	if err != nil {
		return nil, &registrystore.NotFoundError{Resource: "conversation", ID: conversationID.String()}
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}

	cur, err := s.entries().Find(ctx, bson.M{"conversation_group_id": conv.ConversationGroupID}, options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}))
	if err != nil {
		return nil, fmt.Errorf("failed to get admin entries: %w", err)
	}
	var docs []entryDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("failed to decode entries: %w", err)
	}

	var filtered []entryDoc
	if query.AllForks {
		filtered = docs
	} else {
		ancestry, err := s.buildAncestryStack(ctx, conv)
		if err != nil {
			return nil, err
		}
		filtered = filterEntriesByAncestryDocs(docs, ancestry)
	}
	if query.Channel != nil {
		ch := strings.ToLower(string(*query.Channel))
		tmp := filtered[:0]
		for _, entry := range filtered {
			if strings.ToLower(entry.Channel) == ch {
				tmp = append(tmp, entry)
			}
		}
		filtered = tmp
	}

	filtered, nextCursor := paginateEntryDocs(filtered, query.AfterCursor, limit)

	entries := make([]model.Entry, len(filtered))
	for i, d := range filtered {
		entries[i] = s.entryDocToModel(d)
		if decrypted, err := s.decrypt(d.Content); err == nil {
			entries[i].Content = decrypted
		}
	}
	return &registrystore.PagedEntries{Data: entries, AfterCursor: nextCursor}, nil
}

func (s *MongoStore) AdminListMemberships(ctx context.Context, conversationID uuid.UUID, afterCursor *string, limit int) ([]model.ConversationMembership, *string, error) {
	var conv convDoc
	err := s.conversations().FindOne(ctx, bson.M{"_id": uuidToStr(conversationID)}).Decode(&conv)
	if err != nil {
		return nil, nil, &registrystore.NotFoundError{Resource: "conversation", ID: conversationID.String()}
	}

	filter := bson.M{"conversation_group_id": conv.ConversationGroupID}
	if afterCursor != nil {
		var cursorDoc memberDoc
		err := s.memberships().FindOne(ctx, bson.M{
			"conversation_group_id": conv.ConversationGroupID,
			"user_id":               *afterCursor,
		}).Decode(&cursorDoc)
		if err == nil {
			filter["created_at"] = bson.M{"$gt": cursorDoc.CreatedAt}
		}
	}

	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}).SetLimit(int64(limit + 1))
	cur, err := s.memberships().Find(ctx, filter, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to admin list memberships: %w", err)
	}
	var docs []memberDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, nil, fmt.Errorf("failed to decode memberships: %w", err)
	}

	hasMore := len(docs) > limit
	if hasMore {
		docs = docs[:limit]
	}

	result := make([]model.ConversationMembership, len(docs))
	for i, d := range docs {
		result[i] = model.ConversationMembership{
			ConversationGroupID: strToUUID(d.ConversationGroupID),
			UserID:              d.UserID,
			AccessLevel:         d.AccessLevel,
			CreatedAt:           d.CreatedAt,
		}
	}

	var nextCursor *string
	if hasMore && len(result) > 0 {
		c := result[len(result)-1].UserID
		nextCursor = &c
	}
	return result, nextCursor, nil
}

func (s *MongoStore) AdminListForks(ctx context.Context, conversationID uuid.UUID, afterCursor *string, limit int) ([]registrystore.ConversationForkSummary, *string, error) {
	var conv convDoc
	err := s.conversations().FindOne(ctx, bson.M{"_id": uuidToStr(conversationID)}).Decode(&conv)
	if err != nil {
		return nil, nil, &registrystore.NotFoundError{Resource: "conversation", ID: conversationID.String()}
	}

	filter := bson.M{
		"conversation_group_id": conv.ConversationGroupID,
	}
	if afterCursor != nil {
		var cursorDoc convDoc
		err := s.conversations().FindOne(ctx, bson.M{"_id": *afterCursor}).Decode(&cursorDoc)
		if err == nil {
			filter["created_at"] = bson.M{"$gt": cursorDoc.CreatedAt}
		}
	}

	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}).SetLimit(int64(limit + 1))
	cur, err := s.conversations().Find(ctx, filter, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to admin list forks: %w", err)
	}
	var docs []convDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, nil, fmt.Errorf("failed to decode forks: %w", err)
	}

	hasMore := len(docs) > limit
	if hasMore {
		docs = docs[:limit]
	}

	forks := make([]registrystore.ConversationForkSummary, len(docs))
	for i, d := range docs {
		forks[i] = registrystore.ConversationForkSummary{
			ID:                     strToUUID(d.ID),
			Title:                  s.decryptString(d.Title),
			ForkedAtEntryID:        ptrStrToUUID(d.ForkedAtEntryID),
			ForkedAtConversationID: ptrStrToUUID(d.ForkedAtConversationID),
			CreatedAt:              d.CreatedAt,
		}
	}

	var nextCursor *string
	if hasMore && len(forks) > 0 {
		c := forks[len(forks)-1].ID.String()
		nextCursor = &c
	}
	return forks, nextCursor, nil
}

func (s *MongoStore) AdminSearchEntries(ctx context.Context, query registrystore.AdminSearchQuery) (*registrystore.SearchResults, error) {
	filter := bson.M{
		"$text": bson.M{"$search": query.Query},
	}
	if query.UserID != nil {
		// Need to join with conversations to filter by owner — use two-step approach
		cur, err := s.conversations().Find(ctx, bson.M{"owner_user_id": *query.UserID})
		if err != nil {
			return nil, fmt.Errorf("admin search failed: %w", err)
		}
		var convDocs []convDoc
		if err := cur.All(ctx, &convDocs); err != nil {
			return nil, fmt.Errorf("failed to decode conversations: %w", err)
		}
		convIDs := make([]string, len(convDocs))
		for i, c := range convDocs {
			convIDs[i] = c.ID
		}
		filter["conversation_id"] = bson.M{"$in": convIDs}
	}

	opts := options.Find().
		SetProjection(bson.M{"score": bson.M{"$meta": "textScore"}}).
		SetSort(bson.D{{Key: "score", Value: bson.M{"$meta": "textScore"}}}).
		SetLimit(int64(query.Limit))
	cur, err := s.entries().Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("admin search failed: %w", err)
	}
	var docs []entrySearchDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("failed to decode search results: %w", err)
	}

	results := make([]registrystore.SearchResult, len(docs))
	for i, d := range docs {
		results[i] = registrystore.SearchResult{
			EntryID:        strToUUID(d.ID),
			ConversationID: strToUUID(d.ConversationID),
			Score:          d.TextScore,
			Kind:           "mongo",
		}
		results[i].ConversationTitle = s.lookupConversationTitle(ctx, d.ConversationID)
		if d.IndexedContent != nil && *d.IndexedContent != "" {
			h := extractHighlight(*d.IndexedContent)
			results[i].Highlights = &h
		}
		if query.IncludeEntry {
			e := s.entryDocToModel(d.asEntryDoc())
			if decrypted, err := s.decrypt(d.Content); err == nil {
				e.Content = decrypted
			}
			results[i].Entry = &e
		}
	}
	return &registrystore.SearchResults{Data: results}, nil
}

func (s *MongoStore) AdminListAttachments(ctx context.Context, query registrystore.AdminAttachmentQuery) ([]registrystore.AdminAttachment, *string, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}

	filter := bson.M{}
	if query.UserID != nil {
		filter["user_id"] = *query.UserID
	}
	if query.EntryID != nil {
		entryID := uuidToStr(*query.EntryID)
		filter["entry_id"] = entryID
	}

	switch strings.ToLower(strings.TrimSpace(query.Status)) {
	case "", "all":
	case "linked":
		filter["entry_id"] = bson.M{"$exists": true, "$ne": nil}
	case "unlinked":
		filter["entry_id"] = bson.M{"$exists": false}
	case "expired":
		filter["expires_at"] = bson.M{"$exists": true, "$lt": time.Now()}
	default:
		return nil, nil, &registrystore.ValidationError{Field: "status", Message: "invalid status"}
	}

	if query.AfterCursor != nil {
		var cursor attachmentDoc
		if err := s.attachments().FindOne(ctx, bson.M{"_id": *query.AfterCursor}).Decode(&cursor); err == nil {
			filter["created_at"] = bson.M{"$gt": cursor.CreatedAt}
		}
	}

	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}, {Key: "_id", Value: 1}}).SetLimit(int64(limit + 1))
	cur, err := s.attachments().Find(ctx, filter, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("admin list attachments failed: %w", err)
	}
	var docs []attachmentDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, nil, fmt.Errorf("failed to decode attachments: %w", err)
	}

	hasMore := len(docs) > limit
	if hasMore {
		docs = docs[:limit]
	}

	results := make([]registrystore.AdminAttachment, len(docs))
	for i, d := range docs {
		refCount := int64(0)
		if d.StorageKey != nil {
			refCount, _ = s.attachments().CountDocuments(ctx, bson.M{
				"storage_key": d.StorageKey,
				"deleted_at":  bson.M{"$exists": false},
			})
		}
		results[i] = registrystore.AdminAttachment{
			Attachment: s.attachmentDocToModel(d),
			RefCount:   refCount,
		}
	}

	var nextCursor *string
	if hasMore && len(docs) > 0 {
		c := docs[len(docs)-1].ID
		nextCursor = &c
	}
	return results, nextCursor, nil
}

func (s *MongoStore) AdminGetAttachment(ctx context.Context, attachmentID uuid.UUID) (*registrystore.AdminAttachment, error) {
	var doc attachmentDoc
	if err := s.attachments().FindOne(ctx, bson.M{"_id": uuidToStr(attachmentID)}).Decode(&doc); err != nil {
		return nil, &registrystore.NotFoundError{Resource: "attachment", ID: attachmentID.String()}
	}

	refCount := int64(0)
	if doc.StorageKey != nil {
		refCount, _ = s.attachments().CountDocuments(ctx, bson.M{
			"storage_key": doc.StorageKey,
			"deleted_at":  bson.M{"$exists": false},
		})
	}

	return &registrystore.AdminAttachment{
		Attachment: s.attachmentDocToModel(doc),
		RefCount:   refCount,
	}, nil
}

func (s *MongoStore) AdminDeleteAttachment(ctx context.Context, attachmentID uuid.UUID) error {
	result, err := s.attachments().DeleteOne(ctx, bson.M{"_id": uuidToStr(attachmentID)})
	if err != nil {
		return fmt.Errorf("admin delete attachment failed: %w", err)
	}
	if result.DeletedCount == 0 {
		return &registrystore.NotFoundError{Resource: "attachment", ID: attachmentID.String()}
	}
	return nil
}

// --- Attachments ---

func (s *MongoStore) CreateAttachment(ctx context.Context, userID string, conversationID uuid.UUID, attachment model.Attachment) (*model.Attachment, error) {
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

	doc := attachmentDoc{
		ID:          uuidToStr(attachment.ID),
		StorageKey:  attachment.StorageKey,
		Filename:    attachment.Filename,
		ContentType: attachment.ContentType,
		Size:        attachment.Size,
		SHA256:      attachment.SHA256,
		UserID:      attachment.UserID,
		Status:      attachment.Status,
		SourceURL:   attachment.SourceURL,
		ExpiresAt:   attachment.ExpiresAt,
		CreatedAt:   time.Now(),
	}
	if attachment.EntryID != nil {
		s := uuidToStr(*attachment.EntryID)
		doc.EntryID = &s
	}

	if _, err := s.attachments().InsertOne(ctx, doc); err != nil {
		return nil, fmt.Errorf("create attachment failed: %w", err)
	}
	attachment.CreatedAt = doc.CreatedAt
	return &attachment, nil
}

func (s *MongoStore) UpdateAttachment(ctx context.Context, userID string, attachmentID uuid.UUID, update registrystore.AttachmentUpdate) (*model.Attachment, error) {
	id := uuidToStr(attachmentID)
	var current attachmentDoc
	if err := s.attachments().FindOne(ctx, bson.M{"_id": id, "deleted_at": bson.M{"$exists": false}}).Decode(&current); err != nil {
		return nil, &registrystore.NotFoundError{Resource: "attachment", ID: attachmentID.String()}
	}
	if current.UserID != userID {
		return nil, &registrystore.ForbiddenError{}
	}

	set := bson.M{}
	if update.StorageKey != nil {
		set["storage_key"] = *update.StorageKey
	}
	if update.Filename != nil {
		set["filename"] = *update.Filename
	}
	if update.ContentType != nil {
		set["content_type"] = *update.ContentType
	}
	if update.Size != nil {
		set["size"] = *update.Size
	}
	if update.SHA256 != nil {
		set["sha256"] = *update.SHA256
	}
	if update.Status != nil {
		set["status"] = *update.Status
	}
	if update.SourceURL != nil {
		set["source_url"] = *update.SourceURL
	}
	if update.ExpiresAt != nil {
		set["expires_at"] = *update.ExpiresAt
	}
	if update.EntryID != nil {
		set["entry_id"] = uuidToStr(*update.EntryID)
	}

	if len(set) > 0 {
		if _, err := s.attachments().UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": set}); err != nil {
			return nil, fmt.Errorf("update attachment failed: %w", err)
		}
	}

	var updated attachmentDoc
	if err := s.attachments().FindOne(ctx, bson.M{"_id": id, "deleted_at": bson.M{"$exists": false}}).Decode(&updated); err != nil {
		return nil, &registrystore.NotFoundError{Resource: "attachment", ID: attachmentID.String()}
	}
	attachment := s.attachmentDocToModel(updated)
	return &attachment, nil
}

func (s *MongoStore) ListAttachments(ctx context.Context, userID string, conversationID uuid.UUID, afterCursor *string, limit int) ([]model.Attachment, *string, error) {
	filter := bson.M{"deleted_at": bson.M{"$exists": false}}
	if conversationID == uuid.Nil {
		filter["user_id"] = userID
		filter["entry_id"] = bson.M{"$exists": false}
	} else {
		groupID, err := s.getGroupID(ctx, userID, conversationID, model.AccessLevelReader)
		if err != nil {
			return nil, nil, err
		}
		cur, err := s.entries().Find(ctx, bson.M{
			"conversation_id":       uuidToStr(conversationID),
			"conversation_group_id": groupID,
		}, options.Find().SetProjection(bson.M{"_id": 1}))
		if err != nil {
			return nil, nil, fmt.Errorf("list attachments failed: %w", err)
		}
		var entryIDs []entryDoc
		if err := cur.All(ctx, &entryIDs); err != nil {
			return nil, nil, fmt.Errorf("list attachments failed: %w", err)
		}
		ids := make([]string, 0, len(entryIDs))
		for _, d := range entryIDs {
			ids = append(ids, d.ID)
		}
		if len(ids) == 0 {
			return []model.Attachment{}, nil, nil
		}
		filter["entry_id"] = bson.M{"$in": ids}
	}

	if afterCursor != nil {
		var cursorDoc attachmentDoc
		err := s.attachments().FindOne(ctx, bson.M{"_id": *afterCursor}).Decode(&cursorDoc)
		if err == nil {
			filter["created_at"] = bson.M{"$gt": cursorDoc.CreatedAt}
		}
	}

	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}).SetLimit(int64(limit + 1))
	cur, err := s.attachments().Find(ctx, filter, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("list attachments failed: %w", err)
	}
	var docs []attachmentDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, nil, fmt.Errorf("failed to decode attachments: %w", err)
	}

	hasMore := len(docs) > limit
	if hasMore {
		docs = docs[:limit]
	}

	result := make([]model.Attachment, len(docs))
	for i, d := range docs {
		result[i] = s.attachmentDocToModel(d)
	}

	var nextCursor *string
	if hasMore && len(result) > 0 {
		c := result[len(result)-1].ID.String()
		nextCursor = &c
	}
	return result, nextCursor, nil
}

func (s *MongoStore) GetAttachment(ctx context.Context, userID string, conversationID uuid.UUID, attachmentID uuid.UUID) (*model.Attachment, error) {
	var doc attachmentDoc
	err := s.attachments().FindOne(ctx, bson.M{
		"_id":        uuidToStr(attachmentID),
		"deleted_at": bson.M{"$exists": false},
	}).Decode(&doc)
	if err != nil {
		return nil, &registrystore.NotFoundError{Resource: "attachment", ID: attachmentID.String()}
	}

	if doc.EntryID == nil {
		if doc.UserID != userID {
			return nil, &registrystore.ForbiddenError{}
		}
		a := s.attachmentDocToModel(doc)
		return &a, nil
	}

	var entry entryDoc
	if err := s.entries().FindOne(ctx, bson.M{"_id": *doc.EntryID}).Decode(&entry); err != nil {
		// Entry was hard-deleted (conversation deletion). Fall back to ownership check.
		if doc.UserID == userID {
			a := s.attachmentDocToModel(doc)
			return &a, nil
		}
		return nil, &registrystore.NotFoundError{Resource: "attachment", ID: attachmentID.String()}
	}

	if conversationID != uuid.Nil && entry.ConversationID != uuidToStr(conversationID) {
		return nil, &registrystore.NotFoundError{Resource: "attachment", ID: attachmentID.String()}
	}

	if _, err := s.requireAccess(ctx, userID, entry.ConversationGroupID, model.AccessLevelReader); err != nil {
		return nil, err
	}

	a := s.attachmentDocToModel(doc)
	return &a, nil
}

func (s *MongoStore) DeleteAttachment(ctx context.Context, userID string, conversationID uuid.UUID, attachmentID uuid.UUID) error {
	attachment, err := s.GetAttachment(ctx, userID, conversationID, attachmentID)
	if err != nil {
		return err
	}
	if attachment.UserID != userID {
		return &registrystore.ForbiddenError{}
	}
	if attachment.EntryID != nil {
		return &registrystore.ConflictError{Message: "linked attachments cannot be deleted"}
	}

	result, err := s.attachments().DeleteOne(ctx, bson.M{"_id": uuidToStr(attachmentID)})
	if err != nil {
		return fmt.Errorf("delete attachment failed: %w", err)
	}
	if result.DeletedCount == 0 {
		return &registrystore.NotFoundError{Resource: "attachment", ID: attachmentID.String()}
	}
	return nil
}

// --- Eviction ---

func (s *MongoStore) FindEvictableGroupIDs(ctx context.Context, cutoff time.Time, limit int) ([]uuid.UUID, error) {
	opts := options.Find().SetLimit(int64(limit)).SetProjection(bson.M{"_id": 1})
	cur, err := s.groups().Find(ctx, bson.M{
		"deleted_at": bson.M{"$exists": true, "$lt": cutoff},
	}, opts)
	if err != nil {
		return nil, err
	}
	var docs []groupDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}

	ids := make([]uuid.UUID, len(docs))
	for i, d := range docs {
		ids[i] = strToUUID(d.ID)
	}
	return ids, nil
}

func (s *MongoStore) CountEvictableGroups(ctx context.Context, cutoff time.Time) (int64, error) {
	return s.groups().CountDocuments(ctx, bson.M{
		"deleted_at": bson.M{"$exists": true, "$lt": cutoff},
	})
}

func (s *MongoStore) HardDeleteConversationGroups(ctx context.Context, groupIDs []uuid.UUID) error {
	strIDs := make([]string, len(groupIDs))
	for i, id := range groupIDs {
		strIDs[i] = uuidToStr(id)
	}

	filter := bson.M{"conversation_group_id": bson.M{"$in": strIDs}}

	// Delete in order: entries → conversations → memberships → transfers → groups
	s.entries().DeleteMany(ctx, filter)
	s.conversations().DeleteMany(ctx, filter)
	s.memberships().DeleteMany(ctx, filter)
	s.transfers().DeleteMany(ctx, filter)
	s.groups().DeleteMany(ctx, bson.M{"_id": bson.M{"$in": strIDs}})
	return nil
}

func (s *MongoStore) CreateTask(ctx context.Context, taskType string, taskBody map[string]any) error {
	var taskName *string
	if rawName, ok := taskBody["taskName"]; ok {
		if name, ok := rawName.(string); ok {
			trimmed := strings.TrimSpace(name)
			if trimmed != "" {
				taskName = &trimmed
			}
		}
	}

	doc := bson.M{
		"_id":           uuidToStr(uuid.New()),
		"task_name":     taskName,
		"task_type":     taskType,
		"task_body":     taskBody,
		"created_at":    time.Now(),
		"retry_at":      time.Now(),
		"processing_at": nil,
		"retry_count":   0,
	}
	if taskName != nil {
		res, err := s.db.Collection("tasks").UpdateOne(
			ctx,
			bson.M{"task_name": *taskName},
			bson.M{"$setOnInsert": doc},
			options.UpdateOne().SetUpsert(true),
		)
		if err != nil {
			return err
		}
		if res.MatchedCount > 0 {
			return nil
		}
		return nil
	}
	_, err := s.db.Collection("tasks").InsertOne(ctx, doc)
	return err
}

func (s *MongoStore) ClaimReadyTasks(ctx context.Context, limit int) ([]model.Task, error) {
	var tasks []model.Task
	now := time.Now()
	staleClaimCutoff := now.Add(-5 * time.Minute)

	for i := 0; i < limit; i++ {
		filter := bson.M{
			"retry_at": bson.M{"$lte": now},
			"$or": []bson.M{
				{"processing_at": bson.M{"$exists": false}},
				{"processing_at": nil},
				{"processing_at": bson.M{"$lt": staleClaimCutoff}},
			},
		}
		update := bson.M{
			"$set": bson.M{
				"processing_at": now,
				"retry_at":      now.Add(5 * time.Minute),
			},
		}
		opts := options.FindOneAndUpdate().
			SetSort(bson.D{{Key: "retry_at", Value: 1}, {Key: "created_at", Value: 1}}).
			SetReturnDocument(options.After)

		var doc bson.M
		err := s.db.Collection("tasks").FindOneAndUpdate(ctx, filter, update, opts).Decode(&doc)
		if err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				break
			}
			return nil, fmt.Errorf("claim ready tasks: %w", err)
		}

		id, _ := doc["_id"].(string)
		taskType, _ := doc["task_type"].(string)
		task := model.Task{
			ID:       strToUUID(id),
			TaskType: taskType,
		}
		switch tb := doc["task_body"].(type) {
		case bson.M:
			task.TaskBody = map[string]any(tb)
		case map[string]any:
			task.TaskBody = tb
		default:
			task.TaskBody = map[string]any{}
		}
		switch rc := doc["retry_count"].(type) {
		case int32:
			task.RetryCount = int(rc)
		case int64:
			task.RetryCount = int(rc)
		case int:
			task.RetryCount = rc
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func (s *MongoStore) DeleteTask(ctx context.Context, taskID uuid.UUID) error {
	_, err := s.db.Collection("tasks").DeleteOne(ctx, bson.M{"_id": uuidToStr(taskID)})
	return err
}

func (s *MongoStore) FailTask(ctx context.Context, taskID uuid.UUID, errMsg string, retryDelay time.Duration) error {
	_, err := s.db.Collection("tasks").UpdateByID(ctx, uuidToStr(taskID), bson.M{
		"$inc": bson.M{"retry_count": 1},
		"$set": bson.M{
			"retry_at":      time.Now().Add(retryDelay),
			"last_error":    errMsg,
			"processing_at": nil,
		},
	})
	return err
}

func (s *MongoStore) AdminGetAttachmentByStorageKey(ctx context.Context, storageKey string) (*registrystore.AdminAttachment, error) {
	var doc attachmentDoc
	if err := s.attachments().FindOne(ctx, bson.M{
		"storage_key": storageKey,
		"deleted_at":  bson.M{"$exists": false},
	}).Decode(&doc); err != nil {
		return nil, &registrystore.NotFoundError{Resource: "attachment", ID: storageKey}
	}

	refCount, _ := s.attachments().CountDocuments(ctx, bson.M{
		"storage_key": storageKey,
		"deleted_at":  bson.M{"$exists": false},
	})

	return &registrystore.AdminAttachment{
		Attachment: s.attachmentDocToModel(doc),
		RefCount:   refCount,
	}, nil
}

type forkAncestorDoc struct {
	ConversationID string
	StopAtEntryID  *string
}

func (s *MongoStore) buildAncestryStack(ctx context.Context, target convDoc) ([]forkAncestorDoc, error) {
	cur, err := s.conversations().Find(ctx, bson.M{"conversation_group_id": target.ConversationGroupID})
	if err != nil {
		return nil, fmt.Errorf("failed to load fork ancestry: %w", err)
	}
	var conversations []convDoc
	if err := cur.All(ctx, &conversations); err != nil {
		return nil, fmt.Errorf("failed to decode fork ancestry: %w", err)
	}

	byID := make(map[string]convDoc, len(conversations))
	for _, conv := range conversations {
		byID[conv.ID] = conv
	}

	stack := make([]forkAncestorDoc, 0, len(conversations))
	current := target
	var stopAt *string
	for {
		stack = append(stack, forkAncestorDoc{
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

func filterEntriesByAncestryDocs(allEntries []entryDoc, ancestry []forkAncestorDoc) []entryDoc {
	if len(ancestry) == 0 {
		return allEntries
	}

	result := make([]entryDoc, 0, len(allEntries))
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

func filterEntriesForAllForksDocs(entries []entryDoc, channel model.Channel, clientID *string, epochFilter *registrystore.MemoryEpochFilter) []entryDoc {
	if channel == "" {
		return entries
	}

	filtered := make([]entryDoc, 0, len(entries))
	for _, entry := range entries {
		if !strings.EqualFold(entry.Channel, string(channel)) {
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
		result := make([]entryDoc, 0, len(filtered))
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
		result := make([]entryDoc, 0, len(filtered))
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

func filterMemoryEntriesWithEpochDocs(allEntries []entryDoc, ancestry []forkAncestorDoc, clientID string, epochFilter *registrystore.MemoryEpochFilter) []entryDoc {
	epoch := normalizeEpochFilter(epochFilter)
	result := make([]entryDoc, 0, len(allEntries))
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

		if strings.EqualFold(entry.Channel, string(model.ChannelMemory)) && entry.ClientID != nil && *entry.ClientID == clientID {
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

// loadEntriesForGroup fetches all entries for a conversation group from MongoDB.
func (s *MongoStore) loadEntriesForGroup(ctx context.Context, groupID string) ([]entryDoc, error) {
	cur, err := s.entries().Find(ctx, bson.M{"conversation_group_id": groupID}, options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}))
	if err != nil {
		return nil, fmt.Errorf("failed to list entries: %w", err)
	}
	var docs []entryDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("failed to decode entries: %w", err)
	}
	return docs, nil
}

// fetchLatestMemoryEntries returns the latest-epoch memory entries for the given
// conversation and clientID, using MemoryEntriesCache as a read-through layer.
func (s *MongoStore) fetchLatestMemoryEntries(ctx context.Context, conv convDoc, ancestry []forkAncestorDoc, clientID string) ([]model.Entry, error) {
	convID := strToUUID(conv.ID)
	if s.entriesCache != nil && s.entriesCache.Available() {
		cached, err := s.entriesCache.Get(ctx, convID, clientID)
		if err == nil && cached != nil {
			if security.CacheHitsTotal != nil {
				security.CacheHitsTotal.Inc()
			}
			return cached.Entries, nil
		}
	}

	docs, err := s.loadEntriesForGroup(ctx, conv.ConversationGroupID)
	if err != nil {
		return nil, err
	}
	latestFilter := &registrystore.MemoryEpochFilter{Mode: registrystore.MemoryEpochModeLatest}
	filteredDocs := filterMemoryEntriesWithEpochDocs(docs, ancestry, clientID, latestFilter)

	entries := make([]model.Entry, len(filteredDocs))
	for i, d := range filteredDocs {
		entries[i] = s.entryDocToModel(d)
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
			if serr := s.entriesCache.Set(ctx, convID, clientID, registrycache.CachedMemoryEntries{Entries: entries, Epoch: epoch}, 0); serr != nil {
				log.Warn("entries cache set error", "err", serr)
			}
		}
	}
	return entries, nil
}

// warmEntriesCache re-fetches the latest memory entries from the DB and updates the cache.
// Called after a successful SyncAgentEntry write to keep the cache warm.
func (s *MongoStore) warmEntriesCache(ctx context.Context, conv convDoc, ancestry []forkAncestorDoc, clientID string) {
	if s.entriesCache == nil || !s.entriesCache.Available() {
		return
	}
	convID := strToUUID(conv.ID)
	docs, err := s.loadEntriesForGroup(ctx, conv.ConversationGroupID)
	if err != nil {
		log.Warn("warmEntriesCache: failed to list entries", "err", err)
		return
	}
	latestFilter := &registrystore.MemoryEpochFilter{Mode: registrystore.MemoryEpochModeLatest}
	filteredDocs := filterMemoryEntriesWithEpochDocs(docs, ancestry, clientID, latestFilter)
	entries := make([]model.Entry, len(filteredDocs))
	for i, d := range filteredDocs {
		entries[i] = s.entryDocToModel(d)
	}
	if len(entries) == 0 {
		if rerr := s.entriesCache.Remove(ctx, convID, clientID); rerr != nil {
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
	if serr := s.entriesCache.Set(ctx, convID, clientID, registrycache.CachedMemoryEntries{Entries: entries, Epoch: epoch}, 0); serr != nil {
		log.Warn("warmEntriesCache: cache set error", "err", serr)
	}
}

// flattenMemoryContentEntries flattens model.Entry content for comparison in SyncAgentEntry.
func flattenMemoryContentEntries(s *MongoStore, entries []model.Entry) []any {
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

// paginateEntriesModel applies cursor-based pagination to a []model.Entry slice.
func paginateEntriesModel(entries []model.Entry, afterEntryID *string, limit int) ([]model.Entry, *string) {
	start := 0
	if afterEntryID != nil {
		for i, e := range entries {
			if e.ID.String() == *afterEntryID {
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
	if err := json.Unmarshal(raw, &obj); err == nil {
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

func paginateEntryDocs(entries []entryDoc, afterEntryID *string, limit int) ([]entryDoc, *string) {
	start := 0
	if afterEntryID != nil {
		for i, entry := range entries {
			if entry.ID == *afterEntryID {
				start = i + 1
				break
			}
		}
	}
	if start >= len(entries) {
		return []entryDoc{}, nil
	}
	end := start + limit
	if end > len(entries) {
		end = len(entries)
	}
	page := entries[start:end]
	var cursor *string
	if end < len(entries) && len(page) > 0 {
		c := page[len(page)-1].ID
		cursor = &c
	}
	return page, cursor
}

// lookupConversationTitle fetches and decrypts a conversation's title by ID.
func (s *MongoStore) lookupConversationTitle(ctx context.Context, conversationID string) *string {
	var doc convDoc
	err := s.conversations().FindOne(ctx, bson.M{"_id": conversationID}).Decode(&doc)
	if err != nil || len(doc.Title) == 0 {
		return nil
	}
	title := s.decryptString(doc.Title)
	return &title
}

// extractHighlight returns a truncated preview of indexed content (max 200 chars).
func extractHighlight(text string) string {
	const maxLength = 200
	if len(text) <= maxLength {
		return text
	}
	return text[:maxLength] + "..."
}

// --- Conversion helpers ---

func (s *MongoStore) entryDocToModel(d entryDoc) model.Entry {
	return model.Entry{
		ID:                  strToUUID(d.ID),
		ConversationID:      strToUUID(d.ConversationID),
		ConversationGroupID: strToUUID(d.ConversationGroupID),
		UserID:              d.UserID,
		ClientID:            d.ClientID,
		Channel:             model.Channel(d.Channel),
		Epoch:               d.Epoch,
		ContentType:         d.ContentType,
		Content:             d.Content,
		IndexedContent:      d.IndexedContent,
		IndexedAt:           d.IndexedAt,
		CreatedAt:           d.CreatedAt,
	}
}

func (s *MongoStore) attachmentDocToModel(d attachmentDoc) model.Attachment {
	a := model.Attachment{
		ID:          strToUUID(d.ID),
		StorageKey:  d.StorageKey,
		Filename:    d.Filename,
		ContentType: d.ContentType,
		Size:        d.Size,
		SHA256:      d.SHA256,
		UserID:      d.UserID,
		Status:      d.Status,
		SourceURL:   d.SourceURL,
		ExpiresAt:   d.ExpiresAt,
		CreatedAt:   d.CreatedAt,
		DeletedAt:   d.DeletedAt,
	}
	a.EntryID = ptrStrToUUID(d.EntryID)
	return a
}
