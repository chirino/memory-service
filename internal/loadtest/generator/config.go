package main

import (
	"flag"
	"fmt"
	"os"
)

// GeneratorConfig holds all CLI-configurable parameters for the seed generator.
type GeneratorConfig struct {
	BaseURL            string
	APIKey             string
	TotalConversations int
	ForkChains         int
	WorkerCount        int
	IndexBatchSize     int
	SeedManifestPath   string
}

// parseConfig reads CLI flags and returns a GeneratorConfig.
func parseConfig() GeneratorConfig {
	cfg := GeneratorConfig{}

	flag.StringVar(&cfg.BaseURL, "base-url", "http://localhost:8082", "Base URL of the memory service")
	flag.StringVar(&cfg.APIKey, "api-key", "agent-api-key-1", "API key for authentication")
	flag.IntVar(&cfg.TotalConversations, "total-conversations", 2000, "Number of conversations to seed")
	flag.IntVar(&cfg.ForkChains, "fork-chains", 10, "Number of fork chains to seed (each = 1 root + 1 fork conversation)")
	flag.IntVar(&cfg.WorkerCount, "worker-count", 5, "Number of concurrent seeding workers")
	flag.IntVar(&cfg.IndexBatchSize, "index-batch-size", 50, "Number of entries per POST /v1/conversations/index batch (reduce if server returns 500)")
	flag.StringVar(&cfg.SeedManifestPath, "seed-manifest-path", "loadtest/results/seed-manifest.json", "Path to write the seed manifest JSON")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: generator [flags]\n\nFlags:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExample:\n  go run ./internal/loadtest/generator/ --total-conversations=2000 --fork-chains=40\n")
	}

	flag.Parse()
	return cfg
}
