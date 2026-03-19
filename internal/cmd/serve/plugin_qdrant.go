//go:build !noqdrant

package serve

import (
	_ "github.com/chirino/memory-service/internal/plugin/store/episodicqdrant"
	_ "github.com/chirino/memory-service/internal/plugin/vector/qdrant"
)
