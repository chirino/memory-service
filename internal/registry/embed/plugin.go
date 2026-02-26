package embed

import (
	"context"
	"fmt"
)

// Embedder produces vector embeddings from text.
type Embedder interface {
	// EmbedTexts returns a vector embedding for each input text, in the same order.
	EmbedTexts(ctx context.Context, texts []string) ([][]float32, error)
	// ModelName returns the model identifier used for embedding.
	ModelName() string
	// Dimension returns the dimensionality of the embeddings.
	Dimension() int
}

// Loader creates an Embedder from config.
type Loader func(ctx context.Context) (Embedder, error)

// Plugin represents an embedder plugin.
type Plugin struct {
	Name   string
	Loader Loader
}

var plugins []Plugin

// Register adds an embedder plugin.
func Register(p Plugin) {
	plugins = append(plugins, p)
}

// Names returns all registered embedder plugin names.
func Names() []string {
	names := make([]string, len(plugins))
	for i, p := range plugins {
		names[i] = p.Name
	}
	return names
}

// Select returns the loader for the named embedder plugin.
func Select(name string) (Loader, error) {
	for _, p := range plugins {
		if p.Name == name {
			return p.Loader, nil
		}
	}
	return nil, fmt.Errorf("unknown embedder %q; valid: %v", name, Names())
}
