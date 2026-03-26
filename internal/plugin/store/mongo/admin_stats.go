//go:build !nomongo

package mongo

import (
	"context"
	"fmt"
	"time"

	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func (s *MongoStore) AdminStatsSummary(ctx context.Context) (*registrystore.AdminStatsSummary, error) {
	conversationGroupsTotal, err := s.groups().CountDocuments(ctx, bson.M{"archived_at": bson.M{"$exists": false}})
	if err != nil {
		return nil, fmt.Errorf("count active conversation groups: %w", err)
	}
	conversationGroupsArchived, err := s.groups().CountDocuments(ctx, bson.M{"archived_at": bson.M{"$exists": true}})
	if err != nil {
		return nil, fmt.Errorf("count archived conversation groups: %w", err)
	}
	findOldestArchivedConversation := s.conversations().FindOne(ctx, bson.M{"archived_at": bson.M{"$exists": true}}, options.FindOne().SetSort(bson.D{{Key: "updated_at", Value: 1}, {Key: "_id", Value: 1}}))
	var oldestArchivedConversation struct {
		UpdatedAt time.Time `bson:"updated_at"`
	}
	var oldestArchivedAt *time.Time
	if err := findOldestArchivedConversation.Decode(&oldestArchivedConversation); err == nil {
		t := oldestArchivedConversation.UpdatedAt.UTC()
		oldestArchivedAt = &t
	} else if err != nil && err != mongo.ErrNoDocuments {
		return nil, fmt.Errorf("load oldest archived conversation: %w", err)
	}
	conversationsTotal, err := s.conversations().CountDocuments(ctx, bson.M{"archived_at": bson.M{"$exists": false}})
	if err != nil {
		return nil, fmt.Errorf("count active conversations: %w", err)
	}
	entriesTotal, err := s.entries().CountDocuments(ctx, bson.M{})
	if err != nil {
		return nil, fmt.Errorf("count entries: %w", err)
	}
	outboxEventsTotal, err := s.outboxEvents().CountDocuments(ctx, bson.M{})
	if err != nil {
		return nil, fmt.Errorf("count outbox events: %w", err)
	}

	var oldest struct {
		CreatedAt time.Time `bson:"created_at"`
	}
	findOldest := s.outboxEvents().FindOne(ctx, bson.M{}, options.FindOne().SetSort(bson.D{{Key: "created_at", Value: 1}, {Key: "_id", Value: 1}}))
	var oldestAt *time.Time
	if err := findOldest.Decode(&oldest); err == nil {
		t := oldest.CreatedAt.UTC()
		oldestAt = &t
	} else if err != nil && err != mongo.ErrNoDocuments {
		return nil, fmt.Errorf("load oldest outbox event: %w", err)
	}

	return &registrystore.AdminStatsSummary{
		ConversationGroups: registrystore.AdminConversationGroupStats{
			Total:            conversationGroupsTotal,
			Archived:         conversationGroupsArchived,
			OldestArchivedAt: oldestArchivedAt,
		},
		Conversations: registrystore.AdminTotalStats{
			Total: conversationsTotal,
		},
		Entries: registrystore.AdminTotalStats{
			Total: entriesTotal,
		},
		OutboxEvents: &registrystore.AdminOutboxStats{
			Total:    outboxEventsTotal,
			OldestAt: oldestAt,
		},
	}, nil
}

var _ registrystore.AdminStatsSummaryProvider = (*MongoStore)(nil)
