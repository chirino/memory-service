package postgres

import registrystore "github.com/chirino/memory-service/internal/registry/store"

// Re-export error types from registry/store for backward compatibility.
type NotFoundError = registrystore.NotFoundError
type ValidationError = registrystore.ValidationError
type ConflictError = registrystore.ConflictError
type ForbiddenError = registrystore.ForbiddenError
