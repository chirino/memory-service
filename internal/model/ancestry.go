package model

import (
	"fmt"

	"github.com/google/uuid"
)

// ConversationAncestrySegment is the datastore-neutral ancestry row shape used
// to construct a descendant's materialized fork path before mapping it back to a
// store-specific representation.
type ConversationAncestrySegment struct {
	ConversationID  string
	Depth           int
	BeforeEntryID   *uuid.UUID
	ForkedAtEntryID *uuid.UUID
}

// BuildConversationAncestrySegments returns the complete ancestry path for a
// new conversation. It always includes the depth-0 self segment. When parentRows
// is non-empty, it copies the parent's already-materialized closure, increments
// depths, records the direct fork anchor on the copied parent self row, and
// applies the exclusive boundary to the ancestor segment that owns the anchor.
func BuildConversationAncestrySegments(convID string, parentRows []ConversationAncestrySegment, forkedAtEntryID *uuid.UUID, anchorOwnerDepth *int) ([]ConversationAncestrySegment, error) {
	rows := []ConversationAncestrySegment{{
		ConversationID: convID,
		Depth:          0,
	}}
	if len(parentRows) == 0 {
		return rows, nil
	}

	for _, parentRow := range parentRows {
		if parentRow.ConversationID == "" {
			return nil, fmt.Errorf("parent conversation ancestry contains an empty conversation id")
		}
		beforeEntryID := parentRow.BeforeEntryID
		if forkedAtEntryID != nil && anchorOwnerDepth != nil {
			switch {
			case parentRow.Depth < *anchorOwnerDepth:
				beforeEntryID = nil
			case parentRow.Depth == *anchorOwnerDepth:
				beforeEntryID = forkedAtEntryID
			}
		}
		var directForkedAtEntryID *uuid.UUID
		if parentRow.Depth == 0 {
			directForkedAtEntryID = forkedAtEntryID
		}
		rows = append(rows, ConversationAncestrySegment{
			ConversationID:  parentRow.ConversationID,
			Depth:           parentRow.Depth + 1,
			BeforeEntryID:   beforeEntryID,
			ForkedAtEntryID: directForkedAtEntryID,
		})
	}
	return rows, nil
}
