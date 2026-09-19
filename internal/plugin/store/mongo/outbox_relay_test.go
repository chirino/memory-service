//go:build !nomongo

package mongo

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/testutil/testmongo"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestMongoOutboxTransactionRelayReplayAndStaleCursor(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DatastoreType = "mongo"
	cfg.DBURL = testmongo.StartMongo(t)
	cfg.OutboxEnabled = true
	cfg.EncryptionDBDisabled = true
	ctx := config.WithContext(context.Background(), &cfg)
	loader, err := registrystore.Select("mongo")
	require.NoError(t, err)
	loaded, err := loader(ctx)
	require.NoError(t, err)
	store := loaded.(*MongoStore)
	t.Cleanup(func() { _ = store.client.Disconnect(context.Background()) })

	rollback := errors.New("rollback")
	err = store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, appendErr := store.AppendOutboxEvents(txCtx, []registrystore.OutboxWrite{{Event: "created", Kind: "conversation", Data: json.RawMessage(`{"conversation":"rolled-back"}`), CreatedAt: time.Now().UTC()}})
		require.NoError(t, appendErr)
		return rollback
	})
	require.ErrorIs(t, err, rollback)
	require.EqualValues(t, 0, countMongoOutboxDocuments(t, ctx, store))

	bus := &captureMongoEventBus{events: make(chan registryeventbus.Event, 4)}
	relayCtx, cancelRelay := context.WithCancel(ctx)
	require.NoError(t, store.StartOutboxRelay(relayCtx, bus))

	var appended []registrystore.OutboxEvent
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		var innerErr error
		appended, innerErr = store.AppendOutboxEvents(txCtx, []registrystore.OutboxWrite{{Event: "created", Kind: "conversation", Data: json.RawMessage(`{"conversation":"c1"}`), CreatedAt: time.Now().UTC()}})
		return innerErr
	}))
	require.Len(t, appended, 1)
	require.Empty(t, appended[0].Cursor, "request path must not invent a pre-commit change-stream cursor")

	first := receiveMongoRelayEvent(t, bus.events)
	require.Equal(t, "created", first.Event)
	require.Contains(t, first.OutboxCursor, "mongo:")
	page, err := store.ListOutboxEvents(ctx, registrystore.OutboxQuery{AfterCursor: "start", Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	require.Equal(t, first.OutboxCursor, page.Events[0].Cursor)
	highWater, err := store.CurrentOutboxCursor(ctx)
	require.NoError(t, err)
	require.Equal(t, first.OutboxCursor, highWater)

	cancelRelay()
	secondBus := &captureMongoEventBus{events: make(chan registryeventbus.Event, 4)}
	secondCtx, cancelSecond := context.WithCancel(ctx)
	defer cancelSecond()
	require.NoError(t, store.StartOutboxRelay(secondCtx, secondBus))
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, innerErr := store.AppendOutboxEvents(txCtx, []registrystore.OutboxWrite{{Event: "updated", Kind: "conversation", Data: json.RawMessage(`{"conversation":"c1"}`), CreatedAt: time.Now().UTC()}})
		return innerErr
	}))
	second := receiveMongoRelayEvent(t, secondBus.events)
	require.Equal(t, "updated", second.Event)
	page, err = store.ListOutboxEvents(ctx, registrystore.OutboxQuery{AfterCursor: first.OutboxCursor, Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	require.Equal(t, second.OutboxCursor, page.Events[0].Cursor)

	_, err = store.EvictOutboxEventsBefore(ctx, time.Now().Add(time.Minute), 100)
	require.NoError(t, err)
	_, err = store.ListOutboxEvents(ctx, registrystore.OutboxQuery{AfterCursor: first.OutboxCursor, Limit: 10})
	require.ErrorIs(t, err, registrystore.ErrStaleOutboxCursor)
}

func TestMongoOutboxRelayRetriesMaterializationBeforeAdvancing(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DatastoreType = "mongo"
	cfg.DBURL = testmongo.StartMongo(t)
	cfg.OutboxEnabled = true
	cfg.EncryptionDBDisabled = true
	ctx := config.WithContext(context.Background(), &cfg)
	loader, err := registrystore.Select("mongo")
	require.NoError(t, err)
	loaded, err := loader(ctx)
	require.NoError(t, err)
	store := loaded.(*MongoStore)
	t.Cleanup(func() { _ = store.client.Disconnect(context.Background()) })

	var attempts atomic.Int32
	store.materializeBeforeTransaction = func(bson.ObjectID) error {
		if attempts.Add(1) == 1 {
			return errors.New("transient materialization failure")
		}
		return nil
	}
	bus := &captureMongoEventBus{events: make(chan registryeventbus.Event, 4)}
	relayCtx, cancelRelay := context.WithCancel(ctx)
	defer cancelRelay()
	require.NoError(t, store.StartOutboxRelay(relayCtx, bus))

	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, appendErr := store.AppendOutboxEvents(txCtx, []registrystore.OutboxWrite{
			{Event: "created", Kind: "conversation", Data: json.RawMessage(`{"conversation":"first"}`), CreatedAt: time.Now().UTC()},
			{Event: "created", Kind: "conversation", Data: json.RawMessage(`{"conversation":"second"}`), CreatedAt: time.Now().UTC()},
		})
		return appendErr
	}))

	first := receiveMongoRelayEvent(t, bus.events)
	second := receiveMongoRelayEvent(t, bus.events)
	firstData, ok := first.Data.(map[string]any)
	require.True(t, ok)
	secondData, ok := second.Data.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "first", firstData["conversation"])
	require.Equal(t, "second", secondData["conversation"])
	page, err := store.ListOutboxEvents(ctx, registrystore.OutboxQuery{AfterCursor: "start", Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Events, 2)
	require.Equal(t, first.OutboxCursor, page.Events[0].Cursor)
	require.Equal(t, second.OutboxCursor, page.Events[1].Cursor)
}

