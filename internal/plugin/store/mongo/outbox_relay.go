//go:build !nomongo

package mongo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	"github.com/chirino/memory-service/internal/service/eventing"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const mongoOutboxRelayStateID = "outbox"

var errMongoOutboxAlreadyMaterialized = errors.New("mongo outbox event already materialized")

type mongoOutboxRelayState struct {
	ID                  string    `bson:"_id"`
	NextEventSeq        int64     `bson:"next_event_seq"`
	PublishedEventSeq   int64     `bson:"published_event_seq"`
	ResumeToken         bson.Raw  `bson:"resume_token,omitempty"`
	PublisherOwner      string    `bson:"publisher_owner,omitempty"`
	PublisherLeaseUntil time.Time `bson:"publisher_lease_until,omitempty"`
}

type mongoOutboxChange struct {
	OperationType string    `bson:"operationType"`
	FullDocument  outboxDoc `bson:"fullDocument"`
}

func (s *MongoStore) RelayPublishesOutboxEvents() bool {
	return s != nil && s.OutboxEnabled()
}

func (s *MongoStore) StartOutboxRelay(ctx context.Context, bus registryeventbus.EventBus) error {
	if s == nil || !s.OutboxEnabled() || bus == nil {
		return nil
	}
	stream, recovered, err := s.openMongoOutboxStreamRecovering(ctx)
	if err != nil {
		return fmt.Errorf("open MongoDB outbox change stream: %w", err)
	}
	go s.runMongoOutboxRelay(ctx, bus, stream, recovered)
	return nil
}

func (s *MongoStore) openMongoOutboxStream(ctx context.Context) (*mongo.ChangeStream, error) {
	var state mongoOutboxRelayState
	err := s.db.Collection("outbox_relay_state").FindOne(ctx, bson.M{"_id": mongoOutboxRelayStateID}).Decode(&state)
	if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
		return nil, err
	}
	opts := options.ChangeStream().SetFullDocument(options.UpdateLookup).SetMaxAwaitTime(time.Second)
	if len(state.ResumeToken) > 0 {
		opts.SetResumeAfter(state.ResumeToken)
	}
	pipeline := mongo.Pipeline{bson.D{{Key: "$match", Value: bson.M{"operationType": "insert"}}}}
	return s.outboxEvents().Watch(ctx, pipeline, opts)
}

func (s *MongoStore) openMongoOutboxStreamRecovering(ctx context.Context) (*mongo.ChangeStream, bool, error) {
	stream, err := s.openMongoOutboxStream(ctx)
	if err == nil {
		return stream, false, nil
	}
	result, clearErr := s.db.Collection("outbox_relay_state").UpdateOne(ctx,
		bson.M{"_id": mongoOutboxRelayStateID, "resume_token": bson.M{"$exists": true}},
		bson.M{"$unset": bson.M{"resume_token": ""}},
	)
	if clearErr != nil {
		return nil, false, errors.Join(err, clearErr)
	}
	if result.ModifiedCount == 0 {
		return nil, false, err
	}
	log.Warn("MongoDB outbox relay discarded an unusable resume token", "err", err)
	stream, err = s.openMongoOutboxStream(ctx)
	return stream, true, err
}

