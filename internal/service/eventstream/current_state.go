package eventstream

import (
	"context"
	"fmt"

	"github.com/chirino/memory-service/internal/model"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
)

const currentStatePageSize = 500

// StreamAdminCurrentState emits one full, scope-appropriate OpenAPI resource
// snapshot for each current conversation, entry, and memory.
func StreamAdminCurrentState(ctx context.Context, store registrystore.MemoryStore, episodicStore registryepisodic.EpisodicStore, kinds, conversationIDs map[string]bool, entryFilter EntryEventFilter, emit func(registryeventbus.Event) error) error {
	selected := func(kind string) bool { return len(kinds) == 0 || kinds[kind] }
	snapshots, ok := store.(registrystore.EventSnapshotStore)
	if !ok && (selected("conversation") || selected("entry")) {
		return registrystore.ErrEventSnapshotUnsupported
	}
	if selected("conversation") {
		var cursor *registrystore.EventSnapshotConversationCursor
		for {
			var rows []registrystore.ConversationSummary
			var next *registrystore.EventSnapshotConversationCursor
			err := store.InReadTx(ctx, func(txCtx context.Context) error {
				var err error
				rows, next, err = snapshots.AdminListEventSnapshotConversations(txCtx, cursor, currentStatePageSize)
				return err
			})
			if err != nil {
				return fmt.Errorf("list current conversations: %w", err)
			}
			for i := range rows {
				if len(conversationIDs) > 0 && !conversationIDs[rows[i].ID] {
					continue
				}
				if err := emit(registryeventbus.Event{Event: "snapshot", Kind: "conversation", Data: AdminConversationSummaryResource(&rows[i])}); err != nil {
					return err
				}
			}
			if next == nil {
				break
			}
			cursor = next
		}
	}
	if selected("entry") {
		var cursor *registrystore.EventSnapshotEntryCursor
		for {
			var rows []model.Entry
			var next *registrystore.EventSnapshotEntryCursor
			err := store.InReadTx(ctx, func(txCtx context.Context) error {
				var err error
				rows, next, err = snapshots.AdminListEventSnapshotEntries(txCtx, cursor, currentStatePageSize)
				return err
			})
			if err != nil {
				return fmt.Errorf("list current entries: %w", err)
			}
			for i := range rows {
				if len(conversationIDs) > 0 && !conversationIDs[rows[i].ConversationID] {
					continue
				}
				if !entryFilter.MatchesEntry(rows[i]) {
					continue
				}
				if err := emit(registryeventbus.Event{Event: "snapshot", Kind: "entry", Data: AdminEntryResource(&rows[i])}); err != nil {
					return err
				}
			}
			if next == nil {
				break
			}
			cursor = next
		}
	}
	if selected("memory") {
		snapshotMemories, ok := episodicStore.(registryepisodic.EventSnapshotStore)
		if !ok {
			return fmt.Errorf("current memory snapshots are unsupported by the configured store")
		}
		cursor := ""
		for {
			var page registryepisodic.AdminMemoryPage
			err := episodicStore.InReadTx(ctx, func(txCtx context.Context) error {
				var err error
				page, err = snapshotMemories.AdminListEventSnapshotMemories(txCtx, registryepisodic.AdminMemoryQuery{Archived: registryepisodic.ArchiveFilterInclude, Limit: currentStatePageSize, AfterCursor: cursor})
				return err
			})
			if err != nil {
				return fmt.Errorf("list current memories: %w", err)
			}
			for i := range page.Items {
				if err := emit(registryeventbus.Event{Event: "snapshot", Kind: "memory", Data: AdminMemoryResource(&page.Items[i])}); err != nil {
					return err
				}
			}
			if page.AfterCursor == "" {
				break
			}
			cursor = page.AfterCursor
		}
	}
	return nil
}
