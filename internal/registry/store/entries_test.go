package store

import (
	"testing"

	"github.com/chirino/memory-service/internal/model"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEpochForChannelOnlySetsContextEpoch(t *testing.T) {
	epoch := int64(7)

	assert.Nil(t, EpochForChannel(model.ChannelHistory, &epoch))
	assert.Nil(t, EpochForChannel(model.ChannelJournal, &epoch))
	require.NotNil(t, EpochForChannel(model.ChannelContext, &epoch))
	assert.Equal(t, epoch, *EpochForChannel(model.ChannelContext, &epoch))
	assert.Equal(t, int64(1), *EpochForChannel(model.ChannelContext, nil))
}

func TestPaginateEntriesRejectsUnknownAfterCursor(t *testing.T) {
	entries := []model.Entry{{ID: uuid.New()}, {ID: uuid.New()}}
	missing := uuid.NewString()

	_, _, _, err := PaginateEntries(entries, &missing, nil, false, 1)
	require.EqualError(t, err, "afterCursor entry not found in visible results")
}

func TestValidateEntryEpochChannelsRejectsNonContextEntries(t *testing.T) {
	epoch := int64(2)

	err := ValidateEntryEpochChannels([]CreateEntryRequest{{Channel: "history"}}, &epoch)
	require.EqualError(t, err, `epoch can only be set for context entries; entry channel "history" does not support epochs`)

	err = ValidateEntryEpochChannels([]CreateEntryRequest{{Channel: "journal"}}, &epoch)
	require.EqualError(t, err, `epoch can only be set for context entries; entry channel "journal" does not support epochs`)

	require.NoError(t, ValidateEntryEpochChannels([]CreateEntryRequest{{Channel: "context"}}, &epoch))
	require.NoError(t, ValidateEntryEpochChannels([]CreateEntryRequest{{Channel: "history"}}, nil))
}

func TestEntryLookupQueriesAreBoundedToTarget(t *testing.T) {
	entryID := uuid.New()
	channel := model.ChannelHistory
	clientID := "client-1"

	query := EntryLookupQuery(entryID, &channel, &clientID)
	require.Equal(t, 1, query.Limit)
	require.True(t, query.Tail)
	require.True(t, query.AllForks)
	require.Equal(t, entryID.String(), requireStringValue(t, query.UpToEntryID))
	require.Equal(t, channel, *query.Channel)
	require.Equal(t, clientID, *query.ClientID)

	adminQuery := AdminEntryLookupQuery(entryID)
	require.Equal(t, 1, adminQuery.Limit)
	require.True(t, adminQuery.Tail)
	require.True(t, adminQuery.AllForks)
	require.Equal(t, entryID.String(), requireStringValue(t, adminQuery.UpToEntryID))
}

func requireStringValue(t *testing.T, value *string) string {
	t.Helper()
	require.NotNil(t, value)
	return *value
}
