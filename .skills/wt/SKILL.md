---
name: worktree
description: Worktree-isolated command execution with `wt` and devcontainers. Use when running builds, tests, dev servers, or any command that opens ports or uses shared resources.
---

# Worktree-Isolated Command Execution

This project uses `wt` (git worktree manager) with devcontainer support.
When working inside a worktree that has a `.devcontainer/devcontainer.json`,
commands that could conflict across worktrees MUST be run inside the
devcontainer using `wt exec`.

## When to use `wt exec`

Use `wt exec -- <command> [args...]` for any command that:

- Opens network ports (test servers, database connections, dev servers)
- Starts background services or daemons
- Uses shared resources like lock files or temp directories
- Runs builds that produce artifacts at fixed paths outside the worktree
- Runs integration or end-to-end tests

Examples:
```sh
wt exec -- make test
wt exec -- npm run dev
wt exec -- go test ./...
wt exec -- cargo test
```

## When NOT to use `wt exec`

Use regular commands (without `wt exec`) for operations that are purely local
to the worktree and cannot conflict:

- Reading files, searching code (`grep`, `find`, `cat`)
- Git operations (`git status`, `git diff`, `git log`)
- Editing files
- Running linters or formatters that don't start servers
- `wt` subcommands themselves (`wt ls`, `wt name`, `wt dir`)

## Accessing services inside the devcontainer

Each worktree's devcontainer has a dedicated SOCKS5 proxy for accessing
services running inside the container from the host:

```sh
# Get the proxy port for the current worktree
wt proxy-port

# Get the proxy port for a named worktree
wt proxy-port <name>
```

Then use the proxy to reach container services:

```sh
# curl through the proxy
curl --proxy socks5h://127.0.0.1:$(wt proxy-port) http://localhost:8080

# Or set the environment variable for tools that support it
export ALL_PROXY=socks5h://127.0.0.1:$(wt proxy-port)
```

## Ensuring the devcontainer is running

Before running `wt exec`, make sure the devcontainer is up:

```sh
wt up
```

## Quick reference

All commands default to the current worktree when no name is given.

| Command | Purpose |
|---|---|
| `wt exec -- <cmd>` | Run a command in the current worktree's devcontainer |
| `wt exec <name> -- <cmd>` | Run a command in a named worktree's devcontainer |
| `wt up` | Start the current worktree's devcontainer |
| `wt up <name>` | Start a named worktree's devcontainer |
| `wt build` | Build the current worktree's devcontainer |
| `wt build <name>` | Build a named worktree's devcontainer |
| `wt proxy-port` | Print the SOCKS proxy port for the current worktree |
| `wt proxy-port <name>` | Print the SOCKS proxy port for a named worktree |
| `wt name` | Print the current worktree name |
| `wt dir` | Print the current worktree root directory |
