//go:build !nomongo

package mongo

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/model"
	_ "github.com/chirino/memory-service/internal/plugin/cache/local"
	registrycache "github.com/chirino/memory-service/internal/registry/cache"
	registrymigrate "github.com/chirino/memory-service/internal/registry/migrate"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/testutil/appendtest"
	"github.com/chirino/memory-service/internal/testutil/testmongo"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestPartialAppendBatchFinishesOriginalPatchAfterFailure(t *testing.T) {
	for _, archived := range []bool{false, true} {
		t.Run(fmt.Sprintf("archived=%t", archived), func(t *testing.T) {
			ctx, store := newMongoOutboxTestStore(t)
			store.cfg.OutboxEnabled = false
			require.NoError(t, registrymigrate.RunAll(ctx))
			client := "client1"
			conv, err := store.CreateConversation(ctx, "user1", client, "Before", map[string]interface{}{"keep": "preserved", "remove": "old"}, nil, nil, nil)
			require.NoError(t, err)
			seq := uint32(40)
			request := registrystore.SequencedAppendRequest{UserID: "user1", ClientID: &client, Entries: []registrystore.CreateEntryRequest{
				{Channel: "journal", ContentType: "test", Seq: &seq, Content: []byte(`"anchor"`)},
				{Channel: "history", ContentType: "history", Content: []byte(`[]`)},
			}}
			title := "Original patch"
			patch := &registrystore.AppendConversationPatch{Title: &title, Metadata: registrystore.MetadataPatch{"stage": "finished", "remove": nil}, Archived: &archived}
			writeCtx, _ := registrystore.WithAppendConversationPatch(ctx, patch)
			interrupted := errors.New("interrupted before conversation patch")
			store.appendBatchPatchBeforeApply = func() error { return interrupted }
			_, err = store.AppendEntries(writeCtx, "user1", conv.ID, request.Entries, &client, nil, nil)
			require.ErrorIs(t, err, interrupted)
			anchors, err := store.GetEntriesBySequence(ctx, "user1", conv.ID, []uint32{seq})
			require.NoError(t, err)
			require.Len(t, anchors, 1)
			require.False(t, anchors[0].AppendBatchComplete, "patch failure must leave the batch recoverable even when every entry exists")
			before, err := store.GetConversation(ctx, "user1", conv.ID)
			require.NoError(t, err)
			require.Equal(t, "Before", before.Title)

			loader, err := registrystore.Select("mongo")
			require.NoError(t, err)
			reopened, err := loader(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { _ = reopened.(*MongoStore).client.Disconnect(context.Background()) })
			retryTitle := "Retry must not win"
			retryCtx, result := registrystore.WithAppendConversationPatch(ctx, &registrystore.AppendConversationPatch{Title: &retryTitle, Metadata: registrystore.MetadataPatch{"stage": "retry"}})
			match, err := registrystore.FindSequencedAppendMatch(retryCtx, reopened, conv.ID, request)
			require.NoError(t, err)
			require.True(t, match.Exact)
			require.Len(t, match.Created, len(request.Entries))
			require.True(t, result.Handled)
			require.True(t, result.Changed)
			completed, err := reopened.GetConversation(ctx, "user1", conv.ID)
			require.NoError(t, err)
			require.Equal(t, title, completed.Title)
			require.Equal(t, "finished", completed.Metadata["stage"])
			require.Equal(t, "preserved", completed.Metadata["keep"])
			require.NotContains(t, completed.Metadata, "remove")
			require.Equal(t, archived, completed.ArchivedAt != nil)
			later := "Later edit"
			_, err = reopened.UpdateConversation(ctx, "user1", conv.ID, &later, registrystore.MetadataPatch{"stage": "later", "remove": "later"})
			require.NoError(t, err)
			match, err = registrystore.FindSequencedAppendMatch(retryCtx, reopened, conv.ID, request)
			require.NoError(t, err)
			require.True(t, match.Exact)
			require.Empty(t, match.Created)
			completed, err = reopened.GetConversation(ctx, "user1", conv.ID)
			require.NoError(t, err)
			require.Equal(t, later, completed.Title)
			require.Equal(t, "later", completed.Metadata["stage"])
			require.Equal(t, "later", completed.Metadata["remove"])
		})
	}
}

