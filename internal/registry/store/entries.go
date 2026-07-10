package store

import (
	"fmt"
	"strings"

	"github.com/chirino/memory-service/internal/model"
	"github.com/google/uuid"
)

// EpochForChannel applies epoch semantics to an entry. Epochs belong only to
// context entries; context defaults to epoch 1 when the request omits one.
func EpochForChannel(channel model.Channel, requested *int64) *int64 {
	if channel != model.ChannelContext {
		return nil
	}
	if requested != nil {
		return requested
	}
	one := int64(1)
	return &one
}

// ValidateEntryEpochChannels rejects request-level epochs on batches containing
// entries from channels where epochs have no meaning.
func ValidateEntryEpochChannels(entries []CreateEntryRequest, epoch *int64) error {
	if epoch == nil {
		return nil
	}
	for _, entry := range entries {
		channel := model.Channel(strings.ToLower(entry.Channel))
		if channel == "" {
			channel = model.ChannelHistory
		}
		if channel != model.ChannelContext {
			return fmt.Errorf("epoch can only be set for context entries; entry channel %q does not support epochs", channel)
		}
	}
	return nil
}

// EntryLookupQuery builds the bounded listing query used when an entry must be
// loaded through normal conversation visibility rules.
func EntryLookupQuery(entryID uuid.UUID, channel *model.Channel, clientID *string) EntryListQuery {
	entryIDString := entryID.String()
	return EntryListQuery{
		Limit:       1,
		Channel:     channel,
		ClientID:    clientID,
		AllForks:    true,
		UpToEntryID: &entryIDString,
		Tail:        true,
	}
}

// AdminEntryLookupQuery builds the equivalent bounded admin lookup.
func AdminEntryLookupQuery(entryID uuid.UUID) AdminMessageQuery {
	entryIDString := entryID.String()
	return AdminMessageQuery{
		Limit:       1,
		AllForks:    true,
		UpToEntryID: &entryIDString,
		Tail:        true,
	}
}

// TrimEntriesToVisiblePrefix keeps entries that are part of the visible prefix
// ending at upToEntryID. The visible slice should already reflect the caller's
// fork visibility, while entries may have additional channel or epoch filters.
func TrimEntriesToVisiblePrefix(entries []model.Entry, visible []model.Entry, upToEntryID *string) ([]model.Entry, error) {
	if upToEntryID == nil || *upToEntryID == "" {
		return entries, nil
	}

	visibleIDs := make(map[string]struct{})
	found := false
	for _, entry := range visible {
		id := entry.ID.String()
		visibleIDs[id] = struct{}{}
		if id == *upToEntryID {
			found = true
			break
		}
	}
	if !found {
		return nil, &NotFoundError{Resource: "entry", ID: *upToEntryID}
	}

	filtered := entries[:0]
	for _, entry := range entries {
		if _, ok := visibleIDs[entry.ID.String()]; ok {
			filtered = append(filtered, entry)
		}
	}
	return filtered, nil
}

// PaginateEntries applies bidirectional pagination to a fully-filtered ascending
// entry slice and returns (page, afterCursor, beforeCursor, err).
//
//   - tail=true: return the last limit entries (page[len-limit:]).
//   - beforeCursor set: return up to limit entries strictly before the anchor.
//   - afterCursor set: return the first limit entries strictly after the anchor.
//   - otherwise: return the first limit entries.
//
// The returned page is always in ascending (chronological) order.
// afterCursor is the ID of the last entry when a newer entry exists, nil otherwise.
// beforeCursor is the ID of the first entry when an older entry exists, nil otherwise.
func PaginateEntries(
	entries []model.Entry,
	afterEntryID *string,
	beforeEntryID *string,
	tail bool,
	limit int,
) (page []model.Entry, afterCursor, beforeCursor *string, err error) {
	if limit <= 0 {
		limit = 50
	}

	n := len(entries)
	if n == 0 {
		return []model.Entry{}, nil, nil, nil
	}

	if tail {
		// Return the last `limit` entries.
		start := n - limit
		if start < 0 {
			start = 0
		}
		page = entries[start:]
		if start > 0 {
			c := entries[start].ID.String()
			beforeCursor = &c
		}
		// afterCursor is nil (this is the newest page).
		return page, nil, beforeCursor, nil
	}

	if beforeEntryID != nil {
		// Find the anchor position.
		anchorIdx := -1
		for i, e := range entries {
			if e.ID.String() == *beforeEntryID {
				anchorIdx = i
				break
			}
		}
		if anchorIdx < 0 {
			return nil, nil, nil, fmt.Errorf("beforeCursor entry not found in visible results")
		}
		// Take the `limit` entries ending just before the anchor.
		end := anchorIdx // exclusive
		start := end - limit
		if start < 0 {
			start = 0
		}
		page = entries[start:end]
		if len(page) == 0 {
			return []model.Entry{}, nil, nil, nil
		}
		// beforeCursor: there are older entries if start > 0.
		if start > 0 {
			c := entries[start].ID.String()
			beforeCursor = &c
		}
		// afterCursor: there are newer entries (the anchor page and beyond).
		if anchorIdx < n {
			c := entries[end-1].ID.String()
			afterCursor = &c
		}
		return page, afterCursor, beforeCursor, nil
	}

	// Forward pagination (afterCursor or from the beginning).
	start := 0
	if afterEntryID != nil {
		found := false
		for i, e := range entries {
			if e.ID.String() == *afterEntryID {
				start = i + 1
				found = true
				break
			}
		}
		if !found {
			return nil, nil, nil, fmt.Errorf("afterCursor entry not found in visible results")
		}
	}
	if start >= n {
		return []model.Entry{}, nil, nil, nil
	}
	end := start + limit
	if end > n {
		end = n
	}
	page = entries[start:end]
	// afterCursor: there are newer entries if end < n.
	if end < n && len(page) > 0 {
		c := page[len(page)-1].ID.String()
		afterCursor = &c
	}
	// beforeCursor: there are older entries if start > 0.
	if start > 0 && len(page) > 0 {
		c := page[0].ID.String()
		beforeCursor = &c
	}
	return page, afterCursor, beforeCursor, nil
}
