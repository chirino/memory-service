package episodicqdrant

import (
	"context"
	"strings"
	"testing"

	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	"github.com/google/uuid"
	pb "github.com/qdrant/go-client/qdrant"
	"github.com/stretchr/testify/require"
)

// Policy attributes must be stored as a nested policy_attributes object so that
// Qdrant filter keys such as "policy_attributes.topic" match. Dotted literal
// top-level keys would never match those nested-path filters.
func TestUpsertMemoryVectorsStoresPolicyAttributesAsNestedObject(t *testing.T) {
	fake := &waitPointsClient{status: pb.UpdateStatus_Completed}
	client := &Client{points: fake, collectionName: "test"}

	require.NoError(t, client.UpsertMemoryVectors(context.Background(), []registryepisodic.MemoryVectorUpsert{{
		MemoryID:   uuid.New(),
		FieldName:  "body",
		Embedding:  []float32{1},
		MemoryKind: "default/v1",
		PolicyAttributes: map[string]interface{}{
			"topic":    "billing",
			"priority": 2,
		},
	}}))

	require.NotNil(t, fake.upsert)
	require.Len(t, fake.upsert.Points, 1)
	payload := fake.upsert.Points[0].GetPayload()

	for key := range payload {
		require.False(t, strings.HasPrefix(key, "policy_attributes."), "unexpected dotted payload key %q", key)
	}

	attrs := payload["policy_attributes"].GetStructValue()
	require.NotNil(t, attrs, "policy_attributes must be a nested object")
	require.Equal(t, "billing", attrs.GetFields()["topic"].GetStringValue())
	require.Equal(t, int64(2), attrs.GetFields()["priority"].GetIntegerValue())
}
