//go:build !nosqlite

package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/operationevent"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/service/eventing"
	"github.com/chirino/memory-service/internal/txscope"
	"github.com/google/uuid"
)

type sqliteOutboxRelay struct {
	ctx  context.Context
	wake chan struct{}
	done chan struct{}
}

// The relay reads committed rows in sequence order. Publication and retries run
// outside write transactions, so a slow bus cannot stall SQLite mutations.
func (s *SQLiteStore) StartOutboxRelay(ctx context.Context, bus registryeventbus.EventBus) error {
	if !s.OutboxEnabled() || bus == nil {
		return nil
	}
	h := s.handle
	for {
		h.outboxMu.Lock()
		if prior := h.outboxRelay; prior != nil {
			if prior.ctx.Err() == nil {
				h.outboxMu.Unlock()
				return nil
			}
			h.outboxMu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-prior.done:
			}
			h.outboxMu.Lock()
			if h.outboxRelay == prior {
				h.outboxRelay = nil
			}
			h.outboxMu.Unlock()
			continue
		}
		var cursor string
		err := s.InReadTx(ctx, func(tx context.Context) error {
			var err error
			cursor, err = s.CurrentOutboxCursor(tx)
			return err
		})
		if err != nil {
			h.outboxMu.Unlock()
			return err
		}
		after, err := parseSQLiteOutboxCursor(cursor)
		if err != nil {
			h.outboxMu.Unlock()
			return err
		}
		runCtx, cancel := context.WithCancel(ctx)
		relay := &sqliteOutboxRelay{ctx: runCtx, wake: make(chan struct{}, 1), done: make(chan struct{})}
		h.outboxRelay = relay
		h.outboxMu.Unlock()
		go s.runOutboxRelay(relay, cancel, sqliteDurableBus{bus}, after)
		return nil
	}
}
func (s *SQLiteStore) RelayPublishesOutboxEvents() bool {
	if !s.OutboxEnabled() || s.handle == nil {
		return false
	}
	s.handle.outboxMu.Lock()
	defer s.handle.outboxMu.Unlock()
	return s.handle.outboxRelay != nil && s.handle.outboxRelay.ctx.Err() == nil
}
func (s *SQLiteStore) publishCommittedOutbox(ctx context.Context, rows []registrystore.OutboxEvent) {
	if len(rows) == 0 || !s.RelayPublishesOutboxEvents() {
		return
	}
	txscope.AfterCommit(ctx, func(context.Context) {
		s.handle.outboxMu.Lock()
		relay := s.handle.outboxRelay
		s.handle.outboxMu.Unlock()
		if relay != nil {
			select {
			case relay.wake <- struct{}{}:
			default:
			}
		}
	})
}

func (s *SQLiteStore) runOutboxRelay(relay *sqliteOutboxRelay, cancel context.CancelFunc, bus registryeventbus.EventBus, after int64) {
	defer close(relay.done)
	defer cancel()
	event := operationevent.New("job.sqlite.outbox.relay")
	event.EmitStart()
	defer func() {
		if recovered := recover(); recovered != nil {
			stack := debug.Stack()
			operationevent.LogRecoveredPanic(event, "", recovered, stack)
			event.SetReason("panic")
			event.EmitTerminal(operationevent.ResultFailed)
		} else {
			event.EmitTerminal(operationevent.ResultCanceled)
		}
	}()
	backoff := 100 * time.Millisecond
	for relay.ctx.Err() == nil {
		var rows []sqliteOutboxRow
		err := s.InReadTx(relay.ctx, func(tx context.Context) error {
			// Unlike client replay, this live publisher can keep advancing after
			// retention removes a previously published cursor's marker row.
			return s.dbFor(tx).Where("seq > ?", after).Order("seq ASC").Limit(256).Find(&rows).Error
		})
		if err == nil {
			for _, row := range rows {
				err = s.publishOutboxEvent(relay.ctx, bus, registrystore.OutboxEvent{Cursor: formatSQLiteOutboxCursor(row.Seq), Event: row.Event, Kind: row.Kind, Data: []byte(row.Data), CreatedAt: row.CreatedAt})
				if err != nil {
					break
				}
				after = row.Seq
			}
		}
		if relay.ctx.Err() != nil {
			return
		}
		if err == nil && len(rows) > 0 {
			backoff = 100 * time.Millisecond
			continue
		}
		if err != nil {
			log.Warn("SQLite outbox publication retry", "errorType", fmt.Sprintf("%T", err))
			timer := time.NewTimer(backoff)
			select {
			case <-relay.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			backoff = min(backoff*2, time.Second)
			continue
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-relay.ctx.Done():
			timer.Stop()
			return
		case <-relay.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

type sqliteDurableBus struct{ registryeventbus.EventBus }

func (b sqliteDurableBus) Publish(ctx context.Context, event registryeventbus.Event) error {
	if durable, ok := b.EventBus.(registryeventbus.DurablePublisher); ok {
		return durable.PublishDurable(ctx, event)
	}
	return b.EventBus.Publish(ctx, event)
}

func (s *SQLiteStore) publishOutboxEvent(ctx context.Context, bus registryeventbus.EventBus, row registrystore.OutboxEvent) error {
	var data map[string]any
	if err := json.Unmarshal(row.Data, &data); err != nil {
		return err
	}
	occurred := row.CreatedAt.UTC()
	event := registryeventbus.Event{Event: row.Event, Kind: row.Kind, Data: data, OutboxCursor: row.Cursor, OccurredAt: &occurred}
	if group, ok := data["conversation_group"].(string); ok {
		event.ConversationGroupID, _ = uuid.Parse(group)
	}
	if row.Kind == "membership" {
		if user, ok := data["user"].(string); ok {
			event.UserIDs = []string{user}
		}
	}
	if row.Kind == "conversation" && row.Event == "deleted" {
		if users, ok := data["members"].([]any); ok {
			for _, value := range users {
				if user, ok := value.(string); ok {
					event.UserIDs = append(event.UserIDs, user)
				}
			}
		}
	}
	if len(event.UserIDs) > 0 {
		return eventing.PublishToUsers(ctx, bus, event.UserIDs, event)
	}
	if event.ConversationGroupID != uuid.Nil {
		return eventing.PublishToGroup(ctx, s, bus, event.ConversationGroupID, event)
	}
	return bus.Publish(ctx, event)
}
