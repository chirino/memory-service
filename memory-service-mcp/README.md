# Memory Service MCP Server

An [MCP](https://modelcontextprotocol.io/) (Model Context Protocol) server that gives AI coding assistants (Claude Code, Cursor, etc.) access to the Memory Service API for persisting and retrieving session notes across conversations.

## Installation

```bash
go install github.com/chirino/memory-service/memory-service-mcp@latest
```

Alternatively, the main `memory-service` binary includes an `mcp` subcommand:

```bash
go install github.com/chirino/memory-service@latest
memory-service mcp remote
```

For a single-process local setup, use:

```bash
memory-service mcp embedded --db-url ./memory.db
```

## Configuration

The MCP server requires two environment variables:

| Variable | Description |
|---|---|
| `MEMORY_SERVICE_URL` | Base URL of the Memory Service (e.g., `http://localhost:8082`) |
| `MEMORY_SERVICE_API_KEY` | API key for authentication |
| `MEMORY_SERVICE_BEARER_TOKEN` | (Optional) Bearer token for HTTP request authentication |

## Usage with Claude Code

Add the following to your project's `.mcp.json`:

```json
{
  "mcpServers": {
    "memory-service": {
      "command": "memory-service-mcp",
      "env": {
        "MEMORY_SERVICE_URL": "${MEMORY_SERVICE_URL}",
        "MEMORY_SERVICE_API_KEY": "${MEMORY_SERVICE_API_KEY}"
      }
    }
  }
}
```

Make sure `$GOPATH/bin` (usually `~/go/bin`) is in your `PATH`, and the environment variables are set in your shell or `.env` file.

`memory-service-mcp` remains the single-command remote wrapper. The main binary uses explicit subcommands: `memory-service mcp remote` and `memory-service mcp embedded`.

## Available Tools

| Tool | Description |
|---|---|
| `save_session_notes` | Save notes from the current session to the memory service |
| `search_sessions` | Search past sessions by keyword or semantic similarity |
| `list_sessions` | List recent sessions |
| `get_session` | Retrieve a specific session by ID |
| `append_note` | Append a note to an existing session |
