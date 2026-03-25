//go:build !nomongo

package mongo

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var errMongoOutboxReplayUnsupported = fmt.Errorf("%w: mongo outbox replay requires session transactions and change-stream relay support", registrystore.ErrOutboxReplayUnsupported)

type outboxDoc struct {
	ID        bson.ObjectID `bson:"_id,omitempty"`
	Event     string        `bson:"event"`
	Kind      string        `bson:"kind"`
	Data      any           `bson:"data"`
	CreatedAt time.Time     `bson:"created_at"`
}

func (s *MongoStore) AppendOutboxEvents(ctx context.Context, events []registrystore.OutboxWrite) ([]registrystore.OutboxEvent, error) {
	if len(events) == 0 {
		return nil, nil
	}

	now := time.Now().UTC()
	docs := make([]any, 0, len(events))
	for i := range events {
		createdAt := events[i].CreatedAt.UTC()
		if createdAt.IsZero() {
			createdAt = now
		}
		var data any
		if err := json.Unmarshal(events[i].Data, &data); err != nil {
			return nil, fmt.Errorf("mongo outbox append decode failed: %w", err)
		}
		docs = append(docs, outboxDoc{
			Event:     events[i].Event,
			Kind:      events[i].Kind,
			Data:      data,
			CreatedAt: createdAt,
		})
	}

	res, err := s.outboxEvents().InsertMany(ctx, docs)
	if err != nil {
		return nil, fmt.Errorf("mongo outbox append failed: %w", err)
	}

	out := make([]registrystore.OutboxEvent, 0, len(events))
	for i, inserted := range res.InsertedIDs {
		id, ok := inserted.(bson.ObjectID)
		if !ok {
			return nil, fmt.Errorf("mongo outbox append returned non-objectid cursor %T", inserted)
		}
		createdAt := events[i].CreatedAt.UTC()
		if createdAt.IsZero() {
			createdAt = now
		}
		out = append(out, registrystore.OutboxEvent{
			Cursor:    id.Hex(),
			Event:     events[i].Event,
			Kind:      events[i].Kind,
			Data:      append(json.RawMessage(nil), events[i].Data...),
			CreatedAt: createdAt,
		})
	}
	return out, nil
}

func (s *MongoStore) ListOutboxEvents(ctx context.Context, query registrystore.OutboxQuery) (*registrystore.OutboxPage, error) {
	if cursor := strings.TrimSpace(query.AfterCursor); cursor != "" && !strings.EqualFold(cursor, "start") {
		return nil, errMongoOutboxReplayUnsupported
	}
	return nil, errMongoOutboxReplayUnsupported
}

func (s *MongoStore) EvictOutboxEventsBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit <= 0 {
		limit = 1000
	}

	filter := bson.M{"created_at": bson.M{"$lt": before.UTC()}}
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}, {Key: "_id", Value: 1}}).SetLimit(int64(limit))
	cursor, err := s.outboxEvents().Find(ctx, filter, opts)
	if err != nil {
		return 0, fmt.Errorf("mongo outbox eviction query failed: %w", err)
	}
	defer cursor.Close(ctx)

	var docs []outboxDoc
	if err := cursor.All(ctx, &docs); err != nil {
		return 0, fmt.Errorf("mongo outbox eviction decode failed: %w", err)
	}
	if len(docs) == 0 {
		return 0, nil
	}

	ids := make([]bson.ObjectID, 0, len(docs))
	for i := range docs {
		ids = append(ids, docs[i].ID)
	}
	res, err := s.outboxEvents().DeleteMany(ctx, bson.M{"_id": bson.M{"$in": ids}})
	if err != nil {
		return 0, fmt.Errorf("mongo outbox eviction delete failed: %w", err)
	}
	return res.DeletedCount, nil
}

var _ registrystore.EventOutboxStore = (*MongoStore)(nil)
