//go:build !nopostgresql

package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
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
)

type relayOutboxRecord struct {
	TxSeq int64
	Event string
	Kind  string
	Data  json.RawMessage
}

type materializedOutboxRecord struct {
	Ord      int   `gorm:"column:ord"`
	TxSeq    int64 `gorm:"column:tx_seq"`
	EventSeq int64 `gorm:"column:event_seq"`
}

type postgresOutboxRelay struct {
	store         *PostgresStore
	bus           registryeventbus.EventBus
	leaderRunning atomic.Bool
}

func (s *PostgresStore) RelayPublishesOutboxEvents() bool {
	return s != nil && s.OutboxEnabled()
}

func (s *PostgresStore) StartOutboxRelay(ctx context.Context, bus registryeventbus.EventBus) error {
	if s == nil || !s.OutboxEnabled() || bus == nil {
		return nil
	}

	relay := &postgresOutboxRelay{store: s, bus: bus}
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
	return nil
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
			if relayStopping(ctx, err) {
				return
			}
			log.Warn("Postgres outbox relay leadership check failed", "err", err)
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
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("postgres outbox relay lock conn: %w", err)
	}

	var acquired bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1, $2)", postgresOutboxLockClass, postgresOutboxLockKey).Scan(&acquired); err != nil {
		_ = conn.Close()
		return nil, false, fmt.Errorf("postgres outbox relay advisory lock: %w", err)
	}
	if !acquired {
		_ = conn.Close()
		return nil, false, nil
	}
	return conn, true, nil
}

func (r *postgresOutboxRelay) runLeader(ctx context.Context, lockConn *sql.Conn, ready chan<- error) {
	r.leaderRunning.Store(true)
	defer r.leaderRunning.Store(false)
	defer func() {
		if lockConn != nil {
			_ = lockConn.Close()
		}
	}()

	if err := r.ensurePublication(ctx); err != nil {
		if ready != nil {
			ready <- err
		}
		if ready == nil && !relayStopping(ctx, err) {
			log.Warn("Postgres outbox relay stopped", "err", err)
		}
		return
	}

	replConn, startLSN, err := r.ensureReplicationSlot(ctx)
	if err != nil {
		if ready != nil {
			ready <- err
		}
		if ready == nil && !relayStopping(ctx, err) {
			log.Warn("Postgres outbox relay stopped", "err", err)
		}
		return
	}
	defer replConn.Close(context.Background())

	if err := pglogrepl.StartReplication(ctx, replConn, postgresOutboxSlotName, startLSN, pglogrepl.StartReplicationOptions{
		PluginArgs: []string{
			"proto_version '1'",
			fmt.Sprintf("publication_names '%s'", postgresOutboxPublicationName),
		},
	}); err != nil {
		err = fmt.Errorf("postgres outbox relay start replication: %w", err)
		if ready != nil {
			ready <- err
		}
		if ready == nil && !relayStopping(ctx, err) {
			log.Warn("Postgres outbox relay stopped", "err", err)
		}
		return
	}

	if ready != nil {
		ready <- nil
	}
	log.Info("Postgres outbox relay active", "slot", postgresOutboxSlotName, "publication", postgresOutboxPublicationName, "lsn", startLSN.String())
	lastEventSeq, err := r.loadLastEventSeq(ctx)
	if err != nil {
		if !relayStopping(ctx, err) {
			log.Warn("Postgres outbox relay stopped", "err", err)
		}
		return
	}

	relations := map[uint32]*pglogrepl.RelationMessage{}
	pending := make([]relayOutboxRecord, 0, 8)
	processedLSN := startLSN
	nextStatusAt := time.Now().Add(relayStatusInterval)

	for {
		if time.Now().After(nextStatusAt) {
			if err := pglogrepl.SendStandbyStatusUpdate(ctx, replConn, pglogrepl.StandbyStatusUpdate{WALWritePosition: processedLSN}); err != nil {
				if !relayStopping(ctx, err) {
					log.Warn("Postgres outbox relay standby status failed", "err", err)
				}
				return
			}
			nextStatusAt = time.Now().Add(relayStatusInterval)
		}

		readCtx, cancel := context.WithDeadline(ctx, nextStatusAt)
		rawMsg, err := replConn.ReceiveMessage(readCtx)
		cancel()
		if err != nil {
			if pgconn.Timeout(err) {
				continue
			}
			if !relayStopping(ctx, err) {
				log.Warn("Postgres outbox relay receive failed", "err", err)
			}
			return
		}

		if errMsg, ok := rawMsg.(*pgproto3.ErrorResponse); ok {
			log.Warn("Postgres outbox relay server error", "severity", errMsg.Severity, "message", errMsg.Message, "detail", errMsg.Detail)
			return
		}

		copyData, ok := rawMsg.(*pgproto3.CopyData)
		if !ok {
			continue
		}

		switch copyData.Data[0] {
		case pglogrepl.PrimaryKeepaliveMessageByteID:
			keepalive, err := pglogrepl.ParsePrimaryKeepaliveMessage(copyData.Data[1:])
			if err != nil {
				log.Warn("Postgres outbox relay keepalive parse failed", "err", err)
				return
			}
			if keepalive.ReplyRequested {
				nextStatusAt = time.Time{}
			}
		case pglogrepl.XLogDataByteID:
			xld, err := pglogrepl.ParseXLogData(copyData.Data[1:])
			if err != nil {
				log.Warn("Postgres outbox relay xlog parse failed", "err", err)
				return
			}
			msg, err := pglogrepl.Parse(xld.WALData)
			if err != nil {
				log.Warn("Postgres outbox relay logical message parse failed", "err", err)
				return
			}
			switch logicalMsg := msg.(type) {
			case *pglogrepl.RelationMessage:
				relations[logicalMsg.RelationID] = logicalMsg
			case *pglogrepl.BeginMessage:
				pending = pending[:0]
			case *pglogrepl.InsertMessage:
				rel, ok := relations[logicalMsg.RelationID]
				if !ok {
					log.Warn("Postgres outbox relay insert missing relation", "relationID", logicalMsg.RelationID)
					return
				}
				record, match, err := decodeOutboxInsert(rel, logicalMsg)
				if err != nil {
					log.Warn("Postgres outbox relay insert decode failed", "err", err)
					return
				}
				if match {
					pending = append(pending, record)
				}
			case *pglogrepl.CommitMessage:
				var err error
				lastEventSeq, err = r.materializeAndPublishBatch(ctx, lastEventSeq, pending)
				if err != nil {
					log.Warn("Postgres outbox relay publish failed", "commitLSN", logicalMsg.CommitLSN.String(), "err", err)
					return
				}
				pending = pending[:0]
				if logicalMsg.TransactionEndLSN > processedLSN {
					processedLSN = logicalMsg.TransactionEndLSN
				}
			}
		}
	}
}

