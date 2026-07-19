package operationevent

import (
	"fmt"

	"github.com/charmbracelet/log"
)

// LogRecoveredPanic emits a stack-bearing diagnostic point log correlated
// with an operation. Stacks and panic values intentionally remain outside the
// canonical event snapshot.
func LogRecoveredPanic(event *Event, operation string, recovered any, stack []byte) {
	var snapshot Snapshot
	if event != nil {
		snapshot = event.Snapshot()
		if operation == "" {
			operation = event.Message()
		}
	}
	args := make([]any, 0, 38)
	add := func(name string, value any, present bool) {
		if present {
			args = append(args, name, value)
		}
	}
	operation = sanitize(operation, maxMessageLength)
	add("operation", operation, operation != "")
	add("requestID", snapshot.RequestID, snapshot.RequestID != "")
	add("userID", snapshot.UserID, snapshot.UserID != "")
	add("clientID", snapshot.ClientID, snapshot.ClientID != "")
	add("agentID", snapshot.AgentID, snapshot.AgentID != "")
	add("conversationID", snapshot.ConversationID, snapshot.ConversationID != "")
	add("entryID", snapshot.EntryID, snapshot.EntryID != "")
	add("attachmentID", snapshot.AttachmentID, snapshot.AttachmentID != "")
	add("memoryID", snapshot.MemoryID, snapshot.MemoryID != "")
	add("taskID", snapshot.TaskID, snapshot.TaskID != "")
	add("connectionID", snapshot.ConnectionID, snapshot.ConnectionID != "")
	add("cursor", snapshot.Cursor, snapshot.Cursor != "")
	add("providerName", snapshot.ProviderName, snapshot.ProviderName != "")
	add("providerStatusCode", snapshot.ProviderStatusCode, snapshot.ProviderStatusCode != 0)
	add("providerErrorCode", snapshot.ProviderErrorCode, snapshot.ProviderErrorCode != "")
	add("providerTransactionID", snapshot.ProviderTransactionID, snapshot.ProviderTransactionID != "")
	add("retryAttempt", snapshot.RetryAttempt, snapshot.RetryAttempt != 0)
	panicType := sanitize(fmt.Sprintf("%T", recovered), maxFieldLength)
	add("panicType", panicType, panicType != "")
	args = append(args, "stack", string(stack))
	log.Error("operation panic", args...)
}
