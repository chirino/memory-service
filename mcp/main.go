package main

import (
	"fmt"
	"os"

	"github.com/mark3labs/mcp-go/server"
)

type mcpServer struct {
	server *server.MCPServer
	client *Client
}

func main() {
	url := os.Getenv("MEMORY_SERVICE_CLIENT_URL")
	if url == "" {
		fmt.Fprintln(os.Stderr, "MEMORY_SERVICE_CLIENT_URL is required")
		os.Exit(1)
	}

	apiKey := os.Getenv("MEMORY_SERVICE_CLIENT_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "MEMORY_SERVICE_CLIENT_API_KEY is required")
		os.Exit(1)
	}

	s := &mcpServer{
		server: server.NewMCPServer(
			"memory-service-mcp",
			"0.1.0",
			server.WithToolCapabilities(false),
		),
		client: NewClient(url, apiKey),
	}

	registerTools(s)

	if err := server.ServeStdio(s.server); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}

