//go:build !nopostgresql

package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	"github.com/chirino/memory-service/internal/security"
	"github.com/chirino/memory-service/internal/service/eventing"
	"github.com/google/uuid"
	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
)

const (
	postgresOutboxPublicationName = "memory_service_outbox_pub"
	postgresOutboxSlotName        = "memory_service_outbox_slot"
	postgresOutboxLockClass       = int64(0x6d656d6f)
	postgresOutboxLockKey         = int64(0x6f757462)
	relayRetryDelay               = time.Second
	relayStatusInterval           = 10 * time.Second
	// relayCallTimeout bounds each database and event-bus call the relay makes,
	// so a call that never returns cannot stall the relay while it keeps its
	// leader lock.
	relayCallTimeout = 30 * time.Second
	// relayWatchdogInterval is how often every process samples unsequenced
	// outbox rows for the stall metrics and watchdog.
	relayWatchdogInterval = 15 * time.Second
)

// errRelayStalled is the cancellation cause the watchdog uses to restart a
// leader that stopped assigning event_seq values.
var errRelayStalled = errors.New("postgres outbox relay stalled")

type relayOutboxRecord struct {
	TxSeq     int64
	Event     string
	Kind      string
	Data      json.RawMessage
	CreatedAt time.Time
}

type materializedOutboxRecord struct {
	Ord      int   `gorm:"column:ord"`
	TxSeq    int64 `gorm:"column:tx_seq"`
	EventSeq int64 `gorm:"column:event_seq"`
}

type postgresOutboxRelay struct {
	store            *PostgresStore
	bus              registryeventbus.EventBus
	leaderRunning    atomic.Bool
	callTimeout      time.Duration
	stallTimeout     time.Duration
	watchdogInterval time.Duration
	statusInterval   time.Duration

	// progress counts successful relay steps (event_seq updates and
	// publishes), so the watchdog can tell a slow batch from a stalled one.
	progress atomic.Uint64

	mu            sync.Mutex
	leaderCancel  context.CancelCauseFunc
	leaderStarted time.Time
}

func (s *PostgresStore) RelayPublishesOutboxEvents() bool {
	return s != nil && s.OutboxEnabled()
}

func (s *PostgresStore) StartOutboxRelay(ctx context.Context, bus registryeventbus.EventBus) error {
	if s == nil || !s.OutboxEnabled() || bus == nil {
		return nil
	}

	relay := newPostgresOutboxRelay(s, bus)
	lockConn, acquired, err := relay.tryAcquireLeaderLock(ctx)
	if err != nil {
		return err
	}
	if acquired {
		ready := make(chan error, 1)
		go relay.runLeader(ctx, lockConn, ready)
		if err := <-ready; err != nil {
			return err
		}
	}
	go relay.candidateLoop(ctx)
	go relay.watchdogLoop(ctx)
	return nil
}

func newPostgresOutboxRelay(s *PostgresStore, bus registryeventbus.EventBus) *postgresOutboxRelay {
	relay := &postgresOutboxRelay{
		store:            s,
		bus:              bus,
		callTimeout:      relayCallTimeout,
		watchdogInterval: relayWatchdogInterval,
		statusInterval:   relayStatusInterval,
	}
	if s.cfg != nil {
		relay.stallTimeout = s.cfg.OutboxRelayStallTimeout
	}
	return relay
}

func (r *postgresOutboxRelay) candidateLoop(ctx context.Context) {
	ticker := time.NewTicker(relayRetryDelay)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		if r.leaderRunning.Load() {
			continue
		}
		lockConn, acquired, err := r.tryAcquireLeaderLock(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			// The candidate loop retries every second, so expected outages
			// would flood WARN; they stay visible at DEBUG.
			if relayConnectionLost(err) {
				log.Debug("Postgres outbox relay leadership check failed", "err", err)
			} else {
				log.Warn("Postgres outbox relay leadership check failed", "err", err)
			}
			continue
		}
		if !acquired {
			continue
		}
		go r.runLeader(ctx, lockConn, nil)
	}
}

func (r *postgresOutboxRelay) tryAcquireLeaderLock(ctx context.Context) (*sql.Conn, bool, error) {
	sqlDB, err := r.store.db.DB()
	if err != nil {
		return nil, false, fmt.Errorf("postgres outbox relay db handle: %w", err)
	}
	callCtx, cancel := r.callContext(ctx)
	defer cancel()
	conn, err := sqlDB.Conn(callCtx)
	if err != nil {
		return nil, false, fmt.Errorf("postgres outbox relay lock conn: %w", err)
	}

	var acquired bool
	if err := conn.QueryRowContext(callCtx, "SELECT pg_try_advisory_lock($1, $2)", postgresOutboxLockClass, postgresOutboxLockKey).Scan(&acquired); err != nil {
		_ = conn.Close()
		return nil, false, fmt.Errorf("postgres outbox relay advisory lock: %w", err)
	}
	if !acquired {
		_ = conn.Close()
		return nil, false, nil
	}
	return conn, true, nil
}

// releaseLeaderLock unlocks the session-level advisory lock before the
// connection goes back to the pool. Closing a *sql.Conn only returns it to the
// pool, so without the unlock the lock stays held and other replicas cannot
// take over. If the unlock fails, the connection is discarded instead, which
// ends the session and its locks.
func (r *postgresOutboxRelay) releaseLeaderLock(lockConn *sql.Conn, shuttingDown bool) {
	if lockConn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.callTimeout)
	defer cancel()
	var released bool
	err := lockConn.QueryRowContext(ctx, "SELECT pg_advisory_unlock($1, $2)", postgresOutboxLockClass, postgresOutboxLockKey).Scan(&released)
	if err != nil || !released {
		if err == nil {
			err = errors.New("lock was not held by the session")
		}
		// During shutdown the pool may already be closed; the session ends
		// with the process anyway.
		logf := log.Warn
		if shuttingDown {
			logf = log.Debug
		}
		logf("Postgres outbox relay could not release leader lock; discarding its connection", "err", err)
		// Returning driver.ErrBadConn from Raw makes database/sql close the
		// underlying connection instead of pooling it.
		_ = lockConn.Raw(func(any) error { return driver.ErrBadConn })
	}
	_ = lockConn.Close()
}

