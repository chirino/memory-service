//go:generate go run ./internal/cmd/generate

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/cmd/commands"
	"github.com/urfave/cli/v3"
)

func main() {
	if lvl := os.Getenv("MEMORY_SERVICE_LOG_LEVEL"); lvl != "" {
		level, err := log.ParseLevel(lvl)
		if err != nil {
			log.Warn("invalid MEMORY_SERVICE_LOG_LEVEL, using default", "value", lvl, "error", err)
		} else {
			log.SetLevel(level)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app := &cli.Command{
		Name:     "memory-service",
		Usage:    "Memory service for AI agents",
		Commands: commands.All(),
	}
	if err := app.Run(ctx, os.Args); err != nil {
		log.Fatal(err)
	}
}
