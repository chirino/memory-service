package commands

import "github.com/chirino/memory-service/internal/cmd/version"

func init() {
	Register(version.Command())
}
