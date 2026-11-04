//go:build !nopostgresql

package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	localbus "github.com/chirino/memory-service/internal/plugin/eventbus/local"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

// Retry loops log connection losses at DEBUG. Timeouts must not count as
// connection losses: a relay call that times out is a stall, not an outage.
func TestRelayConnectionLostClassifiesConnectionErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil error", err: nil, want: false},
		{name: "connection refused", err: fmt.Errorf("dial: %w", syscall.ECONNREFUSED), want: true},
		{name: "unexpected EOF", err: fmt.Errorf("read: %w", io.ErrUnexpectedEOF), want: true},
		{name: "broken pipe", err: fmt.Errorf("write: %w", syscall.EPIPE), want: true},
		{name: "closed network connection", err: fmt.Errorf("read: %w", net.ErrClosed), want: true},
		{name: "server shutting down", err: fmt.Errorf("lock: %w", &pgconn.PgError{Code: "57P01", Message: "terminating connection due to administrator command"}), want: true},
		{name: "server starting up", err: fmt.Errorf("connect: %w", &pgconn.PgError{Code: "57P03", Message: "the database system is starting up"}), want: true},
		{name: "call timeout", err: fmt.Errorf("materialize: %w", context.DeadlineExceeded), want: false},
		{name: "unrelated error", err: errors.New("permission denied for publication"), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, relayConnectionLost(tc.err))
		})
	}
}

func TestRelayWatchdogRestartsOnlyAStalledLeader(t *testing.T) {
	const timeout = time.Minute
	stalled := func(txSeq int64, age time.Duration) unsequencedOutboxStats {
		return unsequencedOutboxStats{Count: 1, OldestTxSeq: sql.NullInt64{Int64: txSeq, Valid: true}, OldestAge: age.Seconds()}
	}
	healthy := unsequencedOutboxStats{}

	t.Run("same old row and no progress on consecutive checks", func(t *testing.T) {
		w := relayWatchdog{}
		require.False(t, w.observe(stalled(7, 2*timeout), 3, true, 2*timeout, timeout), "first sighting never restarts")
		require.True(t, w.observe(stalled(7, 2*timeout), 3, true, 2*timeout, timeout))
	})
	t.Run("long transaction committed old rows", func(t *testing.T) {
		w := relayWatchdog{}
		require.False(t, w.observe(healthy, 3, true, 2*timeout, timeout))
		require.False(t, w.observe(stalled(7, 2*timeout), 3, true, 2*timeout, timeout))
	})
	t.Run("leader is still numbering rows", func(t *testing.T) {
		w := relayWatchdog{}
		require.False(t, w.observe(stalled(7, 2*timeout), 3, true, 2*timeout, timeout))
		require.False(t, w.observe(stalled(7, 2*timeout), 4, true, 2*timeout, timeout))
	})
	t.Run("oldest row is younger than the timeout", func(t *testing.T) {
		w := relayWatchdog{}
		require.False(t, w.observe(stalled(7, timeout/2), 3, true, 2*timeout, timeout))
		require.False(t, w.observe(stalled(7, timeout/2), 3, true, 2*timeout, timeout))
	})
	t.Run("leader restarted recently", func(t *testing.T) {
		w := relayWatchdog{}
		require.False(t, w.observe(stalled(7, 2*timeout), 3, true, timeout/2, timeout))
		require.False(t, w.observe(stalled(7, 2*timeout), 3, true, timeout/2, timeout))
	})
	t.Run("this process is not the leader", func(t *testing.T) {
		w := relayWatchdog{}
		require.False(t, w.observe(stalled(7, 2*timeout), 3, false, 0, timeout))
		require.False(t, w.observe(stalled(7, 2*timeout), 3, false, 0, timeout))
	})
	t.Run("restarts disabled", func(t *testing.T) {
		w := relayWatchdog{}
		require.False(t, w.observe(stalled(7, 2*timeout), 3, true, 2*timeout, 0))
		require.False(t, w.observe(stalled(7, 2*timeout), 3, true, 2*timeout, 0))
	})
}

// blockingRelayBus blocks every publish until its context ends, like an event
// bus call that never returns.
type blockingRelayBus struct{ published atomic.Int64 }

