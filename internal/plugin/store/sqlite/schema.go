//go:build !nosqlite

package sqlite

import _ "embed"

//go:embed db/schema.sql
var schemaSQL string

//go:embed db/schema_fts.sql
var ftsSchemaSQL string

// ForceImport is a no-op variable that can be referenced to ensure this package's init() runs.
var ForceImport = 0
