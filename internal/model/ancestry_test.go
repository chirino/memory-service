package model

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestBuildConversationAncestrySegmentsRoot(t *testing.T) {
	rows, err := BuildConversationAncestrySegments("root", nil, nil, nil)

	require.NoError(t, err)
	require.Equal(t, []ConversationAncestrySegment{{ConversationID: "root", Depth: 0}}, rows)
}

func TestBuildConversationAncestrySegmentsInheritedAnchor(t *testing.T) {
	rootBoundary := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	anchor := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	anchorOwnerDepth := 1
	parentRows := []ConversationAncestrySegment{
		{ConversationID: "child", Depth: 0},
		{ConversationID: "root", Depth: 1, BeforeEntryID: &rootBoundary},
	}

	rows, err := BuildConversationAncestrySegments("grandchild", parentRows, &anchor, &anchorOwnerDepth)

	require.NoError(t, err)
	require.Len(t, rows, 3)
	require.Equal(t, ConversationAncestrySegment{ConversationID: "grandchild", Depth: 0}, rows[0])
	require.Equal(t, "child", rows[1].ConversationID)
	require.Equal(t, 1, rows[1].Depth)
	require.Nil(t, rows[1].BeforeEntryID)
	require.Equal(t, &anchor, rows[1].ForkedAtEntryID)
	require.Equal(t, "root", rows[2].ConversationID)
	require.Equal(t, 2, rows[2].Depth)
	require.Equal(t, &anchor, rows[2].BeforeEntryID)
	require.Nil(t, rows[2].ForkedAtEntryID)
}
