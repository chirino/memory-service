package migrate

import (
	"context"
	"fmt"
	"sort"
)

// Migrator runs schema migrations for a single plugin.
type Migrator interface {
	Name() string
	Migrate(ctx context.Context) error
}

// Plugin represents a migrator with an order for deterministic execution sequence.
type Plugin struct {
	Order    int
	Migrator Migrator
}

var plugins []Plugin

// Register adds a migration plugin. Called from init() in plugin packages.
func Register(p Plugin) {
	plugins = append(plugins, p)
}

// RunAll executes all registered migrators sorted by Order.
func RunAll(ctx context.Context) error {
	sorted := make([]Plugin, len(plugins))
	copy(sorted, plugins)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Order < sorted[j].Order })

	for _, p := range sorted {
		if err := p.Migrator.Migrate(ctx); err != nil {
			return fmt.Errorf("migration %s failed: %w", p.Migrator.Name(), err)
		}
	}
	return nil
}
