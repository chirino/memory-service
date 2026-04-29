package bdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var serialFeatureFiles = map[string]bool{
	"admin-rest.feature":             true,
	"admin-checkpoints-rest.feature": true,
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

func filterFeatureBase(paths []string, skip map[string]bool) []string {
	if len(skip) == 0 {
		return paths
	}
	filtered := make([]string, 0, len(paths))
	for _, path := range paths {
		if skip[filepath.Base(path)] {
			continue
		}
		filtered = append(filtered, path)
	}
	return filtered
}

func collectRESTFeatureFiles(t *testing.T, resourcesDir string, skip map[string]bool) []string {
	t.Helper()

	featuresDir := filepath.Join(resourcesDir, "features")
	if _, err := os.Stat(featuresDir); os.IsNotExist(err) {
		t.Skipf("Feature files directory not found: %s", featuresDir)
	}

	all, err := filepath.Glob(filepath.Join(featuresDir, "*.feature"))
	if err != nil {
		t.Fatalf("glob REST feature files: %v", err)
	}

	var filtered []string
	for _, featurePath := range all {
		if isSerialFeature(featurePath) {
			continue
		}
		base := filepath.Base(featurePath)
		if strings.Contains(base, "-grpc") {
			continue
		}
		if skip != nil && skip[base] {
			continue
		}
		filtered = append(filtered, featurePath)
	}
	if len(filtered) == 0 {
		t.Fatalf("No REST feature files found in %s", featuresDir)
	}
	return filtered
}
