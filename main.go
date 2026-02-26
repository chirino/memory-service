//go:generate go run ./internal/cmd/generate

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/cmd/migrate"
	"github.com/chirino/memory-service/internal/cmd/serve"
	"github.com/urfave/cli/v3"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app := &cli.Command{
		Name:  "memory-service",
		Usage: "Memory service for AI agents",
		Commands: []*cli.Command{
			serve.Command(),
			migrate.Command(),
		},
	}
	if err := app.Run(ctx, os.Args); err != nil {
		log.Fatal(err)
	}
}
