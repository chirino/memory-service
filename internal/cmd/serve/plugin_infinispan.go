//go:build !noinfinispan

package serve

import (
	_ "github.com/chirino/memory-service/internal/plugin/cache/infinispan"
	_ "github.com/chirino/memory-service/internal/plugin/vector/infinispan"
)
