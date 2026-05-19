package commands

import "github.com/chirino/memory-service/internal/cmd/process"

func init() {
	Register(process.Command())
}
