package commands

import "github.com/chirino/memory-service/internal/cmd/migrate"

func init() {
	Register(migrate.Command())
}
