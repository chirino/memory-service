package store

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chirino/memory-service/internal/model"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// The admin attachment endpoints serialize AdminAttachment directly, so the
// internal attempt ID must stay out of that JSON as well as the model's.
func TestAdminAttachmentJSONOmitsAttemptID(t *testing.T) {
	attempt := uuid.New()
	data, err := json.Marshal(AdminAttachment{
		Attachment: model.Attachment{ID: uuid.New(), ContentType: "text/plain", UserID: "user1", Status: "uploading", AttemptID: &attempt},
		RefCount:   1,
	})
	require.NoError(t, err)

	var fields map[string]any
	require.NoError(t, json.Unmarshal(data, &fields))
	require.Contains(t, fields, "refCount", "the response must still carry the embedded and admin fields")
	require.Contains(t, fields, "status")
	for name, value := range fields {
		require.NotContains(t, strings.ToLower(name), "attempt", "no attempt field may be serialized")
		require.NotEqual(t, attempt.String(), value, "the attempt ID value must not leak under %q", name)
	}
	require.NotContains(t, string(data), attempt.String())
}
