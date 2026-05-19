package store

import (
	"github.com/chirino/memory-service/internal/model"
)

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
