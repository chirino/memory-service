// Package evictiontest checks conversation eviction filters identically on
// every datastore. Callers supply a fresh datastore and a Clock.
package evictiontest

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/model"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/service/eventstream"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Clock backdates timestamps that the store API always sets to the current time.
type Clock interface {
	// SetGroupActivity sets the group's creation time, the creation and update
	// times of its conversations, and the creation times of its entries.
	SetGroupActivity(t *testing.T, ctx context.Context, groupID uuid.UUID, at time.Time)
	// SetGroupArchivedAt archives the group and its conversations at the given time.
	SetGroupArchivedAt(t *testing.T, ctx context.Context, groupID uuid.UUID, at time.Time)
}

const (
	owner  = "owner"
	client = "app"
)

// Cutoff returns a cutoff every datastore stores exactly (MongoDB keeps milliseconds).
func Cutoff() time.Time {
	return time.Now().Add(-30 * 24 * time.Hour).UTC().Truncate(time.Millisecond)
}

type fixture struct {
	t     *testing.T
	ctx   context.Context
	store registrystore.MemoryStore
}

func (f fixture) write(fn func(context.Context) error) {
	f.t.Helper()
	require.NoError(f.t, f.store.InWriteTx(f.ctx, fn))
}

func (f fixture) create(title string) *registrystore.ConversationDetail {
	f.t.Helper()
	var conv *registrystore.ConversationDetail
	f.write(func(ctx context.Context) error {
		var err error
		conv, err = f.store.CreateConversation(ctx, owner, client, title, nil, nil, nil, nil)
		return err
	})
	return conv
}

func (f fixture) fork(parentID string, entryID uuid.UUID) *registrystore.ConversationDetail {
	f.t.Helper()
	var conv *registrystore.ConversationDetail
	f.write(func(ctx context.Context) error {
		var err error
		conv, err = f.store.CreateConversation(ctx, owner, client, "fork", nil, nil, &parentID, &entryID)
		return err
	})
	return conv
}

func (f fixture) appendEntry(conversationID string, startedBy *string) model.Entry {
	f.t.Helper()
	var entries []model.Entry
	clientID := client
	f.write(func(ctx context.Context) error {
		var err error
		entries, err = f.store.AppendEntries(ctx, owner, conversationID, []registrystore.CreateEntryRequest{{
			Channel:                 "history",
			ContentType:             "history",
			Content:                 []byte(`[{"role":"USER","text":"hello"}]`),
			StartedByConversationID: startedBy,
		}}, &clientID, nil, nil)
		return err
	})
	require.Len(f.t, entries, 1)
	return entries[0]
}

func (f fixture) groupOf(conversationID string) uuid.UUID {
	f.t.Helper()
	var conv *registrystore.ConversationDetail
	require.NoError(f.t, f.store.InReadTx(f.ctx, func(ctx context.Context) error {
		var err error
		conv, err = f.store.GetConversation(ctx, owner, conversationID)
		return err
	}))
	return conv.ConversationGroupID
}