// Independent clients model two service processes finalizing the same anchor.
func TestPartialAppendBatchStaleFinalizerPreservesLaterEdits(t *testing.T) {
	for _, stage := range []int{1, 2, 3} {
		for _, archived := range []bool{false, true} {
			t.Run(fmt.Sprintf("stage=%d/archived=%t", stage, archived), func(t *testing.T) {
				cfg := config.DefaultConfig()
				cfg.DatastoreType = "mongo"
				cfg.DBURL = testmongo.StartMongo(t)
				cfg.OutboxEnabled = false
				cfg.EncryptionDBDisabled = true
				ctx := config.WithContext(context.Background(), &cfg)
				load, err := registrystore.Select("mongo")
				require.NoError(t, err)
				loaded, err := load(ctx)
				require.NoError(t, err)
				store := loaded.(*MongoStore)
				t.Cleanup(func() { _ = store.client.Disconnect(context.Background()) })
				require.NoError(t, registrymigrate.RunAll(ctx))
				client := "client1"
				conv, err := store.CreateConversation(ctx, "user1", client, "Before", nil, nil, nil, nil)
				require.NoError(t, err)
				// Model an existing schema-v2 datastore from before revision fencing.
				_, err = store.conversations().UpdateOne(ctx, bson.M{"_id": conv.ID}, bson.M{"$unset": bson.M{"patch_revision": ""}})
				require.NoError(t, err)
				_, err = store.groups().UpdateOne(ctx, bson.M{"_id": conv.ConversationGroupID.String()}, bson.M{"$unset": bson.M{"patch_revision": ""}})
				require.NoError(t, err)
				// Recover without a reset or eager migration.
				legacyCount, err := store.conversations().CountDocuments(ctx, bson.M{"_id": conv.ID, "patch_revision": bson.M{"$exists": false}})
				require.NoError(t, err)
				require.EqualValues(t, 1, legacyCount)
				legacyCount, err = store.groups().CountDocuments(ctx, bson.M{"_id": conv.ConversationGroupID.String(), "patch_revision": bson.M{"$exists": false}})
				require.NoError(t, err)
				require.EqualValues(t, 1, legacyCount)
				seq := uint32(60)
				request := registrystore.SequencedAppendRequest{UserID: "user1", ClientID: &client, Entries: []registrystore.CreateEntryRequest{
					{Channel: "journal", ContentType: "test", Seq: &seq, Content: []byte(`"anchor"`)},
					{Channel: "history", ContentType: "history", Content: []byte(`[]`)},
				}}
				title := "Saved patch"
				writeCtx, _ := registrystore.WithAppendConversationPatch(ctx, &registrystore.AppendConversationPatch{Title: &title, Metadata: registrystore.MetadataPatch{"stage": "saved"}, Archived: &archived})
				store.appendBatchPatchBeforeApply = func() error { return errors.New("interrupted patch") }
				_, err = store.AppendEntries(writeCtx, "user1", conv.ID, request.Entries, &client, nil, nil)
				require.Error(t, err)
				loader, err := registrystore.Select("mongo")
				require.NoError(t, err)
				reopened, err := loader(ctx)
				require.NoError(t, err)
				winner := reopened.(*MongoStore)
				winner.cfg.OutboxEnabled = false
				t.Cleanup(func() { _ = winner.client.Disconnect(context.Background()) })
				paused, resume := make(chan struct{}), make(chan struct{})
				var calls int
				store.appendBatchPatchBeforeApply = func() error {
					calls++
					if calls == stage {
						close(paused)
						<-resume
					}
					return nil
				}
				done := make(chan struct{})
				var loserMatch registrystore.SequencedAppendMatch
				var loserErr error
				go func() {
					defer close(done)
					loserMatch, loserErr = registrystore.FindSequencedAppendMatch(ctx, store, conv.ID, request)
				}()
				t.Cleanup(func() {
					select {
					case <-resume:
					default:
						close(resume)
					}
					<-done
				})
				select {
				case <-paused:
				case <-time.After(10 * time.Second):
					t.Fatal("finalizer did not pause")
				}
				match, err := registrystore.FindSequencedAppendMatch(ctx, winner, conv.ID, request)
				require.NoError(t, err)
				require.Len(t, match.Created, 2)
				later := "Later edit"
				_, err = winner.UpdateConversation(ctx, "user1", conv.ID, &later, registrystore.MetadataPatch{"stage": "later"})
				require.NoError(t, err)
				if archived {
					_, err = winner.UnarchiveConversationIfNeeded(ctx, "user1", conv.ID)
				} else {
					_, err = winner.ArchiveConversationIfNeeded(ctx, "user1", conv.ID)
				}
				require.NoError(t, err)
				close(resume)
				<-done
				require.NoError(t, loserErr)
				require.True(t, loserMatch.Exact)
				require.Empty(t, loserMatch.Created)
				current, err := winner.GetConversation(ctx, "user1", conv.ID)
				require.NoError(t, err)
				require.Equal(t, later, current.Title)
				require.Equal(t, "later", current.Metadata["stage"])
				require.Equal(t, !archived, current.ArchivedAt != nil)
				var group groupDoc
				require.NoError(t, winner.groups().FindOne(ctx, bson.M{"_id": conv.ConversationGroupID.String()}).Decode(&group))
				require.Equal(t, !archived, group.ArchivedAt != nil)
				require.Greater(t, group.PatchRevision, int64(0))
				var migrated convDoc
				require.NoError(t, winner.conversations().FindOne(ctx, bson.M{"_id": conv.ID}).Decode(&migrated))
				require.Greater(t, migrated.PatchRevision, int64(0))
			})
		}
	}
}