func (b *blockingRelayBus) Publish(ctx context.Context, _ registryeventbus.Event) error {
	b.published.Add(1)
	<-ctx.Done()
	return ctx.Err()
}

func (*blockingRelayBus) Subscribe(context.Context, string) (<-chan registryeventbus.Event, error) {
	return make(chan registryeventbus.Event), nil
}

func (*blockingRelayBus) Close() error { return nil }

// recordingRelayBus records every published event.
type recordingRelayBus struct {
	mu     sync.Mutex
	events []registryeventbus.Event
}

func (b *recordingRelayBus) Publish(_ context.Context, event registryeventbus.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, event)
	return nil
}

func (*recordingRelayBus) Subscribe(context.Context, string) (<-chan registryeventbus.Event, error) {
	return make(chan registryeventbus.Event), nil
}

func (*recordingRelayBus) Close() error { return nil }

func (b *recordingRelayBus) cursors() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.events))
	for _, event := range b.events {
		out = append(out, event.OutboxCursor)
	}
	return out
}

func appendMembershipOutboxRow(t *testing.T, store *PostgresStore, ctx context.Context, user string, createdAt time.Time) int64 {
	t.Helper()
	var txSeq int64
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		if _, err := store.AppendOutboxEvents(txCtx, []registrystore.OutboxWrite{{
			Event:     "created",
			Kind:      "membership",
			Data:      json.RawMessage(fmt.Sprintf(`{"user":%q}`, user)),
			CreatedAt: createdAt,
		}}); err != nil {
			return err
		}
		return store.dbFor(txCtx).Raw("SELECT max(tx_seq) FROM outbox_events").Scan(&txSeq).Error
	}))
	return txSeq
}

func outboxEventSeq(t *testing.T, store *PostgresStore, txSeq int64) sql.NullInt64 {
	t.Helper()
	var eventSeq sql.NullInt64
	require.NoError(t, store.db.Raw("SELECT event_seq FROM outbox_events WHERE tx_seq = ?", txSeq).Row().Scan(&eventSeq))
	return eventSeq
}

func TestPostgresOutboxRelayUnsequencedStats(t *testing.T) {
	store, ctx := setupPostgresOutboxStore(t)
	relay := newPostgresOutboxRelay(store, localbus.New(16))

	stats, err := relay.loadUnsequencedStats(ctx)
	require.NoError(t, err)
	require.Zero(t, stats.Count)
	require.False(t, stats.OldestTxSeq.Valid)
	require.Zero(t, stats.OldestAge)

	txSeq := appendMembershipOutboxRow(t, store, ctx, "user-1", time.Now().Add(-time.Hour))
	stats, err = relay.loadUnsequencedStats(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), stats.Count)
	require.Equal(t, txSeq, stats.OldestTxSeq.Int64)
	require.GreaterOrEqual(t, stats.OldestAge, (59 * time.Minute).Seconds())
}

// Rows committed before the replication slot exists are never streamed by it:
// rows a replica writes while another replica is still creating the slot, or
// rows left behind when a failover or restore loses the slot. The relay must
// number and publish them when it creates the slot, before any streamed row,
// and must not number or publish streamed rows twice.
func TestPostgresOutboxRelaySequencesRowsCommittedBeforeSlot(t *testing.T) {
	store, ctx := setupPostgresOutboxStore(t)
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)

	before := []int64{
		appendMembershipOutboxRow(t, store, ctx, "user-1", time.Now()),
		appendMembershipOutboxRow(t, store, ctx, "user-2", time.Now()),
	}

	bus := &recordingRelayBus{}
	require.NoError(t, store.StartOutboxRelay(ctx, bus))
	for _, txSeq := range before {
		require.True(t, outboxEventSeq(t, store, txSeq).Valid, "rows committed before the slot must be numbered during relay startup")
	}

	after := appendMembershipOutboxRow(t, store, ctx, "user-3", time.Now())
	require.Eventually(t, func() bool { return outboxEventSeq(t, store, after).Valid }, 15*time.Second, 50*time.Millisecond)

	first, second, third := outboxEventSeq(t, store, before[0]).Int64, outboxEventSeq(t, store, before[1]).Int64, outboxEventSeq(t, store, after).Int64
	require.Less(t, first, second)
	require.Less(t, second, third)
	require.Eventually(t, func() bool { return len(bus.cursors()) == 3 }, 15*time.Second, 50*time.Millisecond)
	require.Equal(t, []string{
		formatPostgresOutboxCursor(first),
		formatPostgresOutboxCursor(second),
		formatPostgresOutboxCursor(third),
	}, bus.cursors(), "each row is published once, in event_seq order")
}