func (r *postgresOutboxRelay) callContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if r.callTimeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, r.callTimeout)
}

func (r *postgresOutboxRelay) runLeader(parent context.Context, lockConn *sql.Conn, ready chan<- error) {
	ctx, cancel := context.WithCancelCause(parent)
	r.mu.Lock()
	r.leaderCancel = cancel
	r.leaderStarted = time.Now()
	r.mu.Unlock()
	r.leaderRunning.Store(true)
	setRelayLeaderMetric(true)
	defer func() {
		r.mu.Lock()
		r.leaderCancel = nil
		r.mu.Unlock()
		cancel(nil)
		r.releaseLeaderLock(lockConn, parent.Err() != nil)
		setRelayLeaderMetric(false)
		r.leaderRunning.Store(false)
	}()

	started := false
	err := r.lead(ctx, func() {
		if started {
			return
		}
		started = true
		if ready != nil {
			ready <- nil
		}
	})
	if !started && ready != nil {
		// Startup failures go back to StartOutboxRelay, which fails startup
		// with the error.
		ready <- err
		return
	}
	logRelayLeaderExit(parent, ctx, err)
}

// logRelayLeaderExit records every leader exit, so a relay can never stop
// silently. Connection losses are logged too: the leader only exits on them
// when its replication stream is gone.
func logRelayLeaderExit(parent, leaderCtx context.Context, err error) {
	switch {
	case parent.Err() != nil:
		countRelayLeaderExit("shutdown")
		log.Info("Postgres outbox relay leader stopped", "reason", "shutdown")
	case errors.Is(context.Cause(leaderCtx), errRelayStalled):
		countRelayLeaderExit("stalled")
		log.Warn("Postgres outbox relay leader stopped", "reason", "stalled", "err", err)
	default:
		countRelayLeaderExit("error")
		log.Warn("Postgres outbox relay leader stopped", "reason", "error", "err", err)
	}
}

// lead runs the relay until it fails or ctx ends. It always returns a non-nil
// error. started is called once replication has started, or earlier when
// backfilled events still need publishing, so an event bus outage cannot fail
// process startup. It may be called more than once.
func (r *postgresOutboxRelay) lead(ctx context.Context, started func()) error {
	if err := r.ensurePublication(ctx); err != nil {
		return err
	}

	replConn, startLSN, err := r.ensureReplicationSlot(ctx)
	if err != nil {
		return err
	}
	defer func() {
		// Close with context.Background() would wait on a blocked socket
		// forever; a bounded context lets pgconn interrupt it.
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = replConn.Close(closeCtx)
	}()

	// Clients resume after their last cursor, so backfilled events must all be
	// published before any newer streamed event.
	if err := r.publishBackfill(ctx, started); err != nil {
		return err
	}

	startCtx, cancel := r.callContext(ctx)
	err = pglogrepl.StartReplication(startCtx, replConn, postgresOutboxSlotName, startLSN, pglogrepl.StartReplicationOptions{
		PluginArgs: []string{
			"proto_version '1'",
			fmt.Sprintf("publication_names '%s'", postgresOutboxPublicationName),
		},
	})
	cancel()
	if err != nil {
		return fmt.Errorf("postgres outbox relay start replication: %w", err)
	}

	started()
	log.Info("Postgres outbox relay active", "slot", postgresOutboxSlotName, "publication", postgresOutboxPublicationName, "lsn", startLSN.String())
	lastEventSeq, err := r.loadLastEventSeq(ctx)
	if err != nil {
		return err
	}

	relations := map[uint32]*pglogrepl.RelationMessage{}
	pending := make([]relayOutboxRecord, 0, 8)
	processedLSN := startLSN
	inTransaction := false
	nextStatusAt := time.Now().Add(r.statusInterval)

	for {
		if time.Now().After(nextStatusAt) {
			if err := r.sendStandbyStatus(replConn, processedLSN); err != nil {
				return fmt.Errorf("postgres outbox relay standby status: %w", err)
			}
			nextStatusAt = time.Now().Add(r.statusInterval)
		}

		readCtx, cancel := context.WithDeadline(ctx, nextStatusAt)
		rawMsg, err := replConn.ReceiveMessage(readCtx)
		cancel()
		if err != nil {
			if ctx.Err() == nil && pgconn.Timeout(err) {
				continue
			}
			return fmt.Errorf("postgres outbox relay receive: %w", err)
		}

		var copyData *pgproto3.CopyData
		switch msg := rawMsg.(type) {
		case *pgproto3.CopyData:
			copyData = msg
		case *pgproto3.NoticeResponse:
			// Asynchronous messages the server may send at any time; they do
			// not end the stream.
			log.Warn("Postgres outbox relay server notice", "severity", msg.Severity, "message", msg.Message)
			continue
		case *pgproto3.ParameterStatus, *pgproto3.NotificationResponse:
			continue
		case *pgproto3.ErrorResponse:
			return fmt.Errorf("postgres outbox relay server error: severity=%s message=%s detail=%s", msg.Severity, msg.Message, msg.Detail)
		default:
			// CopyDone, CommandComplete, ReadyForQuery, and anything else mean
			// the server ended replication. Exit so the relay restarts and
			// resumes from the slot instead of waiting on a dead stream.
			return fmt.Errorf("postgres outbox relay replication stream ended: received %T", rawMsg)
		}
		if len(copyData.Data) == 0 {
			return errors.New("postgres outbox relay received empty replication message")
		}

		switch copyData.Data[0] {
		case pglogrepl.PrimaryKeepaliveMessageByteID:
			keepalive, err := pglogrepl.ParsePrimaryKeepaliveMessage(copyData.Data[1:])
			if err != nil {
				return fmt.Errorf("postgres outbox relay keepalive parse: %w", err)
			}
			// Confirm the server's position between transactions, not only
			// after transactions that write outbox rows. Otherwise the slot
			// retains all WAL written since the last outbox write. The server
			// sends every change before that position ahead of the
			// keepalive, so once no transaction is open, everything before it
			// has been handled. Transactions still running on the server
			// commit after it and are streamed later.
			if !inTransaction && keepalive.ServerWALEnd > processedLSN {
				processedLSN = keepalive.ServerWALEnd
			}
			if keepalive.ReplyRequested {
				nextStatusAt = time.Time{}
			}
		case pglogrepl.XLogDataByteID:
			xld, err := pglogrepl.ParseXLogData(copyData.Data[1:])
			if err != nil {
				return fmt.Errorf("postgres outbox relay xlog parse: %w", err)
			}
			msg, err := pglogrepl.Parse(xld.WALData)
			if err != nil {
				return fmt.Errorf("postgres outbox relay logical message parse: %w", err)
			}
			switch logicalMsg := msg.(type) {
			case *pglogrepl.RelationMessage:
				relations[logicalMsg.RelationID] = logicalMsg
			case *pglogrepl.BeginMessage:
				inTransaction = true
				pending = pending[:0]
			case *pglogrepl.InsertMessage:
				rel, ok := relations[logicalMsg.RelationID]
				if !ok {
					return fmt.Errorf("postgres outbox relay insert missing relation %d", logicalMsg.RelationID)
				}
				record, match, err := decodeOutboxInsert(rel, logicalMsg)
				if err != nil {
					return fmt.Errorf("postgres outbox relay insert decode: %w", err)
				}
				if match {
					pending = append(pending, record)
				}
			case *pglogrepl.CommitMessage:
				var err error
				lastEventSeq, err = r.materializeAndPublishBatch(ctx, lastEventSeq, pending)
				if err != nil {
					return fmt.Errorf("postgres outbox relay publish failed at commit %s: %w", logicalMsg.CommitLSN.String(), err)
				}
				pending = pending[:0]
				inTransaction = false
				if logicalMsg.TransactionEndLSN > processedLSN {
					processedLSN = logicalMsg.TransactionEndLSN
				}
				setRelayLastCommitMetric(time.Now())
			}
		}
	}
}

