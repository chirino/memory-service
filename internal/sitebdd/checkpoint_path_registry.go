//go:build site_tests

package sitebdd

import (
	"fmt"
	"sync"
)

type checkpointPathRegistry struct {
	mu     sync.Mutex
	owners map[string]string // checkpointPath -> scenario key
}

func newCheckpointPathRegistry() *checkpointPathRegistry {
	return &checkpointPathRegistry{
		owners: make(map[string]string),
	}
}

func (r *checkpointPathRegistry) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.owners = make(map[string]string)
}

func (r *checkpointPathRegistry) Claim(path, scenarioKey string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if owner, exists := r.owners[path]; exists && owner != scenarioKey {
		return fmt.Errorf("checkpoint path %q already claimed by scenario %q", path, owner)
	}
	r.owners[path] = scenarioKey
	return nil
}

func (r *checkpointPathRegistry) Release(path, scenarioKey string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if owner, exists := r.owners[path]; exists && owner == scenarioKey {
		delete(r.owners, path)
	}
}

var globalCheckpointPathRegistry = newCheckpointPathRegistry()