func (r *postgresOutboxRelay) ensurePublication(ctx context.Context) error {
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
	replConn, err := connectReplication(ctx, r.store.cfg.DBURL)
	if err != nil {
		return nil, 0, explainOutboxRelaySetupError("connect replication", err)
	}

	startLSN, exists, err := currentSlotLSN(ctx, r.store.db, postgresOutboxSlotName)
	if err != nil {
		replConn.Close(context.Background())
		return nil, 0, err
	}
	if !exists {
		result, err := pglogrepl.CreateReplicationSlot(ctx, replConn, postgresOutboxSlotName, "pgoutput", pglogrepl.CreateReplicationSlotOptions{})
		if err != nil {
			replConn.Close(context.Background())
			return nil, 0, explainOutboxRelaySetupError("create replication slot", err)
		}
		startLSN, err = pglogrepl.ParseLSN(result.ConsistentPoint)
		if err != nil {
			replConn.Close(context.Background())
			return nil, 0, fmt.Errorf("postgres outbox relay parse replication slot LSN %q: %w", result.ConsistentPoint, err)
		}
	}
	return replConn, startLSN, nil
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
		if err == sql.ErrNoRows {
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
		}
	}
	if record.TxSeq == 0 || record.Event == "" || record.Kind == "" || len(record.Data) == 0 {
		return relayOutboxRecord{}, false, fmt.Errorf("outbox insert missing required fields: tx_seq=%d event=%q kind=%q data=%d", record.TxSeq, record.Event, record.Kind, len(record.Data))
	}
	return record, true, nil
}

func (r *postgresOutboxRelay) loadLastEventSeq(ctx context.Context) (int64, error) {
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
		switch {
		case len(event.UserIDs) > 0:
			if err := eventing.PublishToUsers(ctx, r.bus, event.UserIDs, event); err != nil {
				return lastEventSeq, err
			}
		case event.ConversationGroupID != uuid.Nil:
			if err := eventing.PublishToGroup(ctx, r.store, r.bus, event.ConversationGroupID, event); err != nil {
				return lastEventSeq, err
			}
		default:
			if err := r.bus.Publish(ctx, event); err != nil {
				return lastEventSeq, err
			}
		}
	}

	return nextEventSeq, nil
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

func relayStopping(ctx context.Context, err error) bool {
	if ctx.Err() != nil || err == nil {
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
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
