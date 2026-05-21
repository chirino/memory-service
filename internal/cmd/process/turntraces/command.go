package turntraces

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/urfave/cli/v3"
)

// Command returns the turn-trace processor command.
func Command() *cli.Command {
	return &cli.Command{
		Name:  "turn-traces",
		Usage: "Derive OpenTelemetry turn traces from Memory Service events",
		Flags: flags(),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			scope := strings.TrimSpace(cmd.String("scope"))
			if scope == "" {
				scope = "admin"
			}
			if scope != "admin" && scope != "user" {
				return fmt.Errorf("scope must be one of: admin, user")
			}
			sessionMode := strings.TrimSpace(cmd.String("langfuse-session-id"))
			if sessionMode == "" {
				sessionMode = "conversation"
			}
			if sessionMode != "conversation" && sessionMode != "conversation-group" {
				return fmt.Errorf("langfuse-session-id must be one of: conversation, conversation-group")
			}
			if cmd.String("overlap-policy") != "" && cmd.String("overlap-policy") != "cut-short" {
				return fmt.Errorf("overlap-policy currently supports only cut-short")
			}

			cfg := Config{
				IdleTimeout:         cmd.Duration("idle-timeout"),
				MaxTurnAge:          cmd.Duration("max-turn-age"),
				MaxOpenTurns:        int(cmd.Int("max-open-turns")),
				LangfuseName:        cmd.String("langfuse-name"),
				SessionIDMode:       sessionMode,
				ServiceName:         cmd.String("otel-service-name"),
				RuntimeVersion:      runtimeVersion(),
				Environment:         cmd.String("environment"),
				DryRun:              cmd.Bool("dry-run"),
				DropOnExportFailure: cmd.Bool("drop-on-export-failure"),
			}

			log.Info("starting turn-traces processor",
				"endpoint", cmd.String("endpoint"),
				"clientID", cmd.String("client-id"),
				"scope", scope,
				"sessionIDMode", sessionMode,
				"langfuseName", cfg.LangfuseName,
				"checkpointInterval", cmd.Duration("checkpoint-interval"),
				"serviceName", cfg.ServiceName,
				"environment", cfg.Environment,
				"dryRun", cfg.DryRun,
			)
			running, err := StartProcessor(ctx, StartOptions{
				Endpoint:           cmd.String("endpoint"),
				ClientID:           cmd.String("client-id"),
				APIKey:             cmd.String("api-key"),
				BearerToken:        cmd.String("bearer-token"),
				Scope:              scope,
				AfterCursor:        cmd.String("after-cursor"),
				CheckpointInterval: cmd.Duration("checkpoint-interval"),
				TurnTraces:         cfg,
			})
			if err != nil {
				return err
			}
			defer func() {
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_ = running.Shutdown(shutdownCtx)
			}()
			return running.Wait()
		},
	}
}

func flags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:     "endpoint",
			Sources:  cli.EnvVars("MEMORY_SERVICE_GRPC_ENDPOINT"),
			Usage:    "gRPC Memory Service endpoint",
			Required: true,
		},
		&cli.StringFlag{
			Name:     "client-id",
			Sources:  cli.EnvVars("MEMORY_SERVICE_PROCESS_CLIENT_ID"),
			Usage:    "Stable checkpoint client ID and processor identity",
			Required: true,
		},
		&cli.StringFlag{
			Name:    "api-key",
			Sources: cli.EnvVars("MEMORY_SERVICE_API_KEY"),
			Usage:   "Memory Service API key",
		},
		&cli.StringFlag{
			Name:    "bearer-token",
			Sources: cli.EnvVars("MEMORY_SERVICE_BEARER_TOKEN"),
			Usage:   "Bearer token for gRPC authorization",
		},
		&cli.StringFlag{
			Name:    "after-cursor",
			Sources: cli.EnvVars("MEMORY_SERVICE_PROCESS_AFTER_CURSOR"),
			Usage:   "Bootstrap cursor when no checkpoint exists",
		},
		&cli.DurationFlag{
			Name:    "checkpoint-interval",
			Sources: cli.EnvVars("MEMORY_SERVICE_PROCESS_CHECKPOINT_INTERVAL"),
			Value:   5 * time.Second,
			Usage:   "Maximum time between checkpoint flushes",
		},
		&cli.StringFlag{
			Name:    "scope",
			Sources: cli.EnvVars("MEMORY_SERVICE_PROCESS_SCOPE"),
			Value:   "admin",
			Usage:   "Event stream scope (admin|user)",
		},
		&cli.DurationFlag{
			Name:    "idle-timeout",
			Sources: cli.EnvVars("MEMORY_SERVICE_TURN_TRACES_IDLE_TIMEOUT"),
			Value:   5 * time.Minute,
			Usage:   "Close an open turn after this idle duration",
		},
		&cli.DurationFlag{
			Name:    "max-turn-age",
			Sources: cli.EnvVars("MEMORY_SERVICE_TURN_TRACES_MAX_TURN_AGE"),
			Value:   30 * time.Minute,
			Usage:   "Force close long-running turns after this age",
		},
		&cli.IntFlag{
			Name:    "max-open-turns",
			Sources: cli.EnvVars("MEMORY_SERVICE_TURN_TRACES_MAX_OPEN_TURNS"),
			Value:   1000,
			Usage:   "Maximum open turns stored in the checkpoint",
		},
		&cli.StringFlag{
			Name:    "overlap-policy",
			Sources: cli.EnvVars("MEMORY_SERVICE_TURN_TRACES_OVERLAP_POLICY"),
			Value:   "cut-short",
			Usage:   "Policy for overlapping user turns",
		},
		&cli.StringFlag{
			Name:    "langfuse-name",
			Sources: cli.EnvVars("MEMORY_SERVICE_TURN_TRACES_LANGFUSE_NAME"),
			Value:   defaultSpanName,
			Usage:   "Langfuse trace name and root span name",
		},
		&cli.StringFlag{
			Name:    "langfuse-session-id",
			Sources: cli.EnvVars("MEMORY_SERVICE_TURN_TRACES_LANGFUSE_SESSION_ID"),
			Value:   "conversation",
			Usage:   "Langfuse session ID mapping (conversation|conversation-group)",
		},
		&cli.StringFlag{
			Name:    "otel-service-name",
			Sources: cli.EnvVars("OTEL_SERVICE_NAME"),
			Value:   "memory-service-turn-traces",
			Usage:   "OpenTelemetry service name",
		},
		&cli.StringFlag{
			Name:    "environment",
			Sources: cli.EnvVars("LANGFUSE_TRACING_ENVIRONMENT"),
			Usage:   "Langfuse/OpenTelemetry environment label",
		},
		&cli.BoolFlag{
			Name:    "dry-run",
			Sources: cli.EnvVars("MEMORY_SERVICE_TURN_TRACES_DRY_RUN"),
			Usage:   "Log derived turn boundaries instead of exporting spans",
		},
		&cli.BoolFlag{
			Name:    "drop-on-export-failure",
			Sources: cli.EnvVars("MEMORY_SERVICE_TURN_TRACES_DROP_ON_EXPORT_FAILURE"),
			Usage:   "Advance checkpoints even if span export fails",
		},
	}
}

func runtimeVersion() string {
	if version := strings.TrimSpace(os.Getenv("MEMORY_SERVICE_VERSION")); version != "" {
		return version
	}
	return "dev"
}
