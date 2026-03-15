package infinispan

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// buildVectorSearchQuery constructs an Ickle query for vector similarity search.
// Query format: SELECT i, score(i) FROM VectorItem{dimension} i
//
//	WHERE i.conversation_group_id IN (...) AND i.embedding <-> [vector] ~ k
func buildVectorSearchQuery(embedding []float32, conversationGroupIDs []uuid.UUID, limit, dimension int) string {
	entityName := fmt.Sprintf("VectorItem%d", dimension)

	// Convert embedding to string representation
	vectorStr := floatSliceToString(embedding)

	// Build conversation group ID filter
	groupFilter := buildGroupIDFilter(conversationGroupIDs)

	// Construct Ickle query with vector similarity and group filtering
	// Note: Infinispan requires the filter BEFORE the vector search operator
	query := fmt.Sprintf(
		"SELECT i, score(i) FROM %s i WHERE %s AND i.embedding <-> %s ~ %d",
		entityName,
		groupFilter,
		vectorStr,
		limit,
	)

	return query
}

// buildDeleteByGroupQuery constructs an Ickle query to delete entries by conversation group ID.
func buildDeleteByGroupQuery(conversationGroupID uuid.UUID, dimension int) string {
	entityName := fmt.Sprintf("VectorItem%d", dimension)

	query := fmt.Sprintf(
		"FROM %s i WHERE i.conversation_group_id = '%s'",
		entityName,
		conversationGroupID.String(),
	)

	return query
}

// buildGroupIDFilter creates an IN clause for conversation group IDs.
func buildGroupIDFilter(conversationGroupIDs []uuid.UUID) string {
	if len(conversationGroupIDs) == 1 {
		return fmt.Sprintf("i.conversation_group_id = '%s'", conversationGroupIDs[0].String())
	}

	// Build IN clause for multiple IDs
	ids := make([]string, len(conversationGroupIDs))
	for i, id := range conversationGroupIDs {
		ids[i] = fmt.Sprintf("'%s'", id.String())
	}

	return fmt.Sprintf("i.conversation_group_id IN (%s)", strings.Join(ids, ", "))
}

// floatSliceToString converts a float32 slice to Ickle array format.
// Example: [0.1, 0.2, 0.3] -> "[0.1, 0.2, 0.3]"
func floatSliceToString(floats []float32) string {
	if len(floats) == 0 {
		return "[]"
	}

	var sb strings.Builder
	sb.WriteString("[")

	for i, f := range floats {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(fmt.Sprintf("%f", f))
	}

	sb.WriteString("]")
	return sb.String()
}