// sendStandbyStatus reports the processed position to the slot.
// pglogrepl.SendStandbyStatusUpdate ignores its context and writes without a
// deadline, so a socket that stops draining would block it forever. A write
// deadline bounds it instead.
func (r *postgresOutboxRelay) sendStandbyStatus(replConn *pgconn.PgConn, processedLSN pglogrepl.LSN) error {
	if r.callTimeout > 0 {
		if err := replConn.Conn().SetWriteDeadline(time.Now().Add(r.callTimeout)); err != nil {
			return fmt.Errorf("postgres outbox relay standby status deadline: %w", err)
		}
		defer func() { _ = replConn.Conn().SetWriteDeadline(time.Time{}) }()
	}
	return pglogrepl.SendStandbyStatusUpdate(context.Background(), replConn, pglogrepl.StandbyStatusUpdate{WALWritePosition: processedLSN})
}

// restartLeader cancels this process's running leader with cause. The
// candidate loop then takes leadership again and resumes from the slot.
func (r *postgresOutboxRelay) restartLeader(cause error) bool {
	r.mu.Lock()
	cancel := r.leaderCancel
	r.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel(cause)
	return true
}

func (r *postgresOutboxRelay) leaderUptime(now time.Time) (time.Duration, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.leaderCancel == nil {
		return 0, false
	}
	return now.Sub(r.leaderStarted), true
}

type unsequencedOutboxStats struct {
	Count       int64         `gorm:"column:unsequenced"`
	OldestTxSeq sql.NullInt64 `gorm:"column:oldest_tx_seq"`
	OldestAge   float64       `gorm:"column:oldest_age_seconds"`
}

func (r *postgresOutboxRelay) loadUnsequencedStats(ctx context.Context) (unsequencedOutboxStats, error) {
	callCtx, cancel := r.callContext(ctx)
	defer cancel()
	var stats unsequencedOutboxStats
	// Served by idx_outbox_events_unsequenced, which only holds rows that
	// still need an event_seq.
	err := r.store.db.WithContext(callCtx).Raw(`
		WITH oldest AS (
			SELECT tx_seq, created_at
			FROM outbox_events
			WHERE event_seq IS NULL
			ORDER BY created_at ASC, tx_seq ASC
			LIMIT 1
		)
		SELECT
			(SELECT count(*) FROM outbox_events WHERE event_seq IS NULL) AS unsequenced,
			(SELECT tx_seq FROM oldest) AS oldest_tx_seq,
			COALESCE((SELECT EXTRACT(EPOCH FROM (now() - created_at)) FROM oldest), 0)::float8 AS oldest_age_seconds
	`).Scan(&stats).Error
	if err != nil {
		return unsequencedOutboxStats{}, fmt.Errorf("postgres outbox relay load unsequenced stats: %w", err)
	}
	return stats, nil
}

// watchdogLoop runs in every process. It exports the unsequenced-row metrics
// and, when this process is the leader, restarts a leader that has stopped
// assigning event_seq values.
func (r *postgresOutboxRelay) watchdogLoop(ctx context.Context) {
	if r.watchdogInterval <= 0 {
		return
	}
	ticker := time.NewTicker(r.watchdogInterval)
	defer ticker.Stop()

	watchdog := relayWatchdog{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		stats, err := r.loadUnsequencedStats(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if relayConnectionLost(err) {
				log.Debug("Postgres outbox relay watchdog check failed", "err", err)
			} else {
				log.Warn("Postgres outbox relay watchdog check failed", "err", err)
			}
			continue
		}
		setUnsequencedMetrics(stats)

		now := time.Now()
		uptime, leading := r.leaderUptime(now)
		if !watchdog.observe(stats, r.progress.Load(), leading, uptime, r.stallTimeout) {
			continue
		}
		if r.restartLeader(errRelayStalled) {
			log.Warn("Postgres outbox relay stalled; restarting leader",
				"unsequenced", stats.Count,
				"oldestTxSeq", stats.OldestTxSeq.Int64,
				"oldestAge", time.Duration(stats.OldestAge*float64(time.Second)).Round(time.Second),
				"stallTimeout", r.stallTimeout)
		}
	}
}