func TestPartialAppendBatchNoOpFinalizerRetainsChangeResult(t *testing.T) {
	ctx, store := newMongoOutboxTestStore(t)
	store.cfg.OutboxEnabled = false
	require.NoError(t, registrymigrate.RunAll(ctx))
	client := "client1"
	conv, err := store.CreateConversation(ctx, "user1", client, "Before", nil, nil, nil, nil)
	require.NoError(t, err)
	seq, archived := uint32(70), true
	request := registrystore.SequencedAppendRequest{UserID: "user1", ClientID: &client, Entries: []registrystore.CreateEntryRequest{
		{Channel: "journal", ContentType: "test", Seq: &seq, Content: []byte(`"anchor"`)},
		{Channel: "history", ContentType: "history", Content: []byte(`[]`)},
	}}
	writeCtx, _ := registrystore.WithAppendConversationPatch(ctx, &registrystore.AppendConversationPatch{Archived: &archived})
	entries, err := store.AppendEntries(writeCtx, "user1", conv.ID, request.Entries, &client, nil, nil)
	require.NoError(t, err)
	// Model a crash after the last target CAS but before recording its outcome or
	// completing the anchor. The bounded target receipt must retain the change.
	_, err = store.entries().UpdateOne(ctx, bson.M{"_id": entries[0].ID.String()}, bson.M{"$unset": bson.M{"append_batch_complete": "", "append_batch_patch_changed": "", "append_batch_patch_archived": ""}})
	require.NoError(t, err)
	retryCtx, result := registrystore.WithAppendConversationPatch(ctx, nil)
	match, err := registrystore.FindSequencedAppendMatch(retryCtx, store, conv.ID, request)
	require.NoError(t, err)
	require.Len(t, match.Created, 2)
	require.True(t, result.Handled)
	require.True(t, result.Changed, "no-op completion must publish the prior finalizer's change")
	require.NotNil(t, result.Archived)
	require.True(t, *result.Archived)
}

func TestPartialAppendBatchChecksSavedPatchPermissions(t *testing.T) {
	ctx, store := newMongoOutboxTestStore(t)
	store.cfg.OutboxEnabled = false
	require.NoError(t, registrymigrate.RunAll(ctx))
	client := "client1"
	conv, err := store.CreateConversation(ctx, "user1", client, "Before", nil, nil, nil, nil)
	require.NoError(t, err)
	seq, archived := uint32(50), true
	request := registrystore.SequencedAppendRequest{UserID: "user1", ClientID: &client, Entries: []registrystore.CreateEntryRequest{
		{Channel: "journal", ContentType: "test", Seq: &seq, Content: []byte(`"anchor"`)},
		{Channel: "history", ContentType: "history", Content: []byte(`[]`)},
	}}
	writeCtx, _ := registrystore.WithAppendConversationPatch(ctx, &registrystore.AppendConversationPatch{Archived: &archived})
	store.appendBatchPatchBeforeApply = func() error { return errors.New("interrupted patch") }
	_, err = store.AppendEntries(writeCtx, "user1", conv.ID, request.Entries, &client, nil, nil)
	require.Error(t, err)
	store.appendBatchPatchBeforeApply = nil
	anchors, err := store.GetEntriesBySequence(ctx, "user1", conv.ID, []uint32{seq})
	require.NoError(t, err)
	require.Len(t, anchors, 1)
	batch, err := registrystore.DecodeAppendBatch(anchors[0].AppendBatch)
	require.NoError(t, err)
	_, err = store.entries().DeleteOne(ctx, bson.M{"_id": batch.IDs[1].String()})
	require.NoError(t, err)
	_, err = store.memberships().UpdateOne(ctx, bson.M{"conversation_group_id": conv.ConversationGroupID.String(), "user_id": "user1"}, bson.M{"$set": bson.M{"access_level": model.AccessLevelWriter}})
	require.NoError(t, err)
	// Omitting the retry patch cannot bypass the original owner-only mutation.
	_, err = registrystore.FindSequencedAppendMatch(ctx, store, conv.ID, request)
	var forbidden *registrystore.ForbiddenError
	require.ErrorAs(t, err, &forbidden)
	count, err := store.entries().CountDocuments(ctx, bson.M{"conversation_id": conv.ID})
	require.NoError(t, err)
	require.EqualValues(t, 1, count, "authorization must precede recovery writes")
	current, err := store.GetConversation(ctx, "user1", conv.ID)
	require.NoError(t, err)
	require.Nil(t, current.ArchivedAt)
}

