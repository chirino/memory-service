package config

import (
	"net/url"
	"strings"
)

// MongoDatabaseName returns the Mongo database name encoded in DBURL, falling
// back to the historical default when the URI omits one.
func MongoDatabaseName(dbURL string) string {
	parsed, err := url.Parse(dbURL)
	if err != nil {
		return "memory_service"
	}
	name := strings.Trim(strings.TrimSpace(parsed.Path), "/")
	if name == "" {
		return "memory_service"
	}
	if idx := strings.IndexByte(name, '/'); idx >= 0 {
		name = name[:idx]
	}
	return name
}
