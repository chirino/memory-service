//go:build !noredis

package serve

import _ "github.com/chirino/memory-service/internal/plugin/cache/redis"
