//go:build !nosqlite

package serve

import (
	_ "github.com/chirino/memory-service/internal/plugin/vector/sqlitevec"
)