// relayWatchdog decides when a leader has stalled. Restarting needs all of:
//   - the oldest unsequenced row is older than the stall timeout;
//   - the same row was already the oldest at the previous check, so a long
//     transaction that just committed old rows does not trigger a restart;
//   - the leader neither numbered rows nor published events since the
//     previous check, so a relay working through a backlog or a large
//     transaction is left alone;
//   - the leader has run for at least the stall timeout, so a restarted
//     leader gets time to recover before the next restart.
type relayWatchdog struct {
	lastOldestTxSeq int64
	lastProgress    uint64
	hasLast         bool
}

func (w *relayWatchdog) observe(stats unsequencedOutboxStats, progress uint64, leading bool, uptime, stallTimeout time.Duration) bool {
	prevOldest, prevProgress, hadLast := w.lastOldestTxSeq, w.lastProgress, w.hasLast
	w.lastOldestTxSeq = 0
	if stats.OldestTxSeq.Valid {
		w.lastOldestTxSeq = stats.OldestTxSeq.Int64
	}
	w.lastProgress = progress
	w.hasLast = true

	if stallTimeout <= 0 || !leading || !hadLast || stats.Count == 0 || !stats.OldestTxSeq.Valid {
		return false
	}
	if uptime < stallTimeout {
		return false
	}
	if time.Duration(stats.OldestAge*float64(time.Second)) < stallTimeout {
		return false
	}
	return stats.OldestTxSeq.Int64 == prevOldest && progress == prevProgress
}

func setRelayLeaderMetric(leading bool) {
	if security.OutboxRelayLeader == nil {
		return
	}
	if leading {
		security.OutboxRelayLeader.Set(1)
	} else {
		security.OutboxRelayLeader.Set(0)
	}
}

func setRelayLastCommitMetric(at time.Time) {
	if security.OutboxRelayLastCommitTimestamp != nil {
		security.OutboxRelayLastCommitTimestamp.Set(float64(at.UnixNano()) / float64(time.Second))
	}
}

func countRelayLeaderExit(reason string) {
	if security.OutboxRelayLeaderExitsTotal != nil {
		security.OutboxRelayLeaderExitsTotal.WithLabelValues(reason).Inc()
	}
}

func setUnsequencedMetrics(stats unsequencedOutboxStats) {
	if security.OutboxUnsequencedEvents != nil {
		security.OutboxUnsequencedEvents.Set(float64(stats.Count))
	}
	if security.OutboxUnsequencedOldestAge != nil {
		security.OutboxUnsequencedOldestAge.Set(stats.OldestAge)
	}
}

func (r *postgresOutboxRelay) ensurePublication(ctx context.Context) error {
	ctx, cancel := r.callContext(ctx)
	defer cancel()
	sqlDB, err := r.store.db.DB()
	if err != nil {
		return fmt.Errorf("postgres outbox relay publication db handle: %w", err)
	}

	var exists bool
	if err := sqlDB.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = $1)", postgresOutboxPublicationName).Scan(&exists); err != nil {
		return fmt.Errorf("postgres outbox relay check publication: %w", err)
	}
	if exists {
		return nil
	}
	if _, err := sqlDB.ExecContext(ctx, fmt.Sprintf("CREATE PUBLICATION %s FOR TABLE outbox_events", postgresOutboxPublicationName)); err != nil {
		return fmt.Errorf("postgres outbox relay create publication: %w", err)
	}
	return nil
}

func (r *postgresOutboxRelay) ensureReplicationSlot(ctx context.Context) (*pgconn.PgConn, pglogrepl.LSN, error) {
	// The timeout covers setup only; pgconn uses the context for the call, not
	// for the connection's lifetime.
	setupCtx, cancel := r.callContext(ctx)
	defer cancel()
	replConn, err := connectReplication(setupCtx, r.store.cfg.DBURL)
	if err != nil {
		return nil, 0, explainOutboxRelaySetupError("connect replication", err)
	}
	fail := func(err error) (*pgconn.PgConn, pglogrepl.LSN, error) {
		replConn.Close(context.Background())
		return nil, 0, err
	}
	sqlDB, err := r.store.db.DB()
	if err != nil {
		return fail(fmt.Errorf("postgres outbox relay slot db handle: %w", err))
	}

	startLSN, exists, err := currentSlotLSN(setupCtx, r.store.db, postgresOutboxSlotName)
	if err != nil {
		return fail(err)
	}
	if exists {
		_, pending, err := r.readRelayState(ctx, sqlDB, relaySlotBackfillKey)
		if err != nil {
			return fail(err)
		}
		if pending {
			// The leader that created the slot stopped before numbering the
			// rows committed before it, and never streamed from it. A new slot
			// exports a snapshot that covers those rows too.
			log.Warn("Postgres outbox relay is recreating a replication slot whose backfill did not finish", "slot", postgresOutboxSlotName)
			if err := pglogrepl.DropReplicationSlot(setupCtx, replConn, postgresOutboxSlotName, pglogrepl.DropReplicationSlotOptions{}); err != nil {
				return fail(fmt.Errorf("postgres outbox relay drop unfinished replication slot: %w", err))
			}
			exists = false
		}
	}
	if !exists {
		// Commit the marker before the slot exists: the slot persists as soon
		// as it is created, so a leader that dies before numbering must leave
		// a record that the slot is incomplete.
		if err := r.writeRelayState(ctx, sqlDB, relaySlotBackfillKey, "pending"); err != nil {
			return fail(err)
		}
		// The exported snapshot shows exactly the transactions that committed
		// before the slot's consistent point. It stays valid until the next
		// command on replConn.
		result, err := pglogrepl.CreateReplicationSlot(setupCtx, replConn, postgresOutboxSlotName, "pgoutput", pglogrepl.CreateReplicationSlotOptions{SnapshotAction: "EXPORT_SNAPSHOT"})
		if err != nil {
			return fail(explainOutboxRelaySetupError("create replication slot", err))
		}
		startLSN, err = pglogrepl.ParseLSN(result.ConsistentPoint)
		if err != nil {
			return fail(fmt.Errorf("postgres outbox relay parse replication slot LSN %q: %w", result.ConsistentPoint, err))
		}
		if err := r.sequenceRowsBeforeSlot(ctx, result.SnapshotName); err != nil {
			return fail(err)
		}
	}
	return replConn, startLSN, nil
}

// relayBackfillChunkSize bounds the rows numbered or published per statement
// when handling rows committed before the slot existed.
const relayBackfillChunkSize = 500