func (s *MongoStore) runMongoOutboxRelay(ctx context.Context, bus registryeventbus.EventBus, stream *mongo.ChangeStream, recovered bool) {
	defer func() { _ = stream.Close(context.Background()) }()
	for {
		if recovered {
			if err := publishMongoRelayInvalidation(ctx, bus); err != nil && ctx.Err() == nil {
				log.Warn("MongoDB outbox relay invalidate publication failed", "err", err)
			}
			recovered = false
		}
		if err := s.recoverAndPublishMongoOutboxWithRetry(ctx, bus); err != nil {
			return
		}
		for stream.Next(ctx) {
			var change mongoOutboxChange
			if err := stream.Decode(&change); err != nil {
				log.Warn("MongoDB outbox relay decode failed", "err", err)
				continue
			}
			token := append(bson.Raw(nil), stream.ResumeToken()...)
			_, _, err := s.materializeMongoOutboxEventWithRetry(ctx, change.FullDocument.ID, token, true)
			if err != nil {
				return
			}
			if err := s.checkpointMongoOutboxResumeToken(ctx, token); err != nil {
				log.Warn("MongoDB outbox relay resume checkpoint failed", "err", err)
				return
			}
			if err := s.publishPendingMongoOutboxWithRetry(ctx, bus); err != nil {
				return
			}
		}
		if ctx.Err() != nil {
			return
		}
		if err := stream.Err(); err != nil {
			log.Warn("MongoDB outbox change stream interrupted", "err", err)
			if publishErr := publishMongoRelayInvalidation(ctx, bus); publishErr != nil && ctx.Err() == nil {
				log.Warn("MongoDB outbox relay invalidate publication failed", "err", publishErr)
			}
		}
		_ = stream.Close(context.Background())
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		var err error
		stream, recovered, err = s.openMongoOutboxStreamRecovering(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Warn("MongoDB outbox change stream reconnect failed", "err", err)
			continue
		}
	}
}

func (s *MongoStore) recoverAndPublishMongoOutboxWithRetry(ctx context.Context, bus registryeventbus.EventBus) error {
	for {
		if err := s.recoverUnmaterializedMongoOutbox(ctx); err == nil {
			return s.publishPendingMongoOutboxWithRetry(ctx, bus)
		} else if ctx.Err() == nil {
			log.Warn("MongoDB outbox relay recovery failed", "err", err, "retryIn", 100*time.Millisecond)
		} else {
			return ctx.Err()
		}
		if err := waitMongoOutboxRetry(ctx); err != nil {
			return err
		}
	}
}

func (s *MongoStore) recoverUnmaterializedMongoOutbox(ctx context.Context) error {
	cursor, err := s.outboxEvents().Find(ctx, bson.M{"event_seq": bson.M{"$exists": false}}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return err
	}
	defer func() { _ = cursor.Close(context.Background()) }()
	for cursor.Next(ctx) {
		var doc outboxDoc
		if err := cursor.Decode(&doc); err != nil {
			return err
		}
		token, err := bson.Marshal(bson.D{{Key: "_data", Value: "recovered:" + doc.ID.Hex()}})
		if err != nil {
			return err
		}
		if _, _, err := s.materializeMongoOutboxEventWithRetry(ctx, doc.ID, token, false); err != nil {
			return err
		}
	}
	return cursor.Err()
}

func (s *MongoStore) publishPendingMongoOutboxWithRetry(ctx context.Context, bus registryeventbus.EventBus) error {
	owner := uuid.NewString()
	for {
		if err := s.acquireMongoPublisherLease(ctx, owner); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err := waitMongoOutboxRetry(ctx); err != nil {
				return err
			}
			continue
		}
		err := s.publishNextMongoOutboxEvent(ctx, bus, owner)
		_ = s.releaseMongoPublisherLease(context.Background(), owner)
		if errors.Is(err, mongo.ErrNoDocuments) || err == nil {
			return nil
		} else if ctx.Err() == nil {
			log.Warn("MongoDB outbox relay publish failed", "err", err, "retryIn", 100*time.Millisecond)
		} else {
			return ctx.Err()
		}
		if err := waitMongoOutboxRetry(ctx); err != nil {
			return err
		}
	}
}

func (s *MongoStore) publishNextMongoOutboxEvent(ctx context.Context, bus registryeventbus.EventBus, owner string) error {
	for {
		if err := s.renewMongoPublisherLease(ctx, owner); err != nil {
			return err
		}
		var state mongoOutboxRelayState
		if err := s.db.Collection("outbox_relay_state").FindOne(ctx, bson.M{"_id": mongoOutboxRelayStateID}).Decode(&state); err != nil {
			return err
		}
		var doc outboxDoc
		err := s.outboxEvents().FindOne(ctx,
			bson.M{"event_seq": bson.M{"$gt": state.PublishedEventSeq}},
			options.FindOne().SetSort(bson.D{{Key: "event_seq", Value: 1}}),
		).Decode(&doc)
		if err != nil {
			return err
		}
		if doc.EventSeq == nil {
			return errors.New("MongoDB outbox relay selected an event without a sequence")
		}
		event, err := mongoRelayEvent(doc, doc.ResumeToken, *doc.EventSeq)
		if err != nil {
			return err
		}
		if err := s.publishMongoRelayEventWithLease(ctx, bus, event, owner); err != nil {
			return err
		}
		result, err := s.db.Collection("outbox_relay_state").UpdateOne(ctx,
			bson.M{"_id": mongoOutboxRelayStateID, "publisher_owner": owner, "publisher_lease_until": bson.M{"$gt": time.Now().UTC()}},
			bson.M{"$max": bson.M{"published_event_seq": *doc.EventSeq}},
		)
		if err != nil {
			return err
		}
		if result.ModifiedCount != 1 {
			return errors.New("MongoDB outbox publisher lease lost")
		}
	}
}

