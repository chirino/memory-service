//go:build nopostgresql && nosqlite && nomongo

package store

// This file is only compiled when every store backend is excluded.
// The undefined reference below produces a build error on purpose.
var _ = at_least_one_store_backend_must_be_enabled
