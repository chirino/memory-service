package postgres

import _ "embed"

//go:embed db/schema.sql
var schemaSQL string

// ForceImport is a no-op variable that can be referenced to ensure this package's init() runs.
var ForceImport = 0
