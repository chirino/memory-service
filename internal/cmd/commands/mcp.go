//go:build !nomcp

package commands

import "github.com/chirino/memory-service/internal/cmd/mcp"

func init() {
	Register(mcp.Command())
}
