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

	rootNavigation, err := BuildForkNavigation(records, []model.ConversationAncestry{{AncestorConversationID: parent, Depth: 0}}, entries, ForkNavigationVisibility{})
	require.NoError(t, err)
	require.Equal(t, []string{"root", "fork"}, rootNavigation.ConversationIDs)
	require.Len(t, rootNavigation.ForkPoints, 1)
	require.Equal(t, originalID, rootNavigation.ForkPoints[0].EntryID)

	forkNavigation, err := BuildForkNavigation(records, []model.ConversationAncestry{
		{AncestorConversationID: "fork", Depth: 0},
		{AncestorConversationID: parent, Depth: 1, BeforeEntryID: &originalID},
	}, entries, ForkNavigationVisibility{})
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
	}, entries, ForkNavigationVisibility{})
	require.NoError(t, err)
	require.Len(t, navigation.ForkPoints, 1)
	require.Equal(t, selectedEntry, navigation.ForkPoints[0].EntryID)
}

func TestBuildForkNavigationScopesJournalAnchorsByClient(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	anchorID, forkEntryID := uuid.New(), uuid.New()
	parent, clientA, clientB := "root", "agent-a", "agent-b"
	records := []ForkNavigationConversation{
		{ID: parent, Title: "Root", CreatedAt: base},
		{ID: "fork", Title: "Fork", ForkedAtConversationID: &parent, ForkedAtEntryID: &anchorID, FirstEntryID: &forkEntryID, FirstEntryCreatedAt: timePtr(base.Add(2 * time.Second)), CreatedAt: base.Add(time.Second)},
	}
	entries := map[uuid.UUID]model.Entry{
		anchorID:    {ID: anchorID, ConversationID: parent, Channel: model.ChannelJournal, ClientID: &clientA, CreatedAt: base.Add(time.Second)},
		forkEntryID: {ID: forkEntryID, ConversationID: "fork", Channel: model.ChannelHistory, CreatedAt: base.Add(2 * time.Second)},
	}
	ancestry := []model.ConversationAncestry{{AncestorConversationID: parent, Depth: 0}}

	withoutClient, err := BuildForkNavigation(records, ancestry, entries, ForkNavigationVisibility{})
	require.NoError(t, err)
	require.Empty(t, withoutClient.ForkPoints)

	otherClient, err := BuildForkNavigation(records, ancestry, entries, ForkNavigationVisibility{ClientID: &clientB})
	require.NoError(t, err)
	require.Empty(t, otherClient.ForkPoints)

	sameClient, err := BuildForkNavigation(records, ancestry, entries, ForkNavigationVisibility{ClientID: &clientA})
	require.NoError(t, err)
	require.Len(t, sameClient.ForkPoints, 1)
	require.Equal(t, anchorID, sameClient.ForkPoints[0].EntryID)

	admin, err := BuildForkNavigation(records, ancestry, entries, ForkNavigationVisibility{IncludeAllJournals: true})
	require.NoError(t, err)
	require.Len(t, admin.ForkPoints, 1)
	require.Equal(t, anchorID, admin.ForkPoints[0].EntryID)
}

func TestBuildForkNavigationSelectsJournalContinuation(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	anchorID, forkEntryID := uuid.New(), uuid.New()
	parent, clientID := "root", "agent-a"
	records := []ForkNavigationConversation{
		{ID: parent, Title: "Root", CreatedAt: base},
		{ID: "fork", Title: "Fork", ForkedAtConversationID: &parent, ForkedAtEntryID: &anchorID, FirstEntryID: &forkEntryID, FirstEntryCreatedAt: timePtr(base.Add(2 * time.Second)), CreatedAt: base.Add(time.Second)},
	}
	entries := map[uuid.UUID]model.Entry{
		anchorID:    {ID: anchorID, ConversationID: parent, Channel: model.ChannelJournal, ClientID: &clientID, CreatedAt: base.Add(time.Second)},
		forkEntryID: {ID: forkEntryID, ConversationID: "fork", Channel: model.ChannelJournal, ClientID: &clientID, CreatedAt: base.Add(2 * time.Second)},
	}
	navigation, err := BuildForkNavigation(records, []model.ConversationAncestry{
		{AncestorConversationID: "fork", Depth: 0},
		{AncestorConversationID: parent, Depth: 1, BeforeEntryID: &anchorID},
	}, entries, ForkNavigationVisibility{ClientID: &clientID})
	require.NoError(t, err)
	require.Len(t, navigation.ForkPoints, 1)
	require.Equal(t, forkEntryID, navigation.ForkPoints[0].EntryID)
}

func TestBuildForkNavigationRejectsContextAnchor(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	anchorID, forkEntryID := uuid.New(), uuid.New()
	parent := "root"
	records := []ForkNavigationConversation{
		{ID: parent, Title: "Root", CreatedAt: base},
		{ID: "fork", Title: "Fork", ForkedAtConversationID: &parent, ForkedAtEntryID: &anchorID, FirstEntryID: &forkEntryID, CreatedAt: base.Add(time.Second)},
	}
	entries := map[uuid.UUID]model.Entry{
		anchorID:    {ID: anchorID, ConversationID: parent, Channel: model.ChannelContext, CreatedAt: base.Add(time.Second)},
		forkEntryID: {ID: forkEntryID, ConversationID: "fork", Channel: model.ChannelHistory, CreatedAt: base.Add(2 * time.Second)},
	}
	navigation, err := BuildForkNavigation(records, []model.ConversationAncestry{{AncestorConversationID: parent, Depth: 0}}, entries, ForkNavigationVisibility{IncludeAllJournals: true})
	require.NoError(t, err)
	require.Empty(t, navigation.ForkPoints)
}

func timePtr(value time.Time) *time.Time { return &value }
