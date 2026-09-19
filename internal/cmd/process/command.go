package process

import (
	clickhouseprocessor "github.com/chirino/memory-service/internal/cmd/process/clickhouse"
	"github.com/chirino/memory-service/internal/cmd/process/turntraces"
	"github.com/urfave/cli/v3"
)

// Command returns the checkpointed event processor command family.
func Command() *cli.Command {
	return &cli.Command{
		Name:  "process",
		Usage: "Run checkpointed Memory Service event processors",
		Commands: []*cli.Command{
			clickhouseprocessor.Command(),
			turntraces.Command(),
		},
	}
}
