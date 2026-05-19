package runtime

import (
	"encoding/json"
	"errors"
)

// ErrCheckpointNotFound marks a missing checkpoint.
var ErrCheckpointNotFound = errors.New("checkpoint not found")

func lastEventCursor(raw json.RawMessage) string {
	var state struct {
		LastEventCursor string `json:"lastEventCursor"`
	}
	if len(raw) == 0 {
		return ""
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return ""
	}
	return state.LastEventCursor
}