func TestMongoOutboxRelayRecoversEventsCommittedBeforeStartup(t *testing.T) {
	ctx, store := newMongoOutboxTestStore(t)
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.AppendOutboxEvents(txCtx, []registrystore.OutboxWrite{{Event: "created", Kind: "conversation", Data: json.RawMessage(`{"conversation":"before-start"}`), CreatedAt: time.Now().UTC()}})
		return err
	}))

	bus := &captureMongoEventBus{events: make(chan registryeventbus.Event, 2)}
	relayCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	require.NoError(t, store.StartOutboxRelay(relayCtx, bus))
	event := receiveMongoRelayEventWithin(t, bus.events, 3*time.Second)
	require.Equal(t, "before-start", event.Data.(map[string]any)["conversation"])
	page, err := store.ListOutboxEvents(ctx, registrystore.OutboxQuery{AfterCursor: "start", Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
}

func TestMongoOutboxRelayRecoversAfterInvalidResumeToken(t *testing.T) {
	ctx, store := newMongoOutboxTestStore(t)
	invalidToken, err := bson.Marshal(bson.D{{Key: "_data", Value: "invalid-resume-token"}})
	require.NoError(t, err)
	_, err = store.db.Collection("outbox_relay_state").InsertOne(ctx, mongoOutboxRelayState{ID: mongoOutboxRelayStateID, ResumeToken: invalidToken})
	require.NoError(t, err)
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, appendErr := store.AppendOutboxEvents(txCtx, []registrystore.OutboxWrite{{Event: "created", Kind: "conversation", Data: json.RawMessage(`{"conversation":"history-gap"}`), CreatedAt: time.Now().UTC()}})
		return appendErr
	}))

	bus := &captureMongoEventBus{events: make(chan registryeventbus.Event, 4)}
	relayCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	require.NoError(t, store.StartOutboxRelay(relayCtx, bus))
	invalidation := receiveMongoRelayEventWithin(t, bus.events, 5*time.Second)
	require.Equal(t, "stream", invalidation.Kind)
	require.Equal(t, "invalidate", invalidation.Event)
	event := receiveMongoRelayEventWithin(t, bus.events, 5*time.Second)
	require.Equal(t, "history-gap", event.Data.(map[string]any)["conversation"])
}

func TestMongoOutboxRelayRecoversPublicationBeforePublishingLaterSequence(t *testing.T) {
	ctx, store := newMongoOutboxTestStore(t)
	failingBus := &failingMongoEventBus{}
	relayCtx, cancel := context.WithCancel(ctx)
	require.NoError(t, store.StartOutboxRelay(relayCtx, failingBus))
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.AppendOutboxEvents(txCtx, []registrystore.OutboxWrite{
			{Event: "created", Kind: "conversation", Data: json.RawMessage(`{"conversation":"first"}`), CreatedAt: time.Now().UTC()},
			{Event: "created", Kind: "conversation", Data: json.RawMessage(`{"conversation":"second"}`), CreatedAt: time.Now().UTC()},
		})
		return err
	}))
	require.Eventually(t, func() bool {
		count, err := store.outboxEvents().CountDocuments(ctx, bson.M{"event_seq": bson.M{"$exists": true}})
		return err == nil && count >= 1
	}, 10*time.Second, 50*time.Millisecond)
	cancel()

	bus := &captureMongoEventBus{events: make(chan registryeventbus.Event, 4)}
	secondCtx, cancelSecond := context.WithCancel(ctx)
	defer cancelSecond()
	require.NoError(t, store.StartOutboxRelay(secondCtx, bus))
	first := receiveMongoRelayEventWithin(t, bus.events, 3*time.Second)
	second := receiveMongoRelayEventWithin(t, bus.events, 3*time.Second)
	require.Equal(t, "first", first.Data.(map[string]any)["conversation"])
	require.Equal(t, "second", second.Data.(map[string]any)["conversation"])
}

