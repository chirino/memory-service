package mongo

import (
	"context"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/model"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestEntryDocOrderLessDistinguishesNullSeqFromZero(t *testing.T) {
	createdAt := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	zero := uint32(0)
	nullSeq := entryDoc{ID: "00000000-0000-0000-0000-0000000000ff", CreatedAt: createdAt}
	seqZero := entryDoc{ID: "00000000-0000-0000-0000-000000000001", CreatedAt: createdAt, Seq: &zero}

	assert.True(t, entryDocOrderLess(nullSeq, seqZero))
	assert.False(t, entryDocOrderLess(seqZero, nullSeq))
}

func TestVisibleContextFilterUsesAncestrySegmentPredicates(t *testing.T) {
	clientID := "test-client"
	filter, err := (&MongoStore{}).visibleContextFilter(
		context.Background(),
		convDoc{ID: "child", ConversationGroupID: "group-a"},
		[]forkAncestorDoc{{ConversationID: "child"}},
		&clientID,
	)
	require.NoError(t, err)

	assert.Equal(t, "group-a", filter["conversation_group_id"])
	assert.Equal(t, "child", filter["conversation_id"])
	assert.Equal(t, string(model.ChannelContext), filter["channel"])
	assert.Equal(t, clientID, filter["client_id"])
}

func TestGroupAllChannelFilterAppliesClientScopeAndEpoch(t *testing.T) {
	clientID := "test-client"
	filter := bson.M{"conversation_group_id": "group-a"}
	addMongoAllChannelScope(filter, &clientID, true)
	_, hasTopLevelOr := filter["$or"]
	require.False(t, hasTopLevelOr)
	andConditions, ok := filter["$and"].(bson.A)
	require.True(t, ok)
	require.Len(t, andConditions, 2)

	epoch := int64(2)
	_, err := (&MongoStore{}).applyMongoEpochFilterToBase(
		context.Background(),
		filter,
		&registrystore.MemoryEpochFilter{Mode: registrystore.MemoryEpochModeEpoch, Epoch: &epoch},
	)
	require.NoError(t, err)
	andConditions, ok = filter["$and"].(bson.A)
	require.True(t, ok)
	require.Len(t, andConditions, 3)
	assert.Contains(t, andConditions, mongoAllChannelEpochCondition(epoch))
}

func TestValidateConversationAncestryDocSizeRejectsOversizedDocument(t *testing.T) {
	store := &MongoStore{maxBSONDocumentSizeOverride: 180}
	doc := conversationAncestryDoc{
		ID:                  "child",
		ConversationGroupID: "group-a",
		Ancestors: []ancestryAncestorDoc{
			{ConversationID: "child", Depth: 0},
			{ConversationID: "parent-with-a-long-id", Depth: 1},
			{ConversationID: "root-with-a-long-id", Depth: 2},
		},
	}

	err := store.validateConversationAncestryDocSize(context.Background(), doc)

	require.Error(t, err)
	var validationErr *registrystore.ValidationError
	require.ErrorAs(t, err, &validationErr)
	assert.Equal(t, "forkedAtConversationId", validationErr.Field)
	assert.Contains(t, validationErr.Message, "conversation ancestry document exceeds MongoDB maximum BSON document size")
}
