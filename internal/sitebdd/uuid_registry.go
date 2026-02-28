//go:build site_tests

package sitebdd

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

var uuidPattern = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)

type scenarioUUIDRegistry struct {
	mu     sync.Mutex
	owners map[string]string // uuid(lowercase) -> scenario key
}

var globalScenarioUUIDRegistry = newScenarioUUIDRegistry()

func newScenarioUUIDRegistry() *scenarioUUIDRegistry {
	return &scenarioUUIDRegistry{
		owners: make(map[string]string),
	}
}

func (r *scenarioUUIDRegistry) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.owners = make(map[string]string)
}

func (r *scenarioUUIDRegistry) ClaimScenarioUUIDs(scenarioKey string, values []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range values {
		uuid := strings.ToLower(v)
		if owner, exists := r.owners[uuid]; exists {
			if owner != scenarioKey {
				return fmt.Errorf("uuid %q already claimed by scenario %q", uuid, owner)
			}
			continue
		}
		r.owners[uuid] = scenarioKey
	}
	return nil
}

func extractUUIDs(values ...string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, value := range values {
		matches := uuidPattern.FindAllString(value, -1)
		for _, m := range matches {
			key := strings.ToLower(m)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, m)
		}
	}
	return out
}
