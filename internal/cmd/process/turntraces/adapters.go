package turntraces

import "encoding/json"

// EntryContentAdapter is the extension point for future content-type-specific
// observation enrichment. Adapters must fail closed: unknown or malformed input
// should return no enrichment rather than guessed telemetry.
type EntryContentAdapter interface {
	ContentTypes() []string
	ExtractObservationAttributes(entry EntryEnvelope) (ObservationEnrichment, error)
}

// EntryEnvelope is a stable adapter-facing entry payload.
type EntryEnvelope struct {
	ID             string
	ConversationID string
	Channel        string
	ContentType    string
	Content        []json.RawMessage
	AgentID        string
	ClientID       string
}

// ObservationEnrichment contains optional metadata for future child observations.
type ObservationEnrichment struct {
	Attributes map[string]string
}
