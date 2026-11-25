package model

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestAttachmentAttemptIDIsNeverSerialized(t *testing.T) {
	attempt := uuid.New()
	data, err := json.Marshal(Attachment{ID: uuid.New(), ContentType: "text/plain", UserID: "user1", Status: "uploading", AttemptID: &attempt})
	require.NoError(t, err)

	var fields map[string]any
	require.NoError(t, json.Unmarshal(data, &fields))
	for name, value := range fields {
		require.NotContains(t, name, "attempt", "attempt ID must not appear as a JSON field")
		require.NotEqual(t, attempt.String(), value, "attempt ID value must not leak under any field")
	}

	var decoded Attachment
	require.NoError(t, json.Unmarshal([]byte(`{"attemptId":"`+attempt.String()+`","attempt_id":"`+attempt.String()+`"}`), &decoded))
	require.Nil(t, decoded.AttemptID, "callers must not be able to set the attempt ID through JSON")
}