func TestMongoOutboxPublisherLeasePreventsStalePublication(t *testing.T) {
	ctx, store := newMongoOutboxTestStore(t)
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.AppendOutboxEvents(txCtx, []registrystore.OutboxWrite{
			{Event: "created", Kind: "conversation", Data: json.RawMessage(`{"conversation":"first"}`), CreatedAt: time.Now().UTC()},
			{Event: "created", Kind: "conversation", Data: json.RawMessage(`{"conversation":"second"}`), CreatedAt: time.Now().UTC()},
		})
		return err
	}))
	require.NoError(t, store.recoverUnmaterializedMongoOutbox(ctx))

	blocked := &blockingMongoEventBus{started: make(chan struct{})}
	staleCtx, cancelStale := context.WithCancel(ctx)
	staleDone := make(chan error, 1)
	go func() { staleDone <- store.publishPendingMongoOutboxWithRetry(staleCtx, blocked) }()
	select {
	case <-blocked.started:
	case <-time.After(5 * time.Second):
		t.Fatal("stale publisher did not reach Publish")
	}

	bus := &captureMongoEventBus{events: make(chan registryeventbus.Event, 4)}
	currentDone := make(chan error, 1)
	go func() { currentDone <- store.publishPendingMongoOutboxWithRetry(ctx, bus) }()
	cancelStale()
	require.ErrorIs(t, <-staleDone, context.Canceled)
	first := receiveMongoRelayEventWithin(t, bus.events, 5*time.Second)
	second := receiveMongoRelayEventWithin(t, bus.events, 5*time.Second)
	require.NoError(t, <-currentDone)
	require.Equal(t, "first", first.Data.(map[string]any)["conversation"])
	require.Equal(t, "second", second.Data.(map[string]any)["conversation"])
	select {
	case event := <-bus.events:
		t.Fatalf("unexpected stale publication after ordered drain: %#v", event)
	default:
	}
}

func TestMongoOutboxEvictionRetainsUnpublishedSequences(t *testing.T) {
	ctx, store := newMongoOutboxTestStore(t)
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.AppendOutboxEvents(txCtx, []registrystore.OutboxWrite{
			{Event: "created", Kind: "conversation", Data: json.RawMessage(`{"conversation":"first"}`), CreatedAt: time.Now().Add(-time.Hour).UTC()},
			{Event: "created", Kind: "conversation", Data: json.RawMessage(`{"conversation":"second"}`), CreatedAt: time.Now().Add(-time.Hour).UTC()},
		})
		return err
	}))
	deleted, err := store.EvictOutboxEventsBefore(ctx, time.Now(), 10)
	require.NoError(t, err)
	require.Zero(t, deleted, "retention must be a no-op before relay state exists")
	require.EqualValues(t, 2, countMongoOutboxDocuments(t, ctx, store))

	cursor, err := store.outboxEvents().Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	require.NoError(t, err)
	var docs []outboxDoc
	require.NoError(t, cursor.All(ctx, &docs))
	require.Len(t, docs, 2)
	for i := range docs {
		token, marshalErr := bson.Marshal(bson.D{{Key: "_data", Value: "eviction-test-" + docs[i].ID.Hex()}})
		require.NoError(t, marshalErr)
		_, won, materializeErr := store.materializeMongoOutboxEvent(ctx, docs[i].ID, token, false)
		require.NoError(t, materializeErr)
		require.True(t, won)
	}

	deleted, err = store.EvictOutboxEventsBefore(ctx, time.Now(), 10)
	require.NoError(t, err)
	require.Zero(t, deleted)
	require.EqualValues(t, 2, countMongoOutboxDocuments(t, ctx, store))

	bus := &captureMongoEventBus{events: make(chan registryeventbus.Event, 2)}
	require.NoError(t, store.publishPendingMongoOutboxWithRetry(ctx, bus))
	first := receiveMongoRelayEventWithin(t, bus.events, time.Second)
	second := receiveMongoRelayEventWithin(t, bus.events, time.Second)
	require.Equal(t, "first", first.Data.(map[string]any)["conversation"])
	require.Equal(t, "second", second.Data.(map[string]any)["conversation"])
}

