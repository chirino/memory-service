package store

import "github.com/google/uuid"

// ExpandConversationGroupDeletion follows started-child relationships through
// every conversation in each group, including forks. Each group is visited once.
// children must keep the visited groups stable until deletion when the datastore
// supports transactions, so a concurrent child cannot escape the event snapshot.
func ExpandConversationGroupDeletion(roots []uuid.UUID, children func([]uuid.UUID) ([]uuid.UUID, error)) ([]uuid.UUID, error) {
	seen := make(map[uuid.UUID]bool)
	var result []uuid.UUID
	for pending := roots; len(pending) > 0; {
		var frontier []uuid.UUID
		for _, id := range pending {
			if !seen[id] {
				seen[id] = true
				frontier = append(frontier, id)
			}
		}
		if len(frontier) == 0 {
			break
		}
		result = append(result, frontier...)
		var err error
		pending, err = children(frontier)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}
