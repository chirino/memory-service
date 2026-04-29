package main

import (
	"context"
	"fmt"
	"os"

	"github.com/chirino/memory-service/internal/cmd/mcp"
)

func main() {
	cmd := mcp.RemoteCommand()
	cmd.Name = "memory-service-mcp"
	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
