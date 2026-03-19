package commands

import "github.com/chirino/memory-service/internal/cmd/serve"

func init() {
	Register(serve.Command())
}
