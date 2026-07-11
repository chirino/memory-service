package store

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/chirino/memory-service/internal/model"
	"github.com/google/uuid"
)

// ForkEntryPreview extracts a short text label from a decrypted entry content
// array without exposing the complete entry payload in fork navigation.
func ForkEntryPreview(content []byte) *string {
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &parts) != nil {
		return nil
	}
	for _, part := range parts {
		if part.Text == "" {
			continue
		}
		text := part.Text
		if len(text) > 200 {
			text = text[:200]
		}
		return &text
	}
	return nil
}

// BuildForkNavigation assembles the UI navigation index from a conversation
// group, the requested conversation's ancestry rows, and the fork-anchor
// entries. Ancestry rows use the same exclusive BeforeEntryID semantics as
// visible entry listing.
func BuildForkNavigation(conversations []ForkNavigationConversation, ancestry []model.ConversationAncestry, anchors map[uuid.UUID]model.Entry) (*ConversationForkNavigation, error) {
	result := &ConversationForkNavigation{ConversationIDs: make([]string, 0, len(conversations)), ForkPoints: []ConversationForkPoint{}}
	byID := make(map[string]ForkNavigationConversation, len(conversations))
	for _, conversation := range conversations {
		result.ConversationIDs = append(result.ConversationIDs, conversation.ID)
		byID[conversation.ID] = conversation
	}
	sort.Slice(result.ConversationIDs, func(i, j int) bool {
		a, b := byID[result.ConversationIDs[i]], byID[result.ConversationIDs[j]]
		if a.CreatedAt.Equal(b.CreatedAt) {
			return a.ID < b.ID
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})

	path := make(map[string]model.ConversationAncestry, len(ancestry))
	for _, row := range ancestry {
		path[row.AncestorConversationID] = row
	}

	type pointKey struct {
		parent string
		anchor uuid.UUID
	}
	children := map[pointKey][]ForkNavigationConversation{}
	for _, conversation := range conversations {
		if conversation.ForkedAtConversationID == nil || conversation.ForkedAtEntryID == nil {
			continue
		}
		key := pointKey{parent: *conversation.ForkedAtConversationID, anchor: *conversation.ForkedAtEntryID}
		children[key] = append(children[key], conversation)
	}

	type rankedPoint struct {
		point     ConversationForkPoint
		depth     int
		createdAt time.Time
	}
	points := make([]rankedPoint, 0, len(children))
	for key, alternatives := range children {
		parentPath, parentVisible := path[key.parent]
		anchor, anchorFound := anchors[key.anchor]
		if !parentVisible || !anchorFound || anchor.Channel != model.ChannelHistory {
			continue
		}

		activeEntryID := &key.anchor
		activeDepth := parentPath.Depth
		if parentPath.BeforeEntryID != nil {
			boundary, ok := anchors[*parentPath.BeforeEntryID]
			if !ok {
				return nil, fmt.Errorf("fork boundary entry %s not found", parentPath.BeforeEntryID.String())
			}
			cmp := compareForkEntries(anchor, boundary)
			if cmp > 0 {
				continue
			}
			if cmp == 0 {
				activeEntryID = nil
				for _, alternative := range alternatives {
					if _, selected := path[alternative.ID]; selected {
						activeEntryID = alternative.FirstEntryID
						activeDepth = path[alternative.ID].Depth
						break
					}
				}
				if activeEntryID == nil {
					continue
				}
			}
		}

		parent := byID[key.parent]
		options := []ConversationForkOption{{ConversationID: parent.ID, EntryID: &key.anchor, Title: parent.Title, Preview: ForkEntryPreview(anchor.Content), CreatedAt: anchor.CreatedAt}}
		for _, alternative := range alternatives {
			createdAt := alternative.CreatedAt
			if alternative.FirstEntryCreatedAt != nil {
				createdAt = *alternative.FirstEntryCreatedAt
			}
			options = append(options, ConversationForkOption{ConversationID: alternative.ID, EntryID: alternative.FirstEntryID, Title: alternative.Title, Preview: alternative.FirstEntryPreview, CreatedAt: createdAt})
		}
		sort.Slice(options, func(i, j int) bool {
			if options[i].CreatedAt.Equal(options[j].CreatedAt) {
				return options[i].ConversationID < options[j].ConversationID
			}
			return options[i].CreatedAt.Before(options[j].CreatedAt)
		})
		activeEntry := anchors[*activeEntryID]
		points = append(points, rankedPoint{point: ConversationForkPoint{EntryID: *activeEntryID, Options: options}, depth: activeDepth, createdAt: activeEntry.CreatedAt})
	}

	sort.Slice(points, func(i, j int) bool {
		// Larger ancestry depth is earlier in the visible path.
		if points[i].depth != points[j].depth {
			return points[i].depth > points[j].depth
		}
		if points[i].createdAt.Equal(points[j].createdAt) {
			return points[i].point.EntryID.String() < points[j].point.EntryID.String()
		}
		return points[i].createdAt.Before(points[j].createdAt)
	})
	for _, point := range points {
		result.ForkPoints = append(result.ForkPoints, point.point)
	}
	return result, nil
}

func compareForkEntries(a, b model.Entry) int {
	if a.CreatedAt.Before(b.CreatedAt) {
		return -1
	}
	if a.CreatedAt.After(b.CreatedAt) {
		return 1
	}
	if a.Seq != nil && b.Seq != nil {
		if *a.Seq < *b.Seq {
			return -1
		}
		if *a.Seq > *b.Seq {
			return 1
		}
	} else if a.Seq == nil && b.Seq != nil {
		return -1
	} else if a.Seq != nil && b.Seq == nil {
		return 1
	}
	if a.ID.String() < b.ID.String() {
		return -1
	}
	if a.ID.String() > b.ID.String() {
		return 1
	}
	return 0
}
