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
wt exec # starts an interactive shell
wt exec -- npm run dev
wt exec -- go test ./...
```

## When NOT to use `wt exec`

Use regular commands (without `wt exec`) for operations that are purely local
to the worktree and cannot conflict:

- Reading files, searching code (`grep`, `find`, `cat`)
- Git operations (`git status`, `git diff`, `git log`)
- Editing files
- Running linters or formatters that don't start servers
- `wt` subcommands themselves (`wt ls`, `wt name`, `wt dir`, `wt skill`)

## Accessing HTTP services inside the devcontainer

Each worktree's devcontainer has a dedicated SOCKS5 proxy for accessing
services running inside the container from the host.

The recommended way to browse HTTP endpoints is with `wt chrome`, which
launches a Chrome instance with a per-worktree profile and the proxy
pre-configured:

```sh
# Open Chrome pointed at the current worktree's devcontainer
wt chrome

# Open Chrome for a named worktree
wt chrome <name>

# Open a specific URL
wt chrome -- http://127.0.0.1:8080
```

**Important:** Always use `127.0.0.1` instead of `localhost` in URLs.
The SOCKS5 proxy cannot resolve `localhost` reliably.

For CLI access, use the proxy port directly:

```sh
# curl through the proxy
curl --proxy socks5h://127.0.0.1:$(wt proxy-port) http://127.0.0.1:8080

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
| `wt chrome` | Open Chrome with proxy to the current worktree's devcontainer |
| `wt chrome <name>` | Open Chrome with proxy to a named worktree's devcontainer |
| `wt curl -- <curl-args>` | Run curl with proxy to the current worktree's devcontainer |
| `wt curl <name> -- <curl-args>` | Run curl with proxy to a named worktree's devcontainer |
| `wt playwright` | Open a Playwright browser with proxy to the current worktree's devcontainer |
| `wt playwright <name>` | Open a Playwright browser with proxy to a named worktree's devcontainer |
| `wt down` | Stop and remove the current worktree's devcontainer |
| `wt down <name>` | Stop and remove a named worktree's devcontainer |
| `wt init` | Create a minimal `.devcontainer/` with SOCKS5 proxy support |
| `wt proxy-port` | Print the SOCKS proxy port for the current worktree |
| `wt proxy-port <name>` | Print the SOCKS proxy port for a named worktree |
| `wt name` | Print the current worktree name |
| `wt dir` | Print the current worktree root directory |
