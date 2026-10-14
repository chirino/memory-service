package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/model"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestEntryOrderAfterTimezoneUpgrade(t *testing.T) {
	cfg := &config.Config{DatastoreType: "sqlite", DBURL: filepath.Join(t.TempDir(), "upgrade.db"), DatastoreMigrateAtStart: true, EncryptionDBDisabled: true}
	ctx := config.WithContext(context.Background(), cfg)
	legacyDB, err := sql.Open("sqlite3", cfg.DBURL)
	require.NoError(t, err)
	_, err = legacyDB.Exec(legacyTextSchemaSQL)
	require.NoError(t, err)
	groupID, convID := uuid.New(), uuid.NewString()
	_, err = legacyDB.Exec(`INSERT INTO conversation_groups(id) VALUES (?);`, groupID.String())
	require.NoError(t, err)
	_, err = legacyDB.Exec(`INSERT INTO conversations(id,owner_user_id,client_id,conversation_group_id) VALUES (?, 'alice', 'client', ?)`, convID, groupID.String())
	require.NoError(t, err)
	_, err = legacyDB.Exec(`INSERT INTO conversation_ancestry(conversation_group_id,descendant_conversation_id,ancestor_conversation_id,depth) VALUES (?,?,?,0)`, groupID.String(), convID, convID)
	require.NoError(t, err)
	_, err = legacyDB.Exec(`INSERT INTO conversation_memberships(conversation_group_id,user_id,access_level) VALUES (?, 'alice', 'owner')`, groupID.String())
	require.NoError(t, err)
	conv := &registrystore.ConversationDetail{ConversationSummary: registrystore.ConversationSummary{ID: convID, ConversationGroupID: groupID}}
	oldTime := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	legacy := []model.Entry{
		{ID: uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff"), CreatedAt: oldTime.Add(100 * time.Nanosecond).In(time.FixedZone("UTC+0530", 19800))},
		{ID: uuid.MustParse("00000000-0000-0000-0000-000000000001"), CreatedAt: oldTime.Add(200 * time.Nanosecond).In(time.FixedZone("UTC-0700", -25200))},
	}
	for i := range legacy {
		legacy[i].ConversationID = conv.ID
		legacy[i].ConversationGroupID = conv.ConversationGroupID
		legacy[i].Channel = model.ChannelHistory
		legacy[i].ContentType = "history"
		legacy[i].Content = []byte(`[]`)
	}
	for _, entry := range legacy {
		_, err = legacyDB.Exec(`INSERT INTO entries(id,conversation_id,conversation_group_id,channel,content_type,content,created_at,created_at_unix_ms) VALUES (?,?,?,'history','history','[]',?,?)`, entry.ID.String(), conv.ID, groupID.String(), entry.CreatedAt, entry.CreatedAt.UnixMilli())
		require.NoError(t, err)
	}
	require.NoError(t, legacyDB.Close())
	require.NoError(t, (&sqliteMigrator{}).Migrate(ctx))
	require.NoError(t, (&sqliteMigrator{}).Migrate(ctx))
	loader, err := registrystore.Select("sqlite")
	require.NoError(t, err)
	store, err := loader(ctx)
	require.NoError(t, err)
	s := store.(*SQLiteStore)
	var newer []model.Entry
	require.NoError(t, store.InWriteTx(ctx, func(tx context.Context) error {
		var err error
		newer, err = store.AppendEntries(tx, "alice", conv.ID, []registrystore.CreateEntryRequest{
			{Channel: "history", ContentType: "history", Content: []byte(`[]`)},
			{Channel: "history", ContentType: "history", Content: []byte(`[]`)},
		}, nil, nil, nil)
		return err
	}))
	want := append(append([]model.Entry(nil), legacy...), newer...)
	check := func(t *testing.T, id string, q registrystore.EntryListQuery, expected []model.Entry) {
		t.Helper()
		require.NoError(t, store.InReadTx(ctx, func(tx context.Context) error {
			page, err := store.GetEntries(tx, "alice", id, q)
			require.NoError(t, err)
			require.Len(t, page.Data, len(expected))
			for i := range expected {
				require.Equal(t, expected[i].ID, page.Data[i].ID)
				require.True(t, expected[i].CreatedAt.Equal(page.Data[i].CreatedAt))
			}
			return nil
		}))
	}
	t.Run("listing", func(t *testing.T) { check(t, conv.ID, registrystore.EntryListQuery{Limit: 10}, want) })
	eq := legacy[0].CreatedAt
	t.Run("exact-precision-filter", func(t *testing.T) {
		check(t, conv.ID, registrystore.EntryListQuery{Limit: 10, CreatedAtFilter: &registrystore.CreatedAtFilter{Eq: &eq}}, legacy[:1])
	})
	t.Run("inclusive-precision-filter", func(t *testing.T) {
		check(t, conv.ID, registrystore.EntryListQuery{Limit: 10, CreatedAtFilter: &registrystore.CreatedAtFilter{After: &eq, Before: &eq}}, legacy[:1])
	})
	t.Run("tail", func(t *testing.T) { check(t, conv.ID, registrystore.EntryListQuery{Limit: 2, Tail: true}, newer) })
	after, before, upTo := legacy[0].ID.String(), newer[0].ID.String(), legacy[1].ID.String()
	t.Run("after", func(t *testing.T) {
		check(t, conv.ID, registrystore.EntryListQuery{Limit: 10, AfterCursor: &after}, want[1:])
	})
	t.Run("before", func(t *testing.T) {
		check(t, conv.ID, registrystore.EntryListQuery{Limit: 10, BeforeCursor: &before}, legacy)
	})
	t.Run("upTo", func(t *testing.T) {
		check(t, conv.ID, registrystore.EntryListQuery{Limit: 10, UpToEntryID: &upTo}, legacy)
	})
	t.Run("admin", func(t *testing.T) {
		require.NoError(t, store.InReadTx(ctx, func(tx context.Context) error {
			page, err := store.AdminGetEntries(tx, conv.ID, registrystore.AdminMessageQuery{Limit: 10})
			require.NoError(t, err)
			require.Len(t, page.Data, len(want))
			for i := range want {
				require.Equal(t, want[i].ID, page.Data[i].ID)
			}
			return nil
		}))
	})
	t.Run("event-snapshot", func(t *testing.T) {
		require.NoError(t, store.InReadTx(ctx, func(tx context.Context) error {
			var cursor *registrystore.EventSnapshotEntryCursor
			for _, expected := range want {
				page, next, err := s.AdminListEventSnapshotEntries(tx, cursor, 1)
				require.NoError(t, err)
				require.Len(t, page, 1)
				require.Equal(t, expected.ID, page[0].ID)
				cursor = next
			}
			return nil
		}))
	})
	for _, boundary := range []model.Entry{legacy[1], newer[0]} {
		t.Run("fork-"+boundary.ID.String(), func(t *testing.T) {
			var fork *registrystore.ConversationDetail
			require.NoError(t, store.InWriteTx(ctx, func(tx context.Context) error {
				var err error
				fork, err = store.CreateConversation(tx, "alice", "client", "Fork", nil, nil, &conv.ID, &boundary.ID)
				return err
			}))
			expected := legacy
			if boundary.ID == legacy[1].ID {
				expected = legacy[:1]
			}
			check(t, fork.ID, registrystore.EntryListQuery{Limit: 10}, expected)
		})
	}
}