// Relay state kept in schema_metadata.
const (
	// relaySlotBackfillKey is present while the replication slot, if it
	// exists, still needs the rows committed before it numbered.
	relaySlotBackfillKey = "outbox_relay_slot_backfill"
	// relayBackfillPublishKey holds "after:through", the event_seq range a
	// backfill numbered that is not fully published yet.
	relayBackfillPublishKey = "outbox_relay_backfill_publish"
)

var exportedSnapshotName = regexp.MustCompile(`^[0-9A-Fa-f-]+$`)

// sequenceRowsBeforeSlot numbers outbox rows committed before a newly created
// slot's consistent point. The slot never streams those rows, so without this
// they would never get an event_seq. This covers rows a replica wrote while
// another replica was still creating the slot, and, after a failover or
// restore that lost the slot, rows the old relay had not numbered plus rows
// written before the relay recreated the slot.
//
// One transaction numbers every row, clears the backfill-pending marker, and
// records the numbered range for publishBackfill, so a leader that stops
// partway leaves either the marker or the range behind, never neither.
func (r *postgresOutboxRelay) sequenceRowsBeforeSlot(ctx context.Context, snapshotName string) error {
	txSeqs, err := r.loadUnsequencedTxSeqsAtSnapshot(ctx, snapshotName)
	if err != nil {
		return err
	}
	sqlDB, err := r.store.db.DB()
	if err != nil {
		return fmt.Errorf("postgres outbox relay backfill db handle: %w", err)
	}
	// A large backlog can outlast one call timeout, so every statement,
	// including BEGIN and COMMIT, gets its own timeout. sql.Tx cannot do this:
	// pgx runs COMMIT under the BeginTx context, so a stalled COMMIT would
	// block first startup, before the watchdog runs.
	callCtx, cancel := r.callContext(ctx)
	conn, err := sqlDB.Conn(callCtx)
	cancel()
	if err != nil {
		return fmt.Errorf("postgres outbox relay backfill conn: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			r.rollbackAndClose(conn)
			return
		}
		_ = conn.Close()
	}()
	exec := func(query string) error {
		callCtx, cancel := r.callContext(ctx)
		defer cancel()
		_, err := conn.ExecContext(callCtx, query)
		return err
	}
	if err := exec("BEGIN"); err != nil {
		return fmt.Errorf("postgres outbox relay begin backfill transaction: %w", err)
	}

	var first int64
	callCtx, cancel = r.callContext(ctx)
	err = conn.QueryRowContext(callCtx, "SELECT COALESCE(MAX(event_seq), 0) FROM outbox_events").Scan(&first)
	cancel()
	if err != nil {
		return fmt.Errorf("postgres outbox relay load last event_seq: %w", err)
	}
	last := first
	var numbered int64
	for start := 0; start < len(txSeqs); start += relayBackfillChunkSize {
		end := min(start+relayBackfillChunkSize, len(txSeqs))
		var count int64
		callCtx, cancel := r.callContext(ctx)
		// Rows evicted meanwhile are skipped; the gap they leave is harmless
		// because only event_seq order matters.
		err := conn.QueryRowContext(callCtx, `
			WITH numbered AS (
				SELECT tx_seq, $2::bigint + row_number() OVER (ORDER BY tx_seq) AS event_seq
				FROM outbox_events
				WHERE tx_seq = ANY($1) AND event_seq IS NULL
			),
			updated AS (
				UPDATE outbox_events AS o
				SET event_seq = n.event_seq
				FROM numbered AS n
				WHERE o.tx_seq = n.tx_seq
				RETURNING o.event_seq
			)
			SELECT count(*), COALESCE(MAX(event_seq), $2::bigint) FROM updated
		`, txSeqs[start:end], last).Scan(&count, &last)
		cancel()
		if err != nil {
			return fmt.Errorf("postgres outbox relay number rows before slot: %w", err)
		}
		numbered += count
		r.progress.Add(1)
	}

	if err := r.deleteRelayState(ctx, conn, relaySlotBackfillKey); err != nil {
		return err
	}
	if last > first {
		after, through := first, last
		previous, ok, err := r.readRelayState(ctx, conn, relayBackfillPublishKey)
		if err != nil {
			return err
		}
		if ok {
			// An earlier backfill was not fully published; keep its start.
			// Events streamed in between are published again, which
			// at-least-once delivery allows.
			prevAfter, prevThrough, err := parseBackfillRange(previous)
			if err != nil {
				return err
			}
			after, through = min(after, prevAfter), max(through, prevThrough)
		}
		if err := r.writeRelayState(ctx, conn, relayBackfillPublishKey, formatBackfillRange(after, through)); err != nil {
			return err
		}
	}
	if err := exec("COMMIT"); err != nil {
		return fmt.Errorf("postgres outbox relay commit backfill: %w", err)
	}
	committed = true
	if numbered > 0 {
		log.Warn("Postgres outbox relay numbered events committed before its replication slot existed", "events", numbered)
	}
	return nil
}

// publishBackfill publishes the events a backfill numbered, in event_seq
// order. It runs before replication starts and returns on the first publish
// failure, so the next leader retries from the recorded position instead of
// streaming past the missed events. Progress is recorded per chunk, so some
// events may be published again, as on the stream path.
func (r *postgresOutboxRelay) publishBackfill(ctx context.Context, started func()) error {
	sqlDB, err := r.store.db.DB()
	if err != nil {
		return fmt.Errorf("postgres outbox relay backfill db handle: %w", err)
	}
	value, ok, err := r.readRelayState(ctx, sqlDB, relayBackfillPublishKey)
	if err != nil || !ok {
		return err
	}
	after, through, err := parseBackfillRange(value)
	if err != nil {
		return err
	}
	// The rows are already numbered and replay by cursor returns them, so an
	// event bus outage from here on must not fail process startup.
	started()
	for after < through {
		records, eventSeqs, err := r.loadBackfillRecords(ctx, after, through)
		if err != nil {
			return err
		}
		for i, record := range records {
			event, err := relayEvent(record, eventSeqs[i])
			if err == nil {
				err = r.publishRelayEvent(ctx, event)
			}
			if err != nil {
				return fmt.Errorf("postgres outbox relay publish backfilled event_seq %d: %w", eventSeqs[i], err)
			}
			r.progress.Add(1)
		}
		if len(records) < relayBackfillChunkSize {
			break
		}
		after = eventSeqs[len(eventSeqs)-1]
		if err := r.writeRelayState(ctx, sqlDB, relayBackfillPublishKey, formatBackfillRange(after, through)); err != nil {
			return err
		}
	}
	return r.deleteRelayState(ctx, sqlDB, relayBackfillPublishKey)
}

