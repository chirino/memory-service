# MCP Bridge: Claude Code <-> Memory Service

Connect Claude Code sessions to the deployed memory-service so developers get persistent, searchable, shared memory across sessions.

## Goal

When working on this project with Claude Code, session context (decisions, discoveries, solutions) persists in the memory-service on Fly.io. New sessions can query past work. Multiple team members share a single knowledge base.

## Architecture

```
Claude Code  --stdio-->  MCP Server (local process)  --HTTPS-->  Memory Service (Fly.io)
                              |                                        |
                         JSON-RPC / MCP protocol               REST API + Bearer auth
```

## MCP Tools to Expose

| Tool | Purpose | API Call |
|------|---------|----------|
| `save_session_notes` | Save a summary/notes from the current session | `POST /v1/conversations` + `POST /v1/conversations/{id}/entries` |
| `search_sessions` | Search past sessions by natural language query | `POST /v1/conversations/search` |
| `list_sessions` | List recent sessions | `GET /v1/conversations` |
| `get_session` | Retrieve entries from a specific past session | `GET /v1/conversations/{id}/entries` |
| `append_note` | Add a note to an existing session/conversation | `POST /v1/conversations/{id}/entries` |

## Implementation

### Language: Go

Keeps it consistent with the rest of the codebase. The MCP server is a small CLI binary that:
- Reads config from env vars (`MEMORY_SERVICE_URL`, `MEMORY_SERVICE_API_KEY`)
- Communicates with Claude Code via stdio (JSON-RPC)
- Calls memory-service REST API over HTTPS

### File structure

```
mcp/
  main.go           # Entry point, stdio transport
  server.go         # MCP server setup, tool registration
  tools.go          # Tool implementations (save, search, list, get, append)
  client.go         # HTTP client for memory-service REST API
  go.mod / go.sum
```

### Configuration

`.mcp.json` at project root (checked in):
```json
{
  "mcpServers": {
    "memory-service": {
      "command": "go",
      "args": ["run", "./mcp"],
      "env": {
        "MEMORY_SERVICE_URL": "${MEMORY_SERVICE_URL}",
        "MEMORY_SERVICE_API_KEY": "${MEMORY_SERVICE_API_KEY}"
      }
    }
  }
}
```

Developers set `MEMORY_SERVICE_URL` and `MEMORY_SERVICE_API_KEY` in their `.env` file.

## Phases

### Phase 1: Core tools (MVP) -- DONE
- [x] Set up Go MCP server with stdio transport
- [x] Implement `save_session_notes` tool
- [x] Implement `search_sessions` tool
- [x] Implement `list_sessions` tool
- [x] Implement `get_session` tool
- [x] Implement `append_note` tool
- [x] Add `.mcp.json` configuration
- [ ] Test end-to-end with Claude Code

### Phase 2: Enhanced session support
- [ ] Handle conversation forking (link related sessions)

### Phase 3: Team workflow
- [ ] Convention for session titles (e.g., `[claude-code] <date> <topic>`)
- [ ] Auto-tagging with metadata (user, branch, working directory)
- [ ] Document team usage patterns

## Dependencies

- Go MCP SDK: `github.com/mark3labs/mcp-go` (mature Go MCP library)
- Memory service REST API (no generated client needed - 5 endpoints)

## Open Questions

- Should the MCP server auto-save session summaries, or only when explicitly asked?
- What content format works best for session notes? Plain text vs structured entries?
- Should we use the `memory` channel (agent-scoped) or `history` channel for session notes?
