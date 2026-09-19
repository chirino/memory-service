//go:build !nomongo

package mongo

import (
	"context"
	"fmt"

	"github.com/chirino/memory-service/internal/model"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func (s *MongoStore) AdminListEventSnapshotConversations(ctx context.Context, after *registrystore.EventSnapshotConversationCursor, limit int) ([]registrystore.ConversationSummary, *registrystore.EventSnapshotConversationCursor, error) {
	if limit <= 0 {
		limit = 100
	}
	filter := bson.M{}
	if after != nil {
		filter["$or"] = bson.A{bson.M{"created_at": bson.M{"$gt": after.CreatedAt}}, bson.M{"created_at": after.CreatedAt, "_id": bson.M{"$gt": after.ID}}}
	}
	cur, err := s.conversations().Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}, {Key: "_id", Value: 1}}).SetLimit(int64(limit+1)))
	if err != nil {
		return nil, nil, fmt.Errorf("list event snapshot conversations: %w", err)
	}
	defer cur.Close(ctx)
	var docs []convDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, nil, err
	}
	hasMore := len(docs) > limit
	if hasMore {
		docs = docs[:limit]
	}
	if err := s.hydrateEventSnapshotConversationForks(ctx, docs); err != nil {
		return nil, nil, err
	}
	rows := make([]registrystore.ConversationSummary, 0, len(docs))
	for i := range docs {
		row, err := s.eventSnapshotConversationSummary(&docs[i])
		if err != nil {
			return nil, nil, err
		}
		rows = append(rows, row)
	}
	if !hasMore || len(docs) == 0 {
		return rows, nil, nil
	}
	last := docs[len(docs)-1]
	return rows, &registrystore.EventSnapshotConversationCursor{CreatedAt: last.CreatedAt, ID: last.ID}, nil
}

func (s *MongoStore) hydrateEventSnapshotConversationForks(ctx context.Context, docs []convDoc) error {
	if len(docs) == 0 {
		return nil
	}
	conversationIDs := make([]string, 0, len(docs))
	groupIDs := make([]string, 0, len(docs))
	for i := range docs {
		conversationIDs = append(conversationIDs, docs[i].ID)
		groupIDs = append(groupIDs, docs[i].ConversationGroupID)
	}
	ancestryCursor, err := s.conversationAncestry().Find(ctx, bson.M{"_id": bson.M{"$in": conversationIDs}})
	if err != nil {
		return fmt.Errorf("load event snapshot conversation ancestry: %w", err)
	}
	defer ancestryCursor.Close(ctx)
	var ancestry []conversationAncestryDoc
	if err := ancestryCursor.All(ctx, &ancestry); err != nil {
		return fmt.Errorf("decode event snapshot conversation ancestry: %w", err)
	}
	directByConversation := make(map[string]conversationAncestryDoc, len(ancestry))
	for _, direct := range ancestry {
		directByConversation[direct.ID] = direct
	}
	lineageCursor, err := s.conversations().Find(ctx, bson.M{
		"conversation_group_id":      bson.M{"$in": groupIDs},
		"started_by_conversation_id": bson.M{"$exists": true},
	}, options.Find().SetProjection(bson.M{
		"conversation_group_id":      1,
		"started_by_conversation_id": 1,
		"started_by_entry_id":        1,
	}))
	if err != nil {
		return fmt.Errorf("load event snapshot conversation lineage: %w", err)
	}
	defer lineageCursor.Close(ctx)
	var lineage []convDoc
	if err := lineageCursor.All(ctx, &lineage); err != nil {
		return fmt.Errorf("decode event snapshot conversation lineage: %w", err)
	}
	lineageByGroup := make(map[string]convDoc, len(lineage))
	for _, item := range lineage {
		lineageByGroup[item.ConversationGroupID] = item
	}
	for i := range docs {
		if direct, ok := directByConversation[docs[i].ID]; ok {
			docs[i].ForkedAtConversationID = ptrStrToConversationID(direct.ParentConversationID)
			docs[i].ForkedAtEntryID = ptrStrToUUID(direct.ForkedAtEntryID)
		}
		if item, ok := lineageByGroup[docs[i].ConversationGroupID]; ok {
			docs[i].StartedByConversationID = item.StartedByConversationID
			docs[i].StartedByEntryID = item.StartedByEntryID
		}
	}
	return nil
}

func (s *MongoStore) eventSnapshotConversationSummary(doc *convDoc) (registrystore.ConversationSummary, error) {
	title, err := s.decryptConversationTitle(doc.ID, doc.Title)
	if err != nil {
		return registrystore.ConversationSummary{}, err
	}
	var startedByEntryID *uuid.UUID
	if doc.StartedByEntryID != nil {
		id := strToUUID(*doc.StartedByEntryID)
		startedByEntryID = &id
	}
	return registrystore.ConversationSummary{ID: doc.ID, Title: title, OwnerUserID: doc.OwnerUserID, ClientID: doc.ClientID, AgentID: doc.AgentID, Metadata: doc.Metadata, ConversationGroupID: strToUUID(doc.ConversationGroupID), ForkedAtConversationID: doc.ForkedAtConversationID, ForkedAtEntryID: doc.ForkedAtEntryID, StartedByConversationID: doc.StartedByConversationID, StartedByEntryID: startedByEntryID, CreatedAt: doc.CreatedAt, UpdatedAt: doc.UpdatedAt, ArchivedAt: doc.ArchivedAt, AccessLevel: model.AccessLevelOwner}, nil
}

func (s *MongoStore) AdminListEventSnapshotEntries(ctx context.Context, after *registrystore.EventSnapshotEntryCursor, limit int) ([]model.Entry, *registrystore.EventSnapshotEntryCursor, error) {
	if limit <= 0 {
		limit = 100
	}
	filter := bson.M{}
	if after != nil {
		filter["$or"] = bson.A{
			bson.M{"created_at": bson.M{"$gt": after.CreatedAt}},
			bson.M{"created_at": after.CreatedAt, "_id": bson.M{"$gt": after.ID}},
		}
	}
	cur, err := s.entries().Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}, {Key: "_id", Value: 1}}).SetLimit(int64(limit+1)))
	if err != nil {
		return nil, nil, fmt.Errorf("list event snapshot entries: %w", err)
	}
	defer cur.Close(ctx)
	var docs []entryDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, nil, fmt.Errorf("decode event snapshot entries: %w", err)
	}
	hasMore := len(docs) > limit
	if hasMore {
		docs = docs[:limit]
	}
	rows := make([]model.Entry, 0, len(docs))
	for _, doc := range docs {
		row, err := s.entryDocToModelWithContent(doc)
		if err != nil {
			return nil, nil, err
		}
		rows = append(rows, row)
	}
	if !hasMore || len(rows) == 0 {
		return rows, nil, nil
	}
	last := rows[len(rows)-1]
	return rows, &registrystore.EventSnapshotEntryCursor{CreatedAt: last.CreatedAt, ID: last.ID.String()}, nil
}