// loadBackfillRecords loads up to relayBackfillChunkSize rows with event_seq
// in (after, through], in event_seq order. Rows evicted meanwhile are absent.
func (r *postgresOutboxRelay) loadBackfillRecords(ctx context.Context, after, through int64) ([]relayOutboxRecord, []int64, error) {
	sqlDB, err := r.store.db.DB()
	if err != nil {
		return nil, nil, fmt.Errorf("postgres outbox relay db handle: %w", err)
	}
	ctx, cancel := r.callContext(ctx)
	defer cancel()
	rows, err := sqlDB.QueryContext(ctx, `
		SELECT tx_seq, event, kind, data, created_at, event_seq
		FROM outbox_events
		WHERE event_seq > $1 AND event_seq <= $2
		ORDER BY event_seq
		LIMIT $3
	`, after, through, relayBackfillChunkSize)
	if err != nil {
		return nil, nil, fmt.Errorf("postgres outbox relay load backfilled rows: %w", err)
	}
	defer rows.Close()
	var records []relayOutboxRecord
	var eventSeqs []int64
	for rows.Next() {
		var record relayOutboxRecord
		var data []byte
		var eventSeq int64
		if err := rows.Scan(&record.TxSeq, &record.Event, &record.Kind, &data, &record.CreatedAt, &eventSeq); err != nil {
			return nil, nil, fmt.Errorf("postgres outbox relay scan backfilled rows: %w", err)
		}
		record.Data = json.RawMessage(data)
		records = append(records, record)
		eventSeqs = append(eventSeqs, eventSeq)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("postgres outbox relay load backfilled rows: %w", err)
	}
	return records, eventSeqs, nil
}

// rollbackAndClose ends an explicit transaction on conn and returns conn to
// the pool. If the rollback fails, conn may still be inside the transaction,
// so it is discarded instead.
func (r *postgresOutboxRelay) rollbackAndClose(conn *sql.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
		// Returning driver.ErrBadConn from Raw makes database/sql close the
		// underlying connection instead of pooling it.
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	}
	_ = conn.Close()
}

// relaySQL is satisfied by *sql.DB and *sql.Conn.
type relaySQL interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (r *postgresOutboxRelay) readRelayState(ctx context.Context, q relaySQL, key string) (string, bool, error) {
	ctx, cancel := r.callContext(ctx)
	defer cancel()
	var value string
	err := q.QueryRowContext(ctx, "SELECT value FROM schema_metadata WHERE key = $1", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("postgres outbox relay read %s: %w", key, err)
	}
	return value, true, nil
}

func (r *postgresOutboxRelay) writeRelayState(ctx context.Context, q relaySQL, key, value string) error {
	ctx, cancel := r.callContext(ctx)
	defer cancel()
	if _, err := q.ExecContext(ctx, `
		INSERT INTO schema_metadata (key, value, updated_at) VALUES ($1, $2, NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()
	`, key, value); err != nil {
		return fmt.Errorf("postgres outbox relay write %s: %w", key, err)
	}
	return nil
}

func (r *postgresOutboxRelay) deleteRelayState(ctx context.Context, q relaySQL, key string) error {
	ctx, cancel := r.callContext(ctx)
	defer cancel()
	if _, err := q.ExecContext(ctx, "DELETE FROM schema_metadata WHERE key = $1", key); err != nil {
		return fmt.Errorf("postgres outbox relay delete %s: %w", key, err)
	}
	return nil
}

func formatBackfillRange(after, through int64) string {
	return strconv.FormatInt(after, 10) + ":" + strconv.FormatInt(through, 10)
}

func parseBackfillRange(value string) (after, through int64, err error) {
	afterText, throughText, ok := strings.Cut(value, ":")
	if ok {
		after, err = strconv.ParseInt(afterText, 10, 64)
		if err == nil {
			through, err = strconv.ParseInt(throughText, 10, 64)
		}
	}
	if !ok || err != nil {
		return 0, 0, fmt.Errorf("postgres outbox relay: invalid %s value %q", relayBackfillPublishKey, value)
	}
	return after, through, nil
}

func (r *postgresOutboxRelay) loadUnsequencedTxSeqsAtSnapshot(ctx context.Context, snapshotName string) ([]int64, error) {
	if !exportedSnapshotName.MatchString(snapshotName) {
		return nil, fmt.Errorf("postgres outbox relay: unexpected exported snapshot name %q", snapshotName)
	}
	sqlDB, err := r.store.db.DB()
	if err != nil {
		return nil, fmt.Errorf("postgres outbox relay snapshot db handle: %w", err)
	}
	ctx, cancel := r.callContext(ctx)
	defer cancel()
	tx, err := sqlDB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("postgres outbox relay begin snapshot transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	// The snapshot name cannot be a bind parameter; it was validated above.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("SET TRANSACTION SNAPSHOT '%s'", snapshotName)); err != nil {
		return nil, fmt.Errorf("postgres outbox relay import slot snapshot: %w", err)
	}
	rows, err := tx.QueryContext(ctx, "SELECT tx_seq FROM outbox_events WHERE event_seq IS NULL ORDER BY tx_seq")
	if err != nil {
		return nil, fmt.Errorf("postgres outbox relay list rows before slot: %w", err)
	}
	defer rows.Close()
	var txSeqs []int64
	for rows.Next() {
		var txSeq int64
		if err := rows.Scan(&txSeq); err != nil {
			return nil, fmt.Errorf("postgres outbox relay scan rows before slot: %w", err)
		}
		txSeqs = append(txSeqs, txSeq)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres outbox relay list rows before slot: %w", err)
	}
	return txSeqs, nil
}

