//go:build !nomongo

package serve

import (
	_ "github.com/chirino/memory-service/internal/plugin/store/mongo"
)
