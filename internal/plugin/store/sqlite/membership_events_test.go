//go:build !nosqlite

package sqlite

import (
	"context"
	"errors"
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

type membershipCaptureBus struct{ events []registryeventbus.Event }

func (b *membershipCaptureBus) Publish(_ context.Context, e registryeventbus.Event) error {
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
	bus := &membershipCaptureBus{}
	require.NoError(t, store.StartOutboxRelay(ctx, bus))
	sentinel := errors.New("abort")
	err = store.InWriteTx(ctx, func(tx context.Context) error {
		_, err := store.CreateConversationWithID(tx, "owner", "client", "aborted", "title", nil, nil, nil, nil)
		require.NoError(t, err)
		require.Empty(t, bus.events)
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)
	require.Empty(t, bus.events)
	require.NoError(t, store.InWriteTx(ctx, func(tx context.Context) error {
		_, err := store.CreateConversationWithID(tx, "owner", "client", "committed", "title", nil, nil, nil, nil)
		require.NoError(t, err)
		require.Empty(t, bus.events)
		return nil
	}))
	require.Len(t, bus.events, 1)
	require.NotEmpty(t, bus.events[0].OutboxCursor)
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
	bus := &orderedMembershipBus{firstStarted: make(chan struct{}), release: make(chan struct{})}
	require.NoError(t, store.StartOutboxRelay(ctx, bus))
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
	select {
	case <-entered:
		t.Fatal("a later transaction passed an unfinished committed publication")
	case <-time.After(100 * time.Millisecond):
	}
	release()
	require.NoError(t, <-firstDone)
	require.NoError(t, <-secondDone)
	require.Len(t, bus.events, 2)
	require.Equal(t, "1", bus.events[0].OutboxCursor)
	require.Equal(t, "2", bus.events[1].OutboxCursor)
}
