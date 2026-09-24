package store

import (
	"errors"
	"fmt"
)

const DuplicateSequenceConflictCode = "duplicate_sequence"
const ConversationAlreadyExistsCode = "conversation_already_exists"

// NotFoundError indicates the resource was not found (or user lacks access).
type NotFoundError struct {
	Resource string
	ID       string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%s not found: %s", e.Resource, e.ID)
}

// ValidationError indicates a client-side validation failure.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error on %s: %s", e.Field, e.Message)
}

// ConflictError indicates a uniqueness/conflict violation.
type ConflictError struct {
	Message string
	Code    string
	Details map[string]interface{}
}

func (e *ConflictError) Error() string {
	return e.Message
}

func NewDuplicateSequenceConflict() error {
	return &ConflictError{
		Message: "duplicate seq value in this conversation",
		Code:    DuplicateSequenceConflictCode,
	}
}

func IsDuplicateSequenceConflict(err error) bool {
	var conflict *ConflictError
	return errors.As(err, &conflict) && conflict.Code == DuplicateSequenceConflictCode
}

// ConversationIDConflictError indicates a conversation with the provided ID exists
// but the request differs from the stored conversation.
// It embeds ConflictError so errors.As() can detect it for gRPC error mapping.
type ConversationIDConflictError struct {
	*ConflictError
	ConversationID string
}

func (e *ConversationIDConflictError) Unwrap() error {
	return e.ConflictError
}

func NewConversationIDConflictError(conversationID string) error {
	return &ConversationIDConflictError{
		ConflictError: &ConflictError{
			Message: fmt.Sprintf("conversation with id %s exists but the request differs", conversationID),
			Code:    ConversationAlreadyExistsCode,
			Details: map[string]interface{}{
				"conversationId": conversationID,
			},
		},
		ConversationID: conversationID,
	}
}

// ForbiddenError indicates insufficient access.
type ForbiddenError struct{}

func (e *ForbiddenError) Error() string {
	return "forbidden"
}

// BadRequestError indicates a client-side request error (e.g., invalid pagination parameters).
type BadRequestError struct {
	Message string
}

func (e *BadRequestError) Error() string {
	return e.Message
}