func connectReplication(ctx context.Context, dbURL string) (*pgconn.PgConn, error) {
	cfg, err := pgconn.ParseConfig(dbURL)
	if err != nil {
		return nil, fmt.Errorf("postgres outbox relay parse replication config: %w", err)
	}
	if cfg.RuntimeParams == nil {
		cfg.RuntimeParams = map[string]string{}
	}
	cfg.RuntimeParams["replication"] = "database"
	cfg.RuntimeParams["application_name"] = "memory-service-outbox-relay"
	conn, err := pgconn.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres outbox relay connect replication: %w", err)
	}
	return conn, nil
}

func currentSlotLSN(ctx context.Context, db sqlDBProvider, slotName string) (pglogrepl.LSN, bool, error) {
	sqlDB, err := db.DB()
	if err != nil {
		return 0, false, fmt.Errorf("postgres outbox relay slot db handle: %w", err)
	}
	var confirmed sql.NullString
	var restart sql.NullString
	err = sqlDB.QueryRowContext(ctx, `
		SELECT confirmed_flush_lsn::text, restart_lsn::text
		FROM pg_replication_slots
		WHERE slot_name = $1
	`, slotName).Scan(&confirmed, &restart)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("postgres outbox relay query slot: %w", err)
	}
	lsnText := confirmed.String
	if !confirmed.Valid || strings.TrimSpace(lsnText) == "" {
		lsnText = restart.String
	}
	if strings.TrimSpace(lsnText) == "" {
		return 0, true, nil
	}
	lsn, err := pglogrepl.ParseLSN(lsnText)
	if err != nil {
		return 0, true, fmt.Errorf("postgres outbox relay parse slot lsn %q: %w", lsnText, err)
	}
	return lsn, true, nil
}

func decodeOutboxInsert(rel *pglogrepl.RelationMessage, insert *pglogrepl.InsertMessage) (relayOutboxRecord, bool, error) {
	if rel.Namespace != "public" || rel.RelationName != "outbox_events" {
		return relayOutboxRecord{}, false, nil
	}
	var record relayOutboxRecord
	for idx, col := range insert.Tuple.Columns {
		if idx >= len(rel.Columns) {
			return relayOutboxRecord{}, false, fmt.Errorf("unexpected tuple column count for relation %s", rel.RelationName)
		}
		name := rel.Columns[idx].Name
		switch col.DataType {
		case 'n':
			continue
		case 't':
		default:
			return relayOutboxRecord{}, false, fmt.Errorf("unsupported tuple data type %q for column %s", col.DataType, name)
		}

		switch name {
		case "tx_seq":
			txSeq, err := strconv.ParseInt(string(col.Data), 10, 64)
			if err != nil {
				return relayOutboxRecord{}, false, fmt.Errorf("parse tx_seq %q: %w", string(col.Data), err)
			}
			record.TxSeq = txSeq
		case "event":
			record.Event = string(col.Data)
		case "kind":
			record.Kind = string(col.Data)
		case "data":
			record.Data = append(record.Data[:0], col.Data...)
		case "created_at":
			var err error
			record.CreatedAt, err = parseRelayTimestamp(string(col.Data))
			if err != nil {
				return relayOutboxRecord{}, false, fmt.Errorf("parse created_at %q: %w", string(col.Data), err)
			}
		}
	}
	if record.TxSeq == 0 || record.Event == "" || record.Kind == "" || len(record.Data) == 0 {
		return relayOutboxRecord{}, false, fmt.Errorf("outbox insert missing required fields: tx_seq=%d event=%q kind=%q data=%d", record.TxSeq, record.Event, record.Kind, len(record.Data))
	}
	return record, true, nil
}

func (r *postgresOutboxRelay) loadLastEventSeq(ctx context.Context) (int64, error) {
	ctx, cancel := r.callContext(ctx)
	defer cancel()
	var lastEventSeq sql.NullInt64
	row := r.store.db.WithContext(ctx).Raw("SELECT COALESCE(MAX(event_seq), 0) FROM outbox_events").Row()
	if err := row.Scan(&lastEventSeq); err != nil {
		return 0, fmt.Errorf("load last event_seq: %w", err)
	}
	return lastEventSeq.Int64, nil
}

func (r *postgresOutboxRelay) materializeAndPublishBatch(ctx context.Context, lastEventSeq int64, pending []relayOutboxRecord) (int64, error) {
	if len(pending) == 0 {
		return lastEventSeq, nil
	}

	materialized, err := r.materializeEventSeqBatch(ctx, lastEventSeq, pending)
	if err != nil {
		return lastEventSeq, err
	}
	r.progress.Add(1)

	nextEventSeq := lastEventSeq + int64(len(pending))
	for i, row := range materialized {
		if row.EventSeq > nextEventSeq {
			nextEventSeq = row.EventSeq
		}
		record := pending[i]
		if row.TxSeq != record.TxSeq {
			return lastEventSeq, fmt.Errorf("materialized outbox row order mismatch at index %d: got tx_seq %d, expected %d", i, row.TxSeq, record.TxSeq)
		}
		event, err := relayEvent(record, row.EventSeq)
		if err != nil {
			return lastEventSeq, err
		}
		if err := r.publishRelayEvent(ctx, event); err != nil {
			return lastEventSeq, err
		}
		r.progress.Add(1)
	}

	return nextEventSeq, nil
}

func (r *postgresOutboxRelay) publishRelayEvent(ctx context.Context, event registryeventbus.Event) error {
	ctx, cancel := r.callContext(ctx)
	defer cancel()
	bus := durablePostgresRelayBus{EventBus: r.bus}
	switch {
	case len(event.UserIDs) > 0:
		return eventing.PublishToUsers(ctx, bus, event.UserIDs, event)
	case event.ConversationGroupID != uuid.Nil:
		return eventing.PublishToGroup(ctx, r.store, bus, event.ConversationGroupID, event)
	default:
		return bus.Publish(ctx, event)
	}
}

// durablePostgresRelayBus routes every relay branch through the transport's
// acknowledged publication path when one is available. Local-only buses keep
// their existing synchronous Publish behavior.
type durablePostgresRelayBus struct{ registryeventbus.EventBus }