// A publish that never returns stalls the relay while it keeps the leader
// lock. The watchdog must restart that leader, and the leader must release its
// advisory lock on exit so any session, not only this process's pool, can
// take over.
func TestPostgresOutboxRelayWatchdogRestartsStalledLeaderAndReleasesLock(t *testing.T) {
	store, ctx := setupPostgresOutboxStore(t)
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)

	bus := &blockingRelayBus{}
	relay := newPostgresOutboxRelay(store, bus)
	relay.callTimeout = time.Minute
	relay.stallTimeout = 500 * time.Millisecond
	relay.watchdogInterval = 100 * time.Millisecond
	lockConn, acquired, err := relay.tryAcquireLeaderLock(ctx)
	require.NoError(t, err)
	require.True(t, acquired)
	ready := make(chan error, 1)
	go relay.runLeader(ctx, lockConn, ready)
	require.NoError(t, <-ready)

	// The first row is numbered, then its publish blocks; the second row
	// waits behind it.
	appendMembershipOutboxRow(t, store, ctx, "user-1", time.Now())
	require.Eventually(t, func() bool { return bus.published.Load() > 0 }, 15*time.Second, 50*time.Millisecond)
	waiting := appendMembershipOutboxRow(t, store, ctx, "user-2", time.Now())

	// Another database session cannot take the lock while the leader runs.
	otherDB, err := sql.Open("pgx", store.cfg.DBURL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = otherDB.Close() })
	require.False(t, tryOutboxLeaderLock(t, otherDB))

	relay.mu.Lock()
	firstStart := relay.leaderStarted
	relay.mu.Unlock()

	// Only the watchdog runs; no candidate loop retakes leadership.
	go relay.watchdogLoop(ctx)
	require.Eventually(t, func() bool { return !relay.leaderRunning.Load() }, 15*time.Second, 50*time.Millisecond,
		"the watchdog must stop a leader that stopped numbering rows")
	require.False(t, outboxEventSeq(t, store, waiting).Valid)

	require.True(t, tryOutboxLeaderLock(t, otherDB), "the stopped leader must release its advisory lock")

	go relay.candidateLoop(ctx)
	require.Eventually(t, func() bool {
		relay.mu.Lock()
		defer relay.mu.Unlock()
		return relay.leaderCancel != nil && relay.leaderStarted.After(firstStart)
	}, 15*time.Second, 50*time.Millisecond, "the candidate loop must take leadership again")
}

// tryOutboxLeaderLock reports whether the leader lock is free, releasing it
// again when it was taken.
func tryOutboxLeaderLock(t *testing.T, db *sql.DB) bool {
	t.Helper()
	conn, err := db.Conn(context.Background())
	require.NoError(t, err)
	defer conn.Close()
	var acquired bool
	require.NoError(t, conn.QueryRowContext(context.Background(), "SELECT pg_try_advisory_lock($1, $2)", postgresOutboxLockClass, postgresOutboxLockKey).Scan(&acquired))
	if acquired {
		_, err := conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1, $2)", postgresOutboxLockClass, postgresOutboxLockKey)
		require.NoError(t, err)
	}
	return acquired
}

