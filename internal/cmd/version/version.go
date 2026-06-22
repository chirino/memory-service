package version

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/chirino/memory-service/internal/runtimeversion"
	"github.com/urfave/cli/v3"
)

// Command returns the version command.
func Command() *cli.Command {
	return &cli.Command{
		Name:  "version",
		Usage: "Print the Memory Service version",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			_ = ctx
			fmt.Fprintln(output(cmd), runtimeversion.Current())
			return nil
		},
	}
}

func output(cmd *cli.Command) io.Writer {
	if cmd != nil {
		if root := cmd.Root(); root != nil && root.Writer != nil {
			return root.Writer
		}
		if cmd.Writer != nil {
			return cmd.Writer
		}
	}
	return os.Stdout
}