func (b durablePostgresRelayBus) Publish(ctx context.Context, event registryeventbus.Event) error {
	if durable, ok := b.EventBus.(registryeventbus.DurablePublisher); ok {
		return durable.PublishDurable(ctx, event)
	}
	return b.EventBus.Publish(ctx, event)
}

func (r *postgresOutboxRelay) materializeEventSeqBatch(ctx context.Context, lastEventSeq int64, pending []relayOutboxRecord) ([]materializedOutboxRecord, error) {
	if len(pending) == 0 {
		return nil, nil
	}

	values := make([]string, 0, len(pending))
	args := make([]any, 0, len(pending)*3)
	for i, record := range pending {
		values = append(values, "(?::integer, ?::bigint, ?::bigint)")
		args = append(args, i, record.TxSeq, lastEventSeq+int64(i)+1)
	}

	query := fmt.Sprintf(`
WITH candidates(ord, tx_seq, candidate_event_seq) AS (
	VALUES %s
),
updated AS (
	UPDATE outbox_events AS o
	SET event_seq = c.candidate_event_seq
	FROM candidates AS c
	WHERE o.tx_seq = c.tx_seq
	  AND o.event_seq IS NULL
	RETURNING o.tx_seq, o.event_seq
)
SELECT c.ord, c.tx_seq, COALESCE(u.event_seq, o.event_seq) AS event_seq
FROM candidates AS c
LEFT JOIN updated AS u ON u.tx_seq = c.tx_seq
LEFT JOIN outbox_events AS o ON o.tx_seq = c.tx_seq AND u.tx_seq IS NULL
ORDER BY c.ord ASC
`, strings.Join(values, ","))

	ctx, cancel := r.callContext(ctx)
	defer cancel()
	var rows []materializedOutboxRecord
	if err := r.store.db.WithContext(ctx).Raw(query, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("materialize event_seq batch: %w", err)
	}
	if len(rows) != len(pending) {
		return nil, fmt.Errorf("materialize event_seq batch: expected %d rows, got %d", len(pending), len(rows))
	}
	for i, row := range rows {
		if row.EventSeq <= 0 {
			return nil, fmt.Errorf("materialize event_seq batch: tx_seq %d has invalid event_seq %d", row.TxSeq, row.EventSeq)
		}
		if row.Ord != i {
			return nil, fmt.Errorf("materialize event_seq batch: unexpected ord %d at index %d", row.Ord, i)
		}
	}
	return rows, nil
}

func relayEvent(record relayOutboxRecord, eventSeq int64) (registryeventbus.Event, error) {
	var payload map[string]any
	if err := json.Unmarshal(record.Data, &payload); err != nil {
		return registryeventbus.Event{}, fmt.Errorf("decode outbox payload for tx_seq %d: %w", record.TxSeq, err)
	}

	event := registryeventbus.Event{
		Event:        record.Event,
		Kind:         record.Kind,
		Data:         payload,
		OutboxCursor: formatPostgresOutboxCursor(eventSeq),
		OccurredAt:   timePtr(record.CreatedAt.UTC()),
	}

	if groupID, ok := uuidFromPayload(payload["conversation_group"]); ok {
		event.ConversationGroupID = groupID
	}

	switch record.Kind {
	case "membership":
		if userID, ok := stringFromPayload(payload["user"]); ok {
			event.UserIDs = []string{userID}
		}
	case "conversation":
		if record.Event == "deleted" {
			event.UserIDs = stringsFromPayload(payload["members"])
		}
	}

	return event, nil
}

func parseRelayTimestamp(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999-07"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported PostgreSQL timestamp")
}

func timePtr(value time.Time) *time.Time { return &value }

func uuidFromPayload(value any) (uuid.UUID, bool) {
	raw, ok := stringFromPayload(value)
	if !ok {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

func stringFromPayload(value any) (string, bool) {
	raw, ok := value.(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return "", false
	}
	return raw, true
}

func stringsFromPayload(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if userID, ok := stringFromPayload(item); ok {
			out = append(out, userID)
		}
	}
	return out
}

type sqlDBProvider interface {
	DB() (*sql.DB, error)
}

// relayConnectionLost reports errors caused by an unreachable or dropped
// database connection. Retry loops log these at DEBUG to avoid flooding the log
// during an outage. Leader exits are always logged, whatever the error.
func relayConnectionLost(err error) bool {
	if err == nil {
		return false
	}
	// admin_shutdown, crash_shutdown, and cannot_connect_now: what clients see
	// while PostgreSQL restarts or fails over.
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "57P01" || pgErr.Code == "57P02" || pgErr.Code == "57P03") {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "connection refused") ||
		strings.Contains(message, "unexpected eof") ||
		strings.Contains(message, "broken pipe") ||
		strings.Contains(message, "use of closed network connection")
}

func explainOutboxRelaySetupError(operation string, err error) error {
	if err == nil {
		return nil
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		message := strings.ToLower(pgErr.Message)
		switch {
		case pgErr.Code == "55000" && strings.Contains(message, `logical decoding requires "wal_level" >= "logical"`):
			return fmt.Errorf(
				"postgres outbox relay %s: PostgreSQL is not configured for logical replication; set wal_level=logical, max_replication_slots >= 1, and max_wal_senders >= 1, then restart PostgreSQL, or disable MEMORY_SERVICE_OUTBOX_ENABLED if durable replay is not needed: %w",
				operation,
				err,
			)
		case pgErr.Code == "53400" && strings.Contains(message, "all replication slots are in use"):
			return fmt.Errorf(
				"postgres outbox relay %s: PostgreSQL has no free replication slots; increase max_replication_slots and restart PostgreSQL, or disable MEMORY_SERVICE_OUTBOX_ENABLED if durable replay is not needed: %w",
				operation,
				err,
			)
		case pgErr.Code == "53300" && strings.Contains(message, "number of requested standby connections exceeds max_wal_senders"):
			return fmt.Errorf(
				"postgres outbox relay %s: PostgreSQL has no free WAL sender capacity; increase max_wal_senders and restart PostgreSQL, or disable MEMORY_SERVICE_OUTBOX_ENABLED if durable replay is not needed: %w",
				operation,
				err,
			)
		}
	}

	return fmt.Errorf("postgres outbox relay %s: %w", operation, err)
}