func (s *MongoStore) publishMongoRelayEventWithLease(ctx context.Context, bus registryeventbus.EventBus, event registryeventbus.Event, owner string) error {
	publishCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- publishMongoRelayEvent(publishCtx, s, bus, event) }()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			return err
		case <-ticker.C:
			if err := s.renewMongoPublisherLease(ctx, owner); err != nil {
				cancel()
				<-done
				return err
			}
		case <-ctx.Done():
			cancel()
			<-done
			return ctx.Err()
		}
	}
}

func (s *MongoStore) acquireMongoPublisherLease(ctx context.Context, owner string) error {
	now := time.Now().UTC()
	if _, err := s.db.Collection("outbox_relay_state").UpdateOne(ctx,
		bson.M{"_id": mongoOutboxRelayStateID},
		bson.M{"$setOnInsert": bson.M{"next_event_seq": int64(0), "published_event_seq": int64(0)}},
		options.UpdateOne().SetUpsert(true),
	); err != nil {
		return err
	}
	return s.db.Collection("outbox_relay_state").FindOneAndUpdate(ctx, bson.M{"_id": mongoOutboxRelayStateID, "$or": bson.A{bson.M{"publisher_owner": owner}, bson.M{"publisher_lease_until": bson.M{"$lte": now}}, bson.M{"publisher_lease_until": bson.M{"$exists": false}}}}, bson.M{"$set": bson.M{"publisher_owner": owner, "publisher_lease_until": now.Add(10 * time.Second)}}, options.FindOneAndUpdate().SetReturnDocument(options.After)).Err()
}

func (s *MongoStore) renewMongoPublisherLease(ctx context.Context, owner string) error {
	result, err := s.db.Collection("outbox_relay_state").UpdateOne(ctx, bson.M{"_id": mongoOutboxRelayStateID, "publisher_owner": owner, "publisher_lease_until": bson.M{"$gt": time.Now().UTC()}}, bson.M{"$set": bson.M{"publisher_lease_until": time.Now().UTC().Add(10 * time.Second)}})
	if err != nil {
		return err
	}
	if result.ModifiedCount != 1 {
		return errors.New("MongoDB outbox publisher lease lost")
	}
	return nil
}

func (s *MongoStore) releaseMongoPublisherLease(ctx context.Context, owner string) error {
	_, err := s.db.Collection("outbox_relay_state").UpdateOne(ctx, bson.M{"_id": mongoOutboxRelayStateID, "publisher_owner": owner}, bson.M{"$unset": bson.M{"publisher_owner": "", "publisher_lease_until": ""}})
	return err
}

func (s *MongoStore) checkpointMongoOutboxResumeToken(ctx context.Context, token bson.Raw) error {
	_, err := s.db.Collection("outbox_relay_state").UpdateOne(ctx,
		bson.M{"_id": mongoOutboxRelayStateID},
		bson.M{"$set": bson.M{"resume_token": token}},
		options.UpdateOne().SetUpsert(true),
	)
	return err
}

