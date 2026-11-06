package store

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestScanIdleEvictionCandidates(t *testing.T) {
	// Candidates are ordered; every third one has recent activity.
	candidates := make([]uuid.UUID, 2*EvictionScanChunk+7)
	active := map[uuid.UUID]bool{}
	for i := range candidates {
		candidates[i] = uuid.UUID{byte(i >> 8), byte(i)}
		if i%3 == 0 {
			active[candidates[i]] = true
		}
	}
	var idleWant []uuid.UUID
	for _, id := range candidates {
		if !active[id] {
			idleWant = append(idleWant, id)
		}
	}

	scan := func(after *uuid.UUID, limit int) (visited []uuid.UUID, pages, walked int) {
		filter := ConversationEvictionFilter{Archived: ConversationArchiveInclude, AgeBasis: ConversationAgeLastActivity, Cutoff: time.Now(), AfterGroupID: after}
		require.NoError(t, ScanIdleEvictionCandidates(filter, limit,
			func(after *uuid.UUID, n int) ([]uuid.UUID, error) {
				pages++
				start := 0
				if after != nil {
					for start < len(candidates) && string(candidates[start][:]) <= string(after[:]) {
						start++
					}
				}
				end := min(start+n, len(candidates))
				return candidates[start:end], nil
			},
			func(ids []uuid.UUID) ([]uuid.UUID, error) {
				require.LessOrEqual(t, len(ids), EvictionScanChunk)
				walked += len(ids)
				var idle []uuid.UUID
				for _, id := range ids {
					if !active[id] {
						idle = append(idle, id)
					}
				}
				return idle, nil
			},
			func(id uuid.UUID) { visited = append(visited, id) }))
		return visited, pages, walked
	}

	visited, pages, walked := scan(nil, 0)
	require.Equal(t, idleWant, visited, "unlimited scan visits every idle candidate in order")
	require.Equal(t, 3, pages)
	require.Equal(t, len(candidates), walked)

	visited, pages, walked = scan(nil, 10)
	require.Equal(t, idleWant[:10], visited)
	require.Equal(t, 1, pages, "a limited scan stops after the page that fills it")
	require.Equal(t, EvictionScanChunk, walked)

	// Resuming after the last visited group continues where the batch stopped.
	visited, _, _ = scan(&idleWant[9], 5)
	require.Equal(t, idleWant[10:15], visited)

	visited, pages, _ = scan(&candidates[len(candidates)-1], 0)
	require.Empty(t, visited)
	require.Equal(t, 1, pages)
}

func TestConversationEvictionFilterCursorRequiresLastActivity(t *testing.T) {
	id := uuid.New()
	filter := DefaultConversationEvictionFilter(time.Now())
	filter.AfterGroupID = &id
	require.Error(t, filter.Validate())
	filter.AgeBasis = ConversationAgeLastActivity
	require.NoError(t, filter.Validate())
}
