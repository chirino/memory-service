package store

import (
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/model"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestBuildForkNavigationSelectsVisibleContinuation(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	originalID, forkID := uuid.New(), uuid.New()
	parent := "root"
	records := []ForkNavigationConversation{
		{ID: parent, Title: "Root", CreatedAt: base},
		{ID: "fork", Title: "Fork", ForkedAtConversationID: &parent, ForkedAtEntryID: &originalID, FirstEntryID: &forkID, FirstEntryCreatedAt: timePtr(base.Add(2 * time.Second)), CreatedAt: base.Add(time.Second)},
	}
	entries := map[uuid.UUID]model.Entry{
		originalID: {ID: originalID, ConversationID: parent, Channel: model.ChannelHistory, CreatedAt: base.Add(time.Second)},
		forkID:     {ID: forkID, ConversationID: "fork", Channel: model.ChannelHistory, CreatedAt: base.Add(2 * time.Second)},
	}

	rootNavigation, err := BuildForkNavigation(records, []model.ConversationAncestry{{AncestorConversationID: parent, Depth: 0}}, entries)
	require.NoError(t, err)
	require.Equal(t, []string{"root", "fork"}, rootNavigation.ConversationIDs)
	require.Len(t, rootNavigation.ForkPoints, 1)
	require.Equal(t, originalID, rootNavigation.ForkPoints[0].EntryID)

	forkNavigation, err := BuildForkNavigation(records, []model.ConversationAncestry{
		{AncestorConversationID: "fork", Depth: 0},
		{AncestorConversationID: parent, Depth: 1, BeforeEntryID: &originalID},
	}, entries)
	require.NoError(t, err)
	require.Len(t, forkNavigation.ForkPoints, 1)
	require.Equal(t, forkID, forkNavigation.ForkPoints[0].EntryID)
	require.Len(t, forkNavigation.ForkPoints[0].Options, 2)
}

func TestBuildForkNavigationExcludesForksAfterSelectedBoundary(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	selectedAnchor, laterAnchor, selectedEntry := uuid.New(), uuid.New(), uuid.New()
	parent := "root"
	records := []ForkNavigationConversation{
		{ID: parent, Title: "Root", CreatedAt: base},
		{ID: "selected", Title: "Selected", ForkedAtConversationID: &parent, ForkedAtEntryID: &selectedAnchor, FirstEntryID: &selectedEntry, FirstEntryCreatedAt: timePtr(base.Add(2 * time.Second)), CreatedAt: base.Add(time.Second)},
		{ID: "later", Title: "Later", ForkedAtConversationID: &parent, ForkedAtEntryID: &laterAnchor, CreatedAt: base.Add(4 * time.Second)},
	}
	entries := map[uuid.UUID]model.Entry{
		selectedAnchor: {ID: selectedAnchor, ConversationID: parent, Channel: model.ChannelHistory, CreatedAt: base.Add(time.Second)},
		selectedEntry:  {ID: selectedEntry, ConversationID: "selected", Channel: model.ChannelHistory, CreatedAt: base.Add(2 * time.Second)},
		laterAnchor:    {ID: laterAnchor, ConversationID: parent, Channel: model.ChannelHistory, CreatedAt: base.Add(3 * time.Second)},
	}
	navigation, err := BuildForkNavigation(records, []model.ConversationAncestry{
		{AncestorConversationID: "selected", Depth: 0},
		{AncestorConversationID: parent, Depth: 1, BeforeEntryID: &selectedAnchor},
	}, entries)
	require.NoError(t, err)
	require.Len(t, navigation.ForkPoints, 1)
	require.Equal(t, selectedEntry, navigation.ForkPoints[0].EntryID)
}

func timePtr(value time.Time) *time.Time { return &value }