// The relay must confirm the server's position from keepalives, not only after
// transactions that write outbox rows, so writes to other tables do not pile
// up as retained WAL behind the slot.
func TestPostgresOutboxRelayConfirmsPositionWithoutOutboxWrites(t *testing.T) {
	store, ctx := setupPostgresOutboxStore(t)
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)

	relay := newPostgresOutboxRelay(store, &recordingRelayBus{})
	relay.statusInterval = 200 * time.Millisecond
	lockConn, acquired, err := relay.tryAcquireLeaderLock(ctx)
	require.NoError(t, err)
	require.True(t, acquired)
	ready := make(chan error, 1)
	go relay.runLeader(ctx, lockConn, ready)
	require.NoError(t, <-ready)

	require.NoError(t, store.db.Exec("CREATE TABLE relay_wal_noise (id integer)").Error)
	require.NoError(t, store.db.Exec("INSERT INTO relay_wal_noise SELECT generate_series(1, 1000)").Error)
	var targetText string
	require.NoError(t, store.db.Raw("SELECT pg_current_wal_flush_lsn()::text").Row().Scan(&targetText))
	target, err := pglogrepl.ParseLSN(targetText)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		confirmed, exists, err := currentSlotLSN(ctx, store.db, postgresOutboxSlotName)
		return err == nil && exists && confirmed >= target
	}, 30*time.Second, 100*time.Millisecond, "the slot must advance past writes that did not touch the outbox")
}

// A leader can die after it creates the replication slot but before it numbers
// the rows committed before the slot. The slot never streams those rows, so
// the next leader must recreate the slot and number them, ahead of any
// streamed row.
func TestPostgresOutboxRelayRecoversInterruptedSlotBackfill(t *testing.T) {
	store, ctx := setupPostgresOutboxStore(t)
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)

	before := []int64{
		appendMembershipOutboxRow(t, store, ctx, "user-1", time.Now()),
		appendMembershipOutboxRow(t, store, ctx, "user-2", time.Now()),
	}

	// What a leader leaves behind when it dies right after creating the slot.
	require.NoError(t, store.db.Exec("INSERT INTO schema_metadata (key, value) VALUES ('outbox_relay_slot_backfill', 'pending')").Error)
	replConn, err := connectReplication(ctx, store.cfg.DBURL)
	require.NoError(t, err)
	_, err = pglogrepl.CreateReplicationSlot(ctx, replConn, postgresOutboxSlotName, "pgoutput", pglogrepl.CreateReplicationSlotOptions{})
	require.NoError(t, err)
	require.NoError(t, replConn.Close(ctx))

	bus := &recordingRelayBus{}
	require.NoError(t, store.StartOutboxRelay(ctx, bus))
	after := appendMembershipOutboxRow(t, store, ctx, "user-3", time.Now())

	require.Eventually(t, func() bool {
		return outboxEventSeq(t, store, before[0]).Valid && outboxEventSeq(t, store, before[1]).Valid && outboxEventSeq(t, store, after).Valid
	}, 15*time.Second, 50*time.Millisecond, "rows committed before an interrupted backfill must still be numbered")

	first, second, third := outboxEventSeq(t, store, before[0]).Int64, outboxEventSeq(t, store, before[1]).Int64, outboxEventSeq(t, store, after).Int64
	require.Less(t, first, second)
	require.Less(t, second, third)
	require.Eventually(t, func() bool { return len(bus.cursors()) == 3 }, 15*time.Second, 50*time.Millisecond)
	require.Equal(t, []string{
		formatPostgresOutboxCursor(first),
		formatPostgresOutboxCursor(second),
		formatPostgresOutboxCursor(third),
	}, bus.cursors(), "each row is published once, in event_seq order")
}

// failingOnceRelayBus fails the publish call numbered failOn and records every
// successful publish.
type failingOnceRelayBus struct {
	recordingRelayBus
	failOn int64
	calls  atomic.Int64
}

func (b *failingOnceRelayBus) Publish(ctx context.Context, event registryeventbus.Event) error {
	if b.calls.Add(1) == b.failOn {
		return errors.New("event bus unavailable")
	}
	return b.recordingRelayBus.Publish(ctx, event)
}

// distinctCursors returns cursors in first-published order without the
// repeats that at-least-once retries may add.
func distinctCursors(cursors []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(cursors))
	for _, cursor := range cursors {
		if !seen[cursor] {
			seen[cursor] = true
			out = append(out, cursor)
		}
	}
	return out
}

