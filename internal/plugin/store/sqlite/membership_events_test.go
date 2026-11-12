//go:build !nosqlite

package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/model"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/stretchr/testify/require"
)

type membershipCaptureBus struct {
	mu     sync.Mutex
	events []registryeventbus.Event
}

func (b *membershipCaptureBus) snapshot() []registryeventbus.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]registryeventbus.Event(nil), b.events...)
}

func (b *membershipCaptureBus) Publish(_ context.Context, e registryeventbus.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, e)
	return nil
}
func (*membershipCaptureBus) Subscribe(context.Context, string) (<-chan registryeventbus.Event, error) {
	return nil, nil
}
func (*membershipCaptureBus) Close() error { return nil }
func TestSQLiteMembershipEventsCommitAndRollback(t *testing.T) {
	cfg := &config.Config{DatastoreType: "sqlite", DBURL: filepath.Join(t.TempDir(), "memberships.db"), DatastoreMigrateAtStart: true, OutboxEnabled: true, EncryptionDBDisabled: true}
	ctx := config.WithContext(context.Background(), cfg)
	require.NoError(t, (&sqliteMigrator{}).Migrate(ctx))
	h, err := getSharedHandle(ctx)
	require.NoError(t, err)
	store := &SQLiteStore{handle: h, db: h.db, cfg: cfg}
	relayCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	bus := &membershipCaptureBus{}
	require.NoError(t, store.StartOutboxRelay(relayCtx, bus))
	sentinel := errors.New("abort")
	err = store.InWriteTx(ctx, func(tx context.Context) error {
		_, err := store.CreateConversationWithID(tx, "owner", "client", "aborted", "title", nil, nil, nil, nil)
		require.NoError(t, err)
		require.Empty(t, bus.snapshot())
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)
	require.Empty(t, bus.snapshot())
	require.NoError(t, store.InWriteTx(ctx, func(tx context.Context) error {
		_, err := store.CreateConversationWithID(tx, "owner", "client", "committed", "title", nil, nil, nil, nil)
		require.NoError(t, err)
		require.Empty(t, bus.snapshot())
		return nil
	}))
	require.Eventually(t, func() bool { return len(bus.snapshot()) == 1 }, 3*time.Second, time.Millisecond)
	require.NotEmpty(t, bus.snapshot()[0].OutboxCursor)
	require.NoError(t, store.InReadTx(ctx, func(tx context.Context) error {
		var cursor *registrystore.EventSnapshotMembershipCursor
		var all []model.ConversationMembership
		for {
			rows, next, err := store.AdminListEventSnapshotMemberships(tx, cursor, 1)
			require.NoError(t, err)
			all = append(all, rows...)
			if next == nil {
				break
			}
			cursor = next
		}
		require.Len(t, all, 1)
		require.Equal(t, "owner", all[0].UserID)
		row, err := store.AdminGetEventSnapshotMembership(tx, all[0].ConversationGroupID.String(), "owner")
		require.NoError(t, err)
		require.Equal(t, all[0], *row)
		return nil
	}))
}

type orderedMembershipBus struct {
	membershipCaptureBus
	calls        atomic.Int32
	firstStarted chan struct{}
	release      chan struct{}
}

func (b *orderedMembershipBus) Publish(ctx context.Context, event registryeventbus.Event) error {
	if b.calls.Add(1) == 1 {
		close(b.firstStarted)
		select {
		case <-b.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return b.membershipCaptureBus.Publish(ctx, event)
}
func TestSQLiteCommittedOutboxPublicationKeepsWriteOrder(t *testing.T) {
	cfg := &config.Config{DatastoreType: "sqlite", DBURL: filepath.Join(t.TempDir(), "ordered.db"), DatastoreMigrateAtStart: true, OutboxEnabled: true, EncryptionDBDisabled: true}
	ctx := config.WithContext(context.Background(), cfg)
	require.NoError(t, (&sqliteMigrator{}).Migrate(ctx))
	h, err := getSharedHandle(ctx)
	require.NoError(t, err)
	store := &SQLiteStore{handle: h, db: h.db, cfg: cfg}
	relayCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	bus := &orderedMembershipBus{firstStarted: make(chan struct{}), release: make(chan struct{})}
	require.NoError(t, store.StartOutboxRelay(relayCtx, bus))
	var once sync.Once
	release := func() { once.Do(func() { close(bus.release) }) }
	defer release()
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- store.InWriteTx(ctx, func(tx context.Context) error {
			_, err := store.CreateConversationWithID(tx, "owner", "client", "first", "title", nil, nil, nil, nil)
			return err
		})
	}()
	select {
	case <-bus.firstStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("first publication did not start")
	}
	entered := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- store.InWriteTx(ctx, func(tx context.Context) error {
			close(entered)
			_, err := store.CreateConversationWithID(tx, "owner", "client", "second", "title", nil, nil, nil, nil)
			return err
		})
	}()
	completed := false
	select {
	case err := <-secondDone:
		require.NoError(t, err)
		completed = true
	case <-time.After(time.Second):
	}
	release()
	require.NoError(t, <-firstDone)
	if !completed {
		require.NoError(t, <-secondDone)
	}
	require.True(t, completed, "a slow publisher must not hold the SQLite writer lock")
	require.Eventually(t, func() bool { return len(bus.snapshot()) == 2 }, 3*time.Second, time.Millisecond)
	require.Equal(t, "1", bus.snapshot()[0].OutboxCursor)
	require.Equal(t, "2", bus.snapshot()[1].OutboxCursor)
}

type failingMembershipBus struct {
	membershipCaptureBus
	attempts atomic.Int32
}

func (b *failingMembershipBus) Publish(ctx context.Context, event registryeventbus.Event) error {
	if b.attempts.Add(1) == 1 {
		return errors.New("temporary event bus failure")
	}
	return b.membershipCaptureBus.Publish(ctx, event)
}

func TestSQLiteOutboxRetriesWithoutDroppingEvents(t *testing.T) {
	cfg := &config.Config{DatastoreType: "sqlite", DBURL: filepath.Join(t.TempDir(), "retry.db"), DatastoreMigrateAtStart: true, OutboxEnabled: true, EncryptionDBDisabled: true}
	ctx, cancel := context.WithCancel(config.WithContext(context.Background(), cfg))
	defer cancel()
	require.NoError(t, (&sqliteMigrator{}).Migrate(ctx))
	h, err := getSharedHandle(ctx)
	require.NoError(t, err)
	store := &SQLiteStore{handle: h, db: h.db, cfg: cfg}
	bus := &failingMembershipBus{}
	require.NoError(t, store.StartOutboxRelay(ctx, bus))
	require.NoError(t, store.InWriteTx(ctx, func(tx context.Context) error {
		for _, id := range []string{"first", "second"} {
			if _, err := store.CreateConversationWithID(tx, "owner", "client", id, "title", nil, nil, nil, nil); err != nil {
				return err
			}
		}
		return nil
	}))
	require.Eventually(t, func() bool { return len(bus.snapshot()) == 2 }, 3*time.Second, time.Millisecond)
	require.Equal(t, "1", bus.snapshot()[0].OutboxCursor)
	require.Equal(t, "2", bus.snapshot()[1].OutboxCursor)
}

type panickingMembershipBus struct {
	membershipCaptureBus
	attempts atomic.Int32
}

func (b *panickingMembershipBus) Publish(ctx context.Context, event registryeventbus.Event) error {
	if b.attempts.Add(1) == 1 {
		panic("event bus panic")
	}
	return b.membershipCaptureBus.Publish(ctx, event)
}

func TestSQLiteOutboxRelayRestartsAfterPanic(t *testing.T) {
	cfg := &config.Config{DatastoreType: "sqlite", DBURL: filepath.Join(t.TempDir(), "panic.db"), DatastoreMigrateAtStart: true, OutboxEnabled: true, EncryptionDBDisabled: true}
	ctx, cancel := context.WithCancel(config.WithContext(context.Background(), cfg))
	defer cancel()
	require.NoError(t, (&sqliteMigrator{}).Migrate(ctx))
	h, err := getSharedHandle(ctx)
	require.NoError(t, err)
	store := &SQLiteStore{handle: h, db: h.db, cfg: cfg}
	bus := &panickingMembershipBus{}
	require.NoError(t, store.StartOutboxRelay(ctx, bus))
	require.NoError(t, store.InWriteTx(ctx, func(tx context.Context) error {
		for _, id := range []string{"first", "second"} {
			if _, err := store.CreateConversationWithID(tx, "owner", "client", id, "title", nil, nil, nil, nil); err != nil {
				return err
			}
		}
		return nil
	}))
	require.Eventually(t, func() bool { return len(bus.snapshot()) == 2 }, 3*time.Second, time.Millisecond)
	require.Equal(t, "1", bus.snapshot()[0].OutboxCursor)
	require.Equal(t, "2", bus.snapshot()[1].OutboxCursor)
	require.True(t, store.RelayPublishesOutboxEvents(), "the relay must keep owning publication after a recovered panic")
}

func TestSQLiteMembershipSnapshotsCrossPageAndGroupBoundaries(t *testing.T) {
	cfg := &config.Config{DatastoreType: "sqlite", DBURL: filepath.Join(t.TempDir(), "pages.db"), DatastoreMigrateAtStart: true, OutboxEnabled: true, EncryptionDBDisabled: true}
	ctx := config.WithContext(context.Background(), cfg)
	require.NoError(t, (&sqliteMigrator{}).Migrate(ctx))
	h, err := getSharedHandle(ctx)
	require.NoError(t, err)
	s := &SQLiteStore{handle: h, db: h.db, cfg: cfg}
	require.NoError(t, s.InWriteTx(ctx, func(tx context.Context) error {
		for _, id := range []string{"first-group", "second-group"} {
			conv, err := s.CreateConversationWithID(tx, "owner", "client", id, "title", nil, nil, nil, nil)
			if err != nil {
				return err
			}
			var rows []model.ConversationMembership
			for i := range 300 {
				rows = append(rows, model.ConversationMembership{ConversationGroupID: conv.Conversation.ConversationGroupID, UserID: fmt.Sprintf("member-%03d", i), AccessLevel: model.AccessLevelReader, CreatedAt: time.Now().UTC()})
			}
			if err := s.writeDBFor(tx, "snapshot test setup").Create(&rows).Error; err != nil {
				return err
			}
		}
		return nil
	}))
	seen := map[string]bool{}
	var cursor *registrystore.EventSnapshotMembershipCursor
	pages := 0
	for {
		var rows []model.ConversationMembership
		var next *registrystore.EventSnapshotMembershipCursor
		require.NoError(t, s.InReadTx(ctx, func(tx context.Context) error {
			var err error
			rows, next, err = s.AdminListEventSnapshotMemberships(tx, cursor, 500)
			return err
		}))
		pages++
		for _, row := range rows {
			id := row.ConversationGroupID.String() + "/" + row.UserID
			require.False(t, seen[id])
			seen[id] = true
		}
		if next == nil {
			break
		}
		cursor = next
	}
	require.Len(t, seen, 602)
	require.Equal(t, 2, pages)
}
