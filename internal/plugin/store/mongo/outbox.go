//go:build !nomongo

package mongo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type outboxDoc struct {
	ID          bson.ObjectID `bson:"_id,omitempty"`
	Event       string        `bson:"event"`
	Kind        string        `bson:"kind"`
	Data        any           `bson:"data"`
	CreatedAt   time.Time     `bson:"created_at"`
	EventSeq    *int64        `bson:"event_seq,omitempty"`
	ResumeToken bson.Raw      `bson:"resume_token,omitempty"`
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
		if _, ok := inserted.(bson.ObjectID); !ok {
			return nil, fmt.Errorf("mongo outbox append returned non-objectid cursor %T", inserted)
		}
		createdAt := events[i].CreatedAt.UTC()
		if createdAt.IsZero() {
			createdAt = now
		}
		out = append(out, registrystore.OutboxEvent{
			Cursor:    "",
			Event:     events[i].Event,
			Kind:      events[i].Kind,
			Data:      append(json.RawMessage(nil), events[i].Data...),
			CreatedAt: createdAt,
		})
	}
	return out, nil
}

func (s *MongoStore) ListOutboxEvents(ctx context.Context, query registrystore.OutboxQuery) (*registrystore.OutboxPage, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}
	filter := bson.M{"event_seq": bson.M{"$exists": true}}
	cursor := strings.TrimSpace(query.AfterCursor)
	if cursor != "" && !strings.EqualFold(cursor, "start") {
		token, err := parseMongoOutboxCursor(cursor)
		if err != nil {
			return nil, &registrystore.BadRequestError{Message: "invalid outbox cursor"}
		}
		var anchor outboxDoc
		if err := s.outboxEvents().FindOne(ctx, bson.M{"resume_token": token}).Decode(&anchor); err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				return nil, registrystore.ErrStaleOutboxCursor
			}
			return nil, fmt.Errorf("resolve MongoDB outbox cursor: %w", err)
		}
		if anchor.EventSeq == nil {
			return nil, registrystore.ErrStaleOutboxCursor
		}
		filter["event_seq"] = bson.M{"$gt": *anchor.EventSeq}
	}
	if len(query.Kinds) > 0 {
		filter["kind"] = bson.M{"$in": query.Kinds}
	}
	cur, err := s.outboxEvents().Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "event_seq", Value: 1}}).SetLimit(int64(limit+1)))
	if err != nil {
		return nil, fmt.Errorf("mongo outbox replay query failed: %w", err)
	}
	defer cur.Close(ctx)
	var docs []outboxDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	hasMore := len(docs) > limit
	if hasMore {
		docs = docs[:limit]
	}
	events := make([]registrystore.OutboxEvent, 0, len(docs))
	for _, doc := range docs {
		raw, err := json.Marshal(doc.Data)
		if err != nil {
			return nil, err
		}
		events = append(events, registrystore.OutboxEvent{Cursor: formatMongoOutboxCursor(doc.ResumeToken), Event: doc.Event, Kind: doc.Kind, Data: raw, CreatedAt: doc.CreatedAt.UTC()})
	}
	return &registrystore.OutboxPage{Events: events, HasMore: hasMore}, nil
}

func (s *MongoStore) CurrentOutboxCursor(ctx context.Context) (string, error) {
	var doc outboxDoc
	err := s.outboxEvents().FindOne(ctx, bson.M{"event_seq": bson.M{"$exists": true}}, options.FindOne().SetSort(bson.D{{Key: "event_seq", Value: -1}})).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return "start", nil
		}
		return "", err
	}
	return formatMongoOutboxCursor(doc.ResumeToken), nil
}

func formatMongoOutboxCursor(token bson.Raw) string {
	return "mongo:" + base64.RawURLEncoding.EncodeToString(token)
}

func parseMongoOutboxCursor(cursor string) (bson.Raw, error) {
	raw, ok := strings.CutPrefix(strings.TrimSpace(cursor), "mongo:")
	if !ok {
		return nil, fmt.Errorf("invalid mongo outbox cursor")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) == 0 {
		return nil, fmt.Errorf("invalid mongo outbox cursor")
	}
	return bson.Raw(decoded), nil
}

func (s *MongoStore) EvictOutboxEventsBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit <= 0 {
		limit = 1000
	}
	var state mongoOutboxRelayState
	if err := s.db.Collection("outbox_relay_state").FindOne(ctx, bson.M{"_id": mongoOutboxRelayStateID}).Decode(&state); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return 0, nil
		}
		return 0, fmt.Errorf("mongo outbox eviction read relay state failed: %w", err)
	}
	if state.PublishedEventSeq <= 0 {
		return 0, nil
	}

	filter := bson.M{"created_at": bson.M{"$lt": before.UTC()}, "event_seq": bson.M{"$lte": state.PublishedEventSeq}}
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
	res, err := s.outboxEvents().DeleteMany(ctx, bson.M{"_id": bson.M{"$in": ids}, "event_seq": bson.M{"$lte": state.PublishedEventSeq}})
	if err != nil {
		return 0, fmt.Errorf("mongo outbox eviction delete failed: %w", err)
	}
	return res.DeletedCount, nil
}

var _ registrystore.EventOutboxStore = (*MongoStore)(nil)
var _ registrystore.OutboxHighWaterStore = (*MongoStore)(nil)
