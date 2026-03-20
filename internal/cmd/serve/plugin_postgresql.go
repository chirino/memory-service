//go:build !nopostgresql

package serve

import (
	_ "github.com/chirino/memory-service/internal/plugin/attach/pgstore"
	_ "github.com/chirino/memory-service/internal/plugin/eventbus/postgres"
	_ "github.com/chirino/memory-service/internal/plugin/store/postgres"
	_ "github.com/chirino/memory-service/internal/plugin/vector/pgvector"
)