// Filters checks every valid archive filter and age basis, cutoff boundaries,
// recent activity in forks and started children, and cascading deletion.
func Filters(t *testing.T, ctx context.Context, store registrystore.MemoryStore, clock Clock) {
	f := fixture{t: t, ctx: ctx, store: store}
	cutoff := Cutoff()
	old := cutoff.Add(-24 * time.Hour)
	recentArchive := cutoff.Add(time.Hour)

	archivedOld := f.create("archived old").ConversationGroupID
	clock.SetGroupActivity(t, ctx, archivedOld, old)
	clock.SetGroupArchivedAt(t, ctx, archivedOld, old)

	// Archived recently, but otherwise idle since long before the cutoff.
	archivedRecently := f.create("archived recently").ConversationGroupID
	clock.SetGroupActivity(t, ctx, archivedRecently, old)
	clock.SetGroupArchivedAt(t, ctx, archivedRecently, recentArchive)

	idle := f.create("idle").ConversationGroupID
	clock.SetGroupActivity(t, ctx, idle, old)

	activeConv := f.create("recent entry")
	activeEntry := activeConv.ConversationGroupID
	clock.SetGroupActivity(t, ctx, activeEntry, old)
	f.appendEntry(activeConv.ID, nil)

	forkRoot := f.create("fork root")
	forkEntry := f.appendEntry(forkRoot.ID, nil)
	fork := f.fork(forkRoot.ID, forkEntry.ID)
	activeFork := forkRoot.ConversationGroupID
	clock.SetGroupActivity(t, ctx, activeFork, old)
	title := "recently updated fork"
	f.write(func(ctx context.Context) error {
		_, err := store.UpdateConversation(ctx, owner, fork.ID, &title, nil)
		return err
	})

	parent := f.create("parent")
	f.appendEntry(parent.ID, nil)
	childID := uuid.NewString()
	childEntry := f.appendEntry(childID, &parent.ID)
	childGroup := f.groupOf(childID)
	childFork := f.fork(childID, childEntry.ID)
	parentGroup := parent.ConversationGroupID
	clock.SetGroupActivity(t, ctx, parentGroup, old)
	clock.SetGroupActivity(t, ctx, childGroup, old)
	f.appendEntry(childFork.ID, nil)

	atCutoff := f.create("at cutoff").ConversationGroupID
	clock.SetGroupActivity(t, ctx, atCutoff, cutoff)
	beforeCutoff := f.create("before cutoff").ConversationGroupID
	clock.SetGroupActivity(t, ctx, beforeCutoff, cutoff.Add(-time.Millisecond))

	all := []uuid.UUID{archivedOld, archivedRecently, idle, activeEntry, activeFork, parentGroup, childGroup, atCutoff, beforeCutoff}
	cases := []struct {
		archived registrystore.ConversationArchiveFilter
		age      registrystore.ConversationAgeBasis
		want     []uuid.UUID
	}{
		{registrystore.ConversationArchiveOnly, registrystore.ConversationAgeArchivedAt, []uuid.UUID{archivedOld}},
		{registrystore.ConversationArchiveOnly, registrystore.ConversationAgeLastActivity, []uuid.UUID{archivedOld, archivedRecently}},
		{registrystore.ConversationArchiveOnly, registrystore.ConversationAgeCreatedAt, []uuid.UUID{archivedOld, archivedRecently}},
		{registrystore.ConversationArchiveExclude, registrystore.ConversationAgeLastActivity, []uuid.UUID{idle, beforeCutoff}},
		{registrystore.ConversationArchiveExclude, registrystore.ConversationAgeCreatedAt, []uuid.UUID{idle, activeEntry, activeFork, parentGroup, childGroup, beforeCutoff}},
		{registrystore.ConversationArchiveInclude, registrystore.ConversationAgeLastActivity, []uuid.UUID{archivedOld, archivedRecently, idle, beforeCutoff}},
		{registrystore.ConversationArchiveInclude, registrystore.ConversationAgeCreatedAt, []uuid.UUID{archivedOld, archivedRecently, idle, activeEntry, activeFork, parentGroup, childGroup, beforeCutoff}},
	}
	for _, tc := range cases {
		filter := registrystore.ConversationEvictionFilter{Archived: tc.archived, AgeBasis: tc.age, Cutoff: cutoff}
		require.NoError(t, filter.Validate())
		require.NoError(t, store.InReadTx(ctx, func(ctx context.Context) error {
			ids, err := store.FindEvictableGroupIDs(ctx, filter, 1000)
			require.NoError(t, err)
			require.Equal(t, sorted(tc.want), sorted(ids), "find %s/%s", tc.archived, tc.age)
			count, err := store.CountEvictableGroups(ctx, filter)
			require.NoError(t, err)
			require.EqualValues(t, len(tc.want), count, "count %s/%s", tc.archived, tc.age)

			// Restricting candidates keeps only the requested matches.
			restricted := filter
			restricted.GroupIDs = []uuid.UUID{all[2], all[3], all[8]}
			ids, err = store.FindEvictableGroupIDs(ctx, restricted, 1000)
			require.NoError(t, err)
			require.Equal(t, sorted(intersect(tc.want, restricted.GroupIDs)), sorted(ids), "restricted %s/%s", tc.archived, tc.age)
			if len(tc.want) > 1 {
				ids, err = store.FindEvictableGroupIDs(ctx, filter, 1)
				require.NoError(t, err)
				require.Len(t, ids, 1)
			}
			if tc.age == registrystore.ConversationAgeLastActivity {
				// Resuming one group at a time visits every match once, in ID order.
				var paged []uuid.UUID
				page := filter
				for {
					ids, err := store.FindEvictableGroupIDs(ctx, page, 1)
					require.NoError(t, err)
					if len(ids) == 0 {
						break
					}
					paged = append(paged, ids...)
					page.AfterGroupID = &ids[0]
				}
				require.Equal(t, sorted(tc.want), inOrder(paged), "paged %s/%s", tc.archived, tc.age)
				count, err := store.CountEvictableGroups(ctx, registrystore.ConversationEvictionFilter{
					Archived: filter.Archived, AgeBasis: filter.AgeBasis, Cutoff: cutoff, AfterGroupID: &paged[0],
				})
				require.NoError(t, err)
				require.EqualValues(t, len(tc.want)-1, count, "count after cursor %s/%s", tc.archived, tc.age)
			}
			return nil
		}))
	}

	// Deleting unarchived idle trees leaves trees with recent activity intact.
	filter := registrystore.ConversationEvictionFilter{
		Archived: registrystore.ConversationArchiveExclude,
		AgeBasis: registrystore.ConversationAgeLastActivity,
		Cutoff:   cutoff,
	}
	evictAll(t, ctx, store, filter)
	require.Equal(t, sorted([]uuid.UUID{archivedOld, archivedRecently, activeEntry, activeFork, parentGroup, childGroup, atCutoff}), sorted(existing(t, ctx, store, all)))

	// Once the child's fork goes idle, the parent's whole tree is evicted.
	clock.SetGroupActivity(t, ctx, childGroup, old)
	evictAll(t, ctx, store, filter)
	require.Equal(t, sorted([]uuid.UUID{archivedOld, archivedRecently, activeEntry, activeFork, atCutoff}), sorted(existing(t, ctx, store, all)))
}