func waitMongoOutboxRetry(ctx context.Context) error {
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func publishMongoRelayInvalidation(ctx context.Context, bus registryeventbus.EventBus) error {
	return bus.Publish(ctx, registryeventbus.Event{Event: "invalidate", Kind: "stream", Data: map[string]string{"reason": "mongo outbox relay recovery"}})
}

func (s *MongoStore) materializeMongoOutboxEventWithRetry(ctx context.Context, id bson.ObjectID, token bson.Raw, checkpointResume bool) (int64, bool, error) {
	for {
		seq, won, err := s.materializeMongoOutboxEvent(ctx, id, token, checkpointResume)
		if err == nil {
			return seq, won, nil
		}
		if ctx.Err() != nil {
			return 0, false, ctx.Err()
		}
		log.Warn("MongoDB outbox relay materialization failed", "err", err, "retryIn", 100*time.Millisecond)
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0, false, ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *MongoStore) materializeMongoOutboxEvent(ctx context.Context, id bson.ObjectID, token bson.Raw, checkpointResume bool) (int64, bool, error) {
	if s.materializeBeforeTransaction != nil {
		if err := s.materializeBeforeTransaction(id); err != nil {
			return 0, false, err
		}
	}
	session, err := s.client.StartSession()
	if err != nil {
		return 0, false, err
	}
	defer session.EndSession(ctx)
	var seq int64
	_, err = session.WithTransaction(ctx, func(txCtx context.Context) (any, error) {
		var state mongoOutboxRelayState
		update := bson.M{"$inc": bson.M{"next_event_seq": 1}}
		if checkpointResume {
			update["$set"] = bson.M{"resume_token": token}
		}
		err := s.db.Collection("outbox_relay_state").FindOneAndUpdate(txCtx,
			bson.M{"_id": mongoOutboxRelayStateID},
			update,
			options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
		).Decode(&state)
		if err != nil {
			return nil, err
		}
		seq = state.NextEventSeq
		result, err := s.outboxEvents().UpdateOne(txCtx, bson.M{"_id": id, "event_seq": bson.M{"$exists": false}}, bson.M{"$set": bson.M{"event_seq": seq, "resume_token": token}})
		if err != nil {
			return nil, err
		}
		if result.ModifiedCount != 1 {
			return nil, errMongoOutboxAlreadyMaterialized
		}
		return nil, nil
	})
	if errors.Is(err, errMongoOutboxAlreadyMaterialized) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return seq, true, nil
}

func mongoRelayEvent(doc outboxDoc, token bson.Raw, seq int64) (registryeventbus.Event, error) {
	raw, err := json.Marshal(doc.Data)
	if err != nil {
		return registryeventbus.Event{}, err
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return registryeventbus.Event{}, err
	}
	occurred := doc.CreatedAt.UTC()
	event := registryeventbus.Event{Event: doc.Event, Kind: doc.Kind, Data: payload, OutboxCursor: formatMongoOutboxCursor(token), OccurredAt: &occurred}
	if rawGroup, ok := payload["conversation_group"].(string); ok {
		if groupID, err := uuid.Parse(rawGroup); err == nil {
			event.ConversationGroupID = groupID
		}
	}
	switch doc.Kind {
	case "membership":
		if user, ok := payload["user"].(string); ok && strings.TrimSpace(user) != "" {
			event.UserIDs = []string{user}
		}
	case "conversation":
		if doc.Event == "deleted" {
			event.UserIDs = mongoStringSlice(payload["members"])
		}
	}
	_ = seq
	return event, nil
}

func mongoStringSlice(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok && text != "" {
			result = append(result, text)
		}
	}
	return result
}

func publishMongoRelayEvent(ctx context.Context, store *MongoStore, bus registryeventbus.EventBus, event registryeventbus.Event) error {
	bus = durableRelayBus{EventBus: bus}
	switch {
	case len(event.UserIDs) > 0:
		return eventing.PublishToUsers(ctx, bus, event.UserIDs, event)
	case event.ConversationGroupID != uuid.Nil:
		return eventing.PublishToGroup(ctx, store, bus, event.ConversationGroupID, event)
	default:
		return bus.Publish(ctx, event)
	}
}

type durableRelayBus struct{ registryeventbus.EventBus }

func (b durableRelayBus) Publish(ctx context.Context, event registryeventbus.Event) error {
	if durable, ok := b.EventBus.(registryeventbus.DurablePublisher); ok {
		return durable.PublishDurable(ctx, event)
	}
	return b.EventBus.Publish(ctx, event)
}
