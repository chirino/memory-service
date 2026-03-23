package mcp

import (
	"context"
	"net/http"
	"time"

	"github.com/chirino/memory-service/internal/generated/apiclient"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/urfave/cli/v3"
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
		Flags: []cli.Flag{
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
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			apiKey := cmd.String("api-key")

			client, err := apiclient.NewClientWithResponses(
				cmd.String("url"),
				apiclient.WithHTTPClient(&http.Client{Timeout: 30 * time.Second}),
				apiclient.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
					req.Header.Set("Authorization", "Bearer "+apiKey)
					return nil
				}),
			)
			if err != nil {
				return err
			}

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
		},
	}
}