func TestOrderedAppendBatch(t *testing.T) {
	for _, atomic := range []bool{false, true} {
		t.Run(fmt.Sprintf("atomic=%t", atomic), func(t *testing.T) {
			ctx, store := newMongoOutboxTestStore(t)
			store.cfg.OutboxEnabled = atomic
			require.NoError(t, registrymigrate.RunAll(ctx))
			appendtest.OrderedBatch(t, ctx, store)
		})
	}
}

func TestPartialAppendBatchConcurrentRecovery(t *testing.T) {
	ctx, store := newMongoOutboxTestStore(t)
	store.cfg.OutboxEnabled = false
	require.NoError(t, registrymigrate.RunAll(ctx))
	client := "client1"
	conv, err := store.CreateConversation(ctx, "user1", client, "Partial", nil, nil, nil, nil)
	require.NoError(t, err)
	seq := uint32(10)
	request := registrystore.SequencedAppendRequest{UserID: "user1", ClientID: &client, Entries: []registrystore.CreateEntryRequest{
		{Channel: "history", ContentType: "history", Content: []byte(`[{"role":"USER","text":"prefix"}]`)},
		{Channel: "journal", ContentType: "test", Seq: &seq, Content: []byte(`{"text":"anchor"}`)},
		{Channel: "history", ContentType: "history", Content: []byte(`[{"role":"USER","text":"suffix"}]`)},
	}}
	entries, err := store.AppendEntries(ctx, "user1", conv.ID, request.Entries, &client, nil, nil)
	require.NoError(t, err)
	// Simulate a process crash immediately after the sequence anchor was persisted.
	_, err = store.entries().UpdateOne(ctx, bson.M{"_id": entries[1].ID.String()}, bson.M{"$unset": bson.M{"append_batch_complete": ""}})
	require.NoError(t, err)
	_, err = store.entries().DeleteMany(ctx, bson.M{"_id": bson.M{"$in": bson.A{entries[0].ID.String(), entries[2].ID.String()}}})
	require.NoError(t, err)
	// Later appends must not affect the original range of reserved timestamps.
	_, err = store.AppendEntries(ctx, "user1", conv.ID, []registrystore.CreateEntryRequest{{Channel: "history", ContentType: "history", Content: []byte(`[]`)}}, &client, nil, nil)
	require.NoError(t, err)
	shortened := request
	shortened.Entries = shortened.Entries[:2]
	match, err := registrystore.FindSequencedAppendMatch(ctx, store, conv.ID, shortened)
	require.NoError(t, err)
	require.False(t, match.Exact)
	count, err := store.entries().CountDocuments(ctx, bson.M{"conversation_id": conv.ID})
	require.NoError(t, err)
	require.EqualValues(t, 2, count)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	created := make(chan []model.Entry, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			match, err := registrystore.FindSequencedAppendMatch(ctx, store, conv.ID, request)
			if err == nil {
				if !match.Exact || len(match.Entries) != 3 {
					err = fmt.Errorf("partial retry did not complete")
				} else {
					for i, entry := range match.Entries {
						if entry.ID != entries[i].ID || !entry.CreatedAt.Equal(entries[i].CreatedAt) {
							err = fmt.Errorf("reserved position changed")
						}
					}
				}
			}
			created <- match.Created
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	close(created)
	for err := range errs {
		require.NoError(t, err)
	}
	createdCount := 0
	for batch := range created {
		createdCount += len(batch)
	}
	require.Equal(t, 3, createdCount)
	count, err = store.entries().CountDocuments(ctx, bson.M{"conversation_id": conv.ID})
	require.NoError(t, err)
	require.EqualValues(t, 4, count)
	// Reload the service object to prove recovery uses only durable state.
	loader, err := registrystore.Select("mongo")
	require.NoError(t, err)
	reopened, err := loader(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = reopened.(*MongoStore).client.Disconnect(context.Background()) })
	match, err = registrystore.FindSequencedAppendMatch(ctx, reopened, conv.ID, request)
	require.NoError(t, err)
	require.True(t, match.Exact)
	require.Empty(t, match.Created)
}