// Event stream clients resume after their last cursor. If the relay streamed
// newer events after failing to publish backfilled ones, a client resuming
// from a streamed cursor would skip the backfilled events for good. A failed
// backfill publish must therefore be retried before anything is streamed, and
// an event bus outage must not fail process startup.
func TestPostgresOutboxRelayRetriesBackfillPublishBeforeStreaming(t *testing.T) {
	store, ctx := setupPostgresOutboxStore(t)
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)

	before := []int64{
		appendMembershipOutboxRow(t, store, ctx, "user-1", time.Now()),
		appendMembershipOutboxRow(t, store, ctx, "user-2", time.Now()),
		appendMembershipOutboxRow(t, store, ctx, "user-3", time.Now()),
	}

	bus := &failingOnceRelayBus{failOn: 2}
	require.NoError(t, store.StartOutboxRelay(ctx, bus), "an event bus outage must not fail startup")
	after := appendMembershipOutboxRow(t, store, ctx, "user-4", time.Now())

	require.Eventually(t, func() bool { return len(distinctCursors(bus.cursors())) == 4 }, 30*time.Second, 50*time.Millisecond,
		"every backfilled event must eventually be published")
	want := make([]string, 0, 4)
	for _, txSeq := range append(before, after) {
		eventSeq := outboxEventSeq(t, store, txSeq)
		require.True(t, eventSeq.Valid)
		want = append(want, formatPostgresOutboxCursor(eventSeq.Int64))
	}
	require.Equal(t, want, distinctCursors(bus.cursors()), "backfilled events are published in event_seq order before any streamed event")

	var relayState int64
	require.NoError(t, store.db.Raw("SELECT count(*) FROM schema_metadata WHERE key IN (?, ?)", relaySlotBackfillKey, relayBackfillPublishKey).Row().Scan(&relayState))
	require.Zero(t, relayState, "a finished backfill leaves no relay state behind")
}

// A COMMIT that never completes (a stalled proxy, or synchronous replication
// that stops acknowledging) must not hold up the leader: on first startup
// nothing else would cancel it, so it would block process startup and keep the
// leader lock. The backfill's COMMIT must be bounded by the call timeout.
func TestPostgresOutboxRelayBoundsBackfillCommit(t *testing.T) {
	store, ctx := setupPostgresOutboxStore(t)
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)

	before := appendMembershipOutboxRow(t, store, ctx, "user-1", time.Now())

	// A deferred constraint trigger runs at COMMIT, so this stalls only the
	// COMMIT of the transaction that records the backfill publish range.
	require.NoError(t, store.db.Exec(`
		CREATE FUNCTION stall_commit() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN PERFORM pg_sleep(60); RETURN NULL; END $$`).Error)
	require.NoError(t, store.db.Exec(`
		CREATE CONSTRAINT TRIGGER stall_backfill_commit
		AFTER INSERT OR UPDATE ON schema_metadata
		DEFERRABLE INITIALLY DEFERRED
		FOR EACH ROW WHEN (NEW.key = 'outbox_relay_backfill_publish')
		EXECUTE FUNCTION stall_commit()`).Error)
	t.Cleanup(func() {
		_ = store.db.Exec("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE wait_event = 'PgSleep'").Error
	})

	relay := newPostgresOutboxRelay(store, &recordingRelayBus{})
	relay.callTimeout = 2 * time.Second
	lockConn, acquired, err := relay.tryAcquireLeaderLock(ctx)
	require.NoError(t, err)
	require.True(t, acquired)
	ready := make(chan error, 1)
	go relay.runLeader(ctx, lockConn, ready)

	select {
	case err := <-ready:
		require.Error(t, err, "a backfill that cannot commit must fail leader startup")
	case <-time.After(15 * time.Second):
		t.Fatal("leader startup is still blocked on the backfill COMMIT")
	}
	require.False(t, outboxEventSeq(t, store, before).Valid)
	var pending int64
	require.NoError(t, store.db.Raw("SELECT count(*) FROM schema_metadata WHERE key = ?", relaySlotBackfillKey).Row().Scan(&pending))
	require.Equal(t, int64(1), pending, "the next leader must still see the backfill as unfinished")

	otherDB, err := sql.Open("pgx", store.cfg.DBURL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = otherDB.Close() })
	require.Eventually(t, func() bool { return tryOutboxLeaderLock(t, otherDB) }, 5*time.Second, 50*time.Millisecond,
		"the failed leader must release its lock so another replica can take over")
}