func newMongoOutboxTestStore(t *testing.T) (context.Context, *MongoStore) {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.DatastoreType = "mongo"
	cfg.DBURL = testmongo.StartMongo(t)
	cfg.OutboxEnabled = true
	cfg.EncryptionDBDisabled = true
	ctx := config.WithContext(context.Background(), &cfg)
	loader, err := registrystore.Select("mongo")
	require.NoError(t, err)
	loaded, err := loader(ctx)
	require.NoError(t, err)
	store := loaded.(*MongoStore)
	t.Cleanup(func() { _ = store.client.Disconnect(context.Background()) })
	return ctx, store
}

func countMongoOutboxDocuments(t *testing.T, ctx context.Context, store *MongoStore) int64 {
	t.Helper()
	count, err := store.outboxEvents().CountDocuments(ctx, map[string]any{})
	require.NoError(t, err)
	return count
}

func receiveMongoRelayEvent(t *testing.T, events <-chan registryeventbus.Event) registryeventbus.Event {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for MongoDB outbox relay event")
		return registryeventbus.Event{}
	}
}

func receiveMongoRelayEventWithin(t *testing.T, events <-chan registryeventbus.Event, timeout time.Duration) registryeventbus.Event {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(timeout):
		t.Fatal("timed out waiting for MongoDB outbox relay event")
		return registryeventbus.Event{}
	}
}

type captureMongoEventBus struct{ events chan registryeventbus.Event }

func (b *captureMongoEventBus) Publish(ctx context.Context, event registryeventbus.Event) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case b.events <- event:
		return nil
	}
}
func (b *captureMongoEventBus) Subscribe(context.Context, string) (<-chan registryeventbus.Event, error) {
	return b.events, nil
}
func (*captureMongoEventBus) Close() error { return nil }

type failingMongoEventBus struct{}

func (*failingMongoEventBus) Publish(context.Context, registryeventbus.Event) error {
	return errors.New("injected publication failure")
}
func (*failingMongoEventBus) Subscribe(context.Context, string) (<-chan registryeventbus.Event, error) {
	return nil, errors.New("unexpected subscribe")
}
func (*failingMongoEventBus) Close() error { return nil }

type durableFailingMongoEventBus struct{ captureMongoEventBus }

func (*durableFailingMongoEventBus) PublishDurable(context.Context, registryeventbus.Event) error {
	return errors.New("cross-node publish not acknowledged")
}

func TestMongoRelayRequiresDurableTransportAcknowledgement(t *testing.T) {
	ctx, store := newMongoOutboxTestStore(t)
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.AppendOutboxEvents(txCtx, []registrystore.OutboxWrite{{Event: "created", Kind: "conversation", Data: json.RawMessage(`{"conversation":"durable"}`), CreatedAt: time.Now().UTC()}})
		return err
	}))
	var doc outboxDoc
	require.NoError(t, store.outboxEvents().FindOne(ctx, bson.M{}).Decode(&doc))
	token, err := bson.Marshal(bson.D{{Key: "_data", Value: "durable-ack"}})
	require.NoError(t, err)
	_, won, err := store.materializeMongoOutboxEvent(ctx, doc.ID, token, false)
	require.NoError(t, err)
	require.True(t, won)
	bus := &durableFailingMongoEventBus{}
	owner := "durable-test"
	require.NoError(t, store.acquireMongoPublisherLease(ctx, owner))
	err = store.publishNextMongoOutboxEvent(ctx, bus, owner)
	require.ErrorContains(t, err, "not acknowledged")
	var state mongoOutboxRelayState
	require.NoError(t, store.db.Collection("outbox_relay_state").FindOne(ctx, bson.M{"_id": mongoOutboxRelayStateID}).Decode(&state))
	require.Zero(t, state.PublishedEventSeq)
}

type blockingMongoEventBus struct {
	started chan struct{}
	once    sync.Once
}

func (b *blockingMongoEventBus) Publish(ctx context.Context, _ registryeventbus.Event) error {
	b.once.Do(func() { close(b.started) })
	<-ctx.Done()
	return ctx.Err()
}
func (*blockingMongoEventBus) Subscribe(context.Context, string) (<-chan registryeventbus.Event, error) {
	return nil, errors.New("unexpected subscribe")
}
func (*blockingMongoEventBus) Close() error { return nil }
