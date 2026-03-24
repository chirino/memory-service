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
	server     *mcpserver.MCPServer
	client     *apiclient.ClientWithResponses
	baseURL    string
	httpClient *http.Client
	authEditor apiclient.RequestEditorFn
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
			&cli.StringFlag{
				Name:    "bearer-token",
				Sources: cli.EnvVars("MEMORY_SERVICE_BEARER_TOKEN"),
				Usage:   "Bearer token for HTTP request authentication",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			apiKey := cmd.String("api-key")
			bearerToken := cmd.String("bearer-token")
			baseURL := cmd.String("url")

			authEditor := apiclient.RequestEditorFn(func(_ context.Context, req *http.Request) error {
				req.Header.Set("X-API-Key", apiKey)
				if bearerToken != "" {
					req.Header.Set("Authorization", "Bearer "+bearerToken)
				}
				return nil
			})

			httpClient := &http.Client{Timeout: 30 * time.Second}

			client, err := apiclient.NewClientWithResponses(
				baseURL,
				apiclient.WithHTTPClient(httpClient),
				apiclient.WithRequestEditorFn(authEditor),
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
				client:     client,
				baseURL:    baseURL,
				httpClient: httpClient,
				authEditor: authEditor,
			}

			registerTools(s)

			return mcpserver.ServeStdio(s.server)
		},
	}
}
