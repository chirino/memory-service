//go:generate go run ./internal/cmd/generate

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/cmd/commands"
	"github.com/chirino/memory-service/internal/runtimeversion"
	"github.com/urfave/cli/v3"
)

// Version is set by release builds with -ldflags "-X main.Version=<version>".
var Version string

func main() {
	runtimeversion.Set(Version)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app := &cli.Command{
		Name:     "memory-service",
		Usage:    "Memory service for AI agents",
		Commands: commands.All(),
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "log-format",
				Usage:   "Log output format (text or json)",
				Value:   "text",
				Sources: cli.EnvVars("MEMORY_SERVICE_LOG_FORMAT"),
			},
		},
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			switch format := cmd.String("log-format"); format {
			case "text":
				log.SetFormatter(log.TextFormatter)
			case "json":
				log.SetFormatter(log.JSONFormatter)
			default:
				return ctx, fmt.Errorf("invalid log format %q: expected text or json", format)
			}

			if lvl := os.Getenv("MEMORY_SERVICE_LOG_LEVEL"); lvl != "" {
				level, err := log.ParseLevel(lvl)
				if err != nil {
					log.Warn("invalid MEMORY_SERVICE_LOG_LEVEL, using default", "value", lvl, "error", err)
				} else {
					log.SetLevel(level)
				}
			}
			return ctx, nil
		},
	}
	if err := app.Run(ctx, os.Args); err != nil {
		log.Fatal(err)
	}
}