// ConcurrentActivity checks that activity committed after eviction selected a
// group, but before it deleted the group, prevents the deletion. Use it only on
// datastores whose write transactions allow concurrent writers.
func ConcurrentActivity(t *testing.T, ctx context.Context, store registrystore.MemoryStore, clock Clock) {
	cutoff := Cutoff()
	old := cutoff.Add(-24 * time.Hour)
	filter := registrystore.ConversationEvictionFilter{
		Archived: registrystore.ConversationArchiveExclude,
		AgeBasis: registrystore.ConversationAgeLastActivity,
		Cutoff:   cutoff,
	}
	activities := map[string]func(f fixture, root *registrystore.ConversationDetail, entry model.Entry){
		"entry": func(f fixture, root *registrystore.ConversationDetail, _ model.Entry) {
			f.appendEntry(root.ID, nil)
		},
		"fork": func(f fixture, root *registrystore.ConversationDetail, entry model.Entry) {
			f.fork(root.ID, entry.ID)
		},
		"child": func(f fixture, root *registrystore.ConversationDetail, _ model.Entry) {
			f.appendEntry(uuid.NewString(), &root.ID)
		},
	}
	for name, activity := range activities {
		t.Run(name, func(t *testing.T) {
			f := fixture{t: t, ctx: ctx, store: store}
			root := f.create("concurrent " + name)
			entry := f.appendEntry(root.ID, nil)
			clock.SetGroupActivity(t, ctx, root.ConversationGroupID, old)

			racing := &raceStore{MemoryStore: store, race: func() {
				// Commit from another transaction while eviction's is open.
				done := make(chan struct{})
				go func() {
					defer close(done)
					activity(f, root, entry)
				}()
				<-done
			}}
			evictAll(t, ctx, racing, filter)
			require.Equal(t, []uuid.UUID{root.ConversationGroupID}, existing(t, ctx, store, []uuid.UUID{root.ConversationGroupID}))
		})
	}
}

// raceStore runs race once, right after eviction first selects candidates.
type raceStore struct {
	registrystore.MemoryStore
	race func()
}

func (s *raceStore) FindEvictableGroupIDs(ctx context.Context, filter registrystore.ConversationEvictionFilter, limit int) ([]uuid.UUID, error) {
	ids, err := s.MemoryStore.FindEvictableGroupIDs(ctx, filter, limit)
	if s.race != nil && err == nil && len(ids) > 0 {
		race := s.race
		s.race = nil
		race()
	}
	return ids, err
}

func evictAll(t *testing.T, ctx context.Context, store registrystore.MemoryStore, filter registrystore.ConversationEvictionFilter) {
	t.Helper()
	for {
		var batch eventstream.ConversationEvictionBatch
		require.NoError(t, store.InWriteTx(ctx, func(ctx context.Context) error {
			var err error
			batch, err = eventstream.EvictConversationGroups(ctx, store, filter, 2)
			return err
		}))
		if batch.Found == 0 {
			return
		}
		filter = batch.Next
	}
}

// existing returns the groups that still exist, using an always-true filter.
func existing(t *testing.T, ctx context.Context, store registrystore.MemoryStore, ids []uuid.UUID) []uuid.UUID {
	t.Helper()
	var result []uuid.UUID
	require.NoError(t, store.InReadTx(ctx, func(ctx context.Context) error {
		var err error
		result, err = store.FindEvictableGroupIDs(ctx, registrystore.ConversationEvictionFilter{
			Archived: registrystore.ConversationArchiveInclude,
			AgeBasis: registrystore.ConversationAgeCreatedAt,
			Cutoff:   time.Now().Add(time.Hour),
			GroupIDs: ids,
		}, len(ids))
		return err
	}))
	return result
}

func intersect(a, b []uuid.UUID) []uuid.UUID {
	in := map[uuid.UUID]bool{}
	for _, id := range b {
		in[id] = true
	}
	var result []uuid.UUID
	for _, id := range a {
		if in[id] {
			result = append(result, id)
		}
	}
	return result
}

func sorted(ids []uuid.UUID) []string {
	result := make([]string, len(ids))
	for i, id := range ids {
		result[i] = id.String()
	}
	sort.Strings(result)
	return result
}

func inOrder(ids []uuid.UUID) []string {
	result := make([]string, len(ids))
	for i, id := range ids {
		result[i] = id.String()
	}
	return result
}
