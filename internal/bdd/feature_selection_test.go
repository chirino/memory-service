package bdd

import (
	"path/filepath"
	"strings"
)

var serialFeatureFiles = map[string]bool{
	"admin-rest.feature":             true,
	"admin-attachments-rest.feature": true,
	"admin-stats-rest.feature":       true,
	"eviction-rest.feature":          true,
	"task-queue.feature":             true,
}

func isSerialFeature(path string) bool {
	base := filepath.Base(path)
	if idx := strings.Index(base, ":"); idx >= 0 {
		base = base[:idx]
	}
	return serialFeatureFiles[base]
}

func filterSerialFeatures(paths []string, includeSerial bool) []string {
	filtered := make([]string, 0, len(paths))
	for _, path := range paths {
		if isSerialFeature(path) == includeSerial {
			filtered = append(filtered, path)
		}
	}
	return filtered
}
