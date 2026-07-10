package store

import "fmt"

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
