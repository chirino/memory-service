package commands

import "github.com/urfave/cli/v3"

var commands []*cli.Command

// Register adds a subcommand to the registry.
func Register(cmd *cli.Command) {
	commands = append(commands, cmd)
}

// All returns all registered subcommands.
func All() []*cli.Command {
	return commands
}
