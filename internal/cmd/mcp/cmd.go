package mcp

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/chirino/memory-service/internal/cmd/serve"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/generated/apiclient"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/urfave/cli/v3"
)

const (
	embeddedClientID    = "embedded-mcp"
	embeddedAPIKey      = "embedded-mcp-api-key"
	embeddedBearerToken = "embedded-mcp-user"
)

type mcpServer struct {
	server *mcpserver.MCPServer
	client *apiclient.ClientWithResponses
}

// Command returns the CLI command for the MCP server.
func Command() *cli.Command {
	return &cli.Command{
		Name:  "mcp",
		Usage: "MCP server for memory-service",
		Commands: []*cli.Command{
			RemoteCommand(),
			EmbeddedCommand(),
		},
	}
}

// RemoteCommand returns the remote MCP bridge command.
func RemoteCommand() *cli.Command {
	return &cli.Command{
		Name:  "remote",
		Usage: "Run the MCP server against a remote memory-service instance",
		Flags: remoteFlags(),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			client, err := newRemoteClient(
				cmd.String("url"),
				cmd.String("api-key"),
				cmd.String("bearer-token"),
			)
			if err != nil {
				return err
			}
			return serveStdio(client)
		},
	}
}

// EmbeddedCommand returns the embedded MCP bridge command.
func EmbeddedCommand() *cli.Command {
	cfg := defaultEmbeddedConfig()
	flagState := serve.NewFlagState(&cfg)
	return &cli.Command{
		Name:  "embedded",
		Usage: "Run the MCP server with an embedded memory-service instance",
		Flags: serve.EmbeddedFlags(&cfg, flagState),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if err := serve.ApplyParsedFlags(&cfg, cmd, flagState, false); err != nil {
				return err
			}
			if cfg.DBURL == "" {
				return fmt.Errorf("required flag \"db-url\" not set")
			}

			runCtx, cancel := context.WithCancel(config.WithContext(ctx, &cfg))
			defer cancel()

			ensureEmbeddedAuth(&cfg)

			srv, err := serve.BuildServer(runCtx, &cfg)
			if err != nil {
				return err
			}

			client, err := newInProcessClient(srv.Router)
			if err != nil {
				cancel()
				return err
			}

			err = serveStdio(client)
			cancel()

			drainCtx, drainCancel := context.WithTimeout(context.Background(), time.Duration(cfg.DrainTimeout)*time.Second)
			defer drainCancel()
			if shutdownErr := srv.Shutdown(drainCtx); shutdownErr != nil && err == nil {
				err = shutdownErr
			}
			return err
		},
	}
}

func remoteFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:     "url",
			Sources:  cli.EnvVars("MEMORY_SERVICE_URL"),
			Usage:    "Memory service base URL",
			Required: true,
		},
		&cli.StringFlag{
			Name:     "api-key",
			Sources:  cli.EnvVars("MEMORY_SERVICE_API_KEY"),
			Usage:    "Memory service API key",
			Required: true,
		},
		&cli.StringFlag{
			Name:    "bearer-token",
			Sources: cli.EnvVars("MEMORY_SERVICE_BEARER_TOKEN"),
			Usage:   "Bearer token for HTTP request authentication",
		},
	}
}

func serveStdio(client *apiclient.ClientWithResponses) error {
	s := &mcpServer{
		server: mcpserver.NewMCPServer(
			"memory-service-mcp",
			"0.1.0",
			mcpserver.WithToolCapabilities(false),
		),
		client: client,
	}

	registerTools(s)
	return mcpserver.ServeStdio(s.server)
}

func newRemoteClient(url, apiKey, bearerToken string) (*apiclient.ClientWithResponses, error) {
	return apiclient.NewClientWithResponses(
		url,
		apiclient.WithHTTPClient(&http.Client{Timeout: 30 * time.Second}),
		apiclient.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
			req.Header.Set("X-API-Key", apiKey)
			if bearerToken != "" {
				req.Header.Set("Authorization", "Bearer "+bearerToken)
			}
			return nil
		}),
	)
}

func ensureEmbeddedAuth(cfg *config.Config) {
	if cfg.APIKeys == nil {
		cfg.APIKeys = map[string]string{}
	}
	cfg.APIKeys[embeddedAPIKey] = embeddedClientID
}

func defaultEmbeddedConfig() config.Config {
	cfg := config.DefaultConfig()
	cfg.DatastoreType = "sqlite"
	cfg.CacheType = "local"
	cfg.AttachType = "fs"
	cfg.VectorType = ""
	cfg.EmbedType = "none"
	cfg.SearchSemanticEnabled = false
	return cfg
}