func TestPartialAppendBatchRefreshesContextCache(t *testing.T) {
	ctx, store := newMongoOutboxTestStore(t)
	store.cfg.OutboxEnabled = false
	require.NoError(t, registrymigrate.RunAll(ctx))
	loader, err := registrycache.Select("local")
	require.NoError(t, err)
	cache, err := loader(ctx)
	require.NoError(t, err)
	store.entriesCache = cache
	client := "client1"
	conv, err := store.CreateConversation(ctx, "user1", client, "Partial context", nil, nil, nil, nil)
	require.NoError(t, err)
	old := registrystore.CreateEntryRequest{Channel: "context", ContentType: "test", Content: []byte(`["old"]`)}
	first, err := store.SyncAgentEntry(ctx, "user1", conv.ID, old, client, nil)
	require.NoError(t, err)
	require.EqualValues(t, 1, *first.Epoch)
	cached, err := cache.Get(ctx, conv.ID, scopedAgentCacheKey(client, ""))
	require.NoError(t, err)
	require.NotNil(t, cached)
	seq, epoch := uint32(10), int64(2)
	request := registrystore.SequencedAppendRequest{UserID: "user1", ClientID: &client, Epoch: &epoch, Entries: []registrystore.CreateEntryRequest{
		{Channel: "context", ContentType: "test", Seq: &seq, Content: []byte(`["anchor"]`)},
		{Channel: "context", ContentType: "test", Content: []byte(`["new"]`)},
	}}
	entries, err := store.AppendEntries(ctx, "user1", conv.ID, request.Entries, &client, nil, &epoch)
	require.NoError(t, err)
	_, err = store.entries().UpdateOne(ctx, bson.M{"_id": entries[0].ID.String()}, bson.M{"$unset": bson.M{"append_batch_complete": ""}})
	require.NoError(t, err)
	_, err = store.entries().DeleteOne(ctx, bson.M{"_id": entries[1].ID.String()})
	require.NoError(t, err)
	// Restore the cache state from before the writer persisted its anchor.
	require.NoError(t, cache.Set(ctx, conv.ID, scopedAgentCacheKey(client, ""), *cached, 0))
	anchors, err := store.GetEntriesBySequence(ctx, "user1", conv.ID, []uint32{seq})
	require.NoError(t, err)
	require.Len(t, anchors, 1)
	match, err := registrystore.FindSequencedAppendMatch(ctx, store, conv.ID, request)
	require.NoError(t, err)
	require.True(t, match.Exact)
	refreshed, err := cache.Get(ctx, conv.ID, scopedAgentCacheKey(client, ""))
	require.NoError(t, err)
	if refreshed != nil {
		require.EqualValues(t, 2, *refreshed.Epoch)
	}
	// Another recovery loaded the incomplete anchor before the winning finalizer.
	// It must invalidate even if it loses the completion compare-and-swap.
	require.NoError(t, cache.Set(ctx, conv.ID, scopedAgentCacheKey(client, ""), *cached, 0))
	match, err = store.RecoverAppendBatch(ctx, anchors[0], request)
	require.NoError(t, err)
	require.True(t, match.Exact)
	require.Empty(t, match.Created)
	result, err := store.SyncAgentEntry(ctx, "user1", conv.ID, old, client, nil)
	require.NoError(t, err)
	require.False(t, result.NoOp, "recovered epoch 2 must replace the cached epoch 1")
	require.EqualValues(t, 3, *result.Epoch)
}
