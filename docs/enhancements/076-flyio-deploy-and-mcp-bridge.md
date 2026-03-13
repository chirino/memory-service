---
status: proposed
---

# Enhancement 076: Fly.io Deployment & MCP Bridge for Claude Code

> **Status**: Proposed — Phase 1 (MVP) implemented.

## Summary

Add a Fly.io deployment target for memory-service and an MCP bridge server that connects Claude Code to the deployed instance. This gives developers persistent, searchable, shared memory across Claude Code sessions.

## Motivation

Today, memory-service runs locally via `task dev:memory-service`. This limits its value for:

1. **Cross-session persistence**: Claude Code sessions are ephemeral. Decisions, discoveries, and solutions are lost when a session ends.
2. **Team knowledge sharing**: Multiple developers working on the same codebase cannot share context from their AI-assisted sessions.
3. **Remote access**: A local-only service cannot be reached from CI, other machines, or cloud-hosted tools.

A lightweight cloud deployment (Fly.io free tier) combined with an MCP bridge makes session knowledge durable and shared.

## Design

### Architecture

```
Claude Code  --stdio-->  MCP Server (local Go binary)  --HTTPS-->  Memory Service (Fly.io)
                              |                                        |
                         JSON-RPC / MCP protocol               REST API + Bearer auth
```

### MCP Tools

| Tool | Purpose | API Call |
|------|---------|----------|
| `save_session_notes` | Save a summary/notes from the current session | `POST /v1/conversations` + `POST /v1/conversations/{id}/entries` |
| `search_sessions` | Search past sessions by natural language query | `POST /v1/conversations/search` |
| `list_sessions` | List recent sessions | `GET /v1/conversations` |
| `get_session` | Retrieve entries from a specific past session | `GET /v1/conversations/{id}/entries` |
| `append_note` | Add a note to an existing session/conversation | `POST /v1/conversations/{id}/entries` |

### Fly.io Deployment

Minimal free-tier deployment: Go server + Postgres, API key auth.

| Feature | Status |
|---------|--------|
| Conversations API | Enabled |
| API key auth | Enabled |
| Postgres datastore | Enabled (auto-migrates) |
| Attachment storage | Enabled (in DB) |
| Redis caching | Disabled |
| Vector search (Qdrant) | Disabled |
| Embeddings (OpenAI) | Disabled |
| OIDC (Keycloak) | Disabled |

### DATABASE_URL Compatibility

Fly.io Postgres sets `DATABASE_URL` automatically via `fly postgres attach`. The serve and migrate commands now accept `DATABASE_URL` as a fallback for `MEMORY_SERVICE_DB_URL`.

### Configuration

Developers configure the MCP bridge via `.mcp.json` (checked in) and a local `.env` file (not checked in) with `MEMORY_SERVICE_CLIENT_URL` and `MEMORY_SERVICE_CLIENT_API_KEY`.

## Implementation Phases

### Phase 1: Core (MVP) — Done

- [x] Fly.io deployment config (`fly.toml`) and deploy script (`deploy/fly/`)
- [x] MCP bridge server in Go (`mcp/`) with stdio transport
- [x] All 5 MCP tools implemented
- [x] `.mcp.json` project configuration
- [x] `DATABASE_URL` fallback in serve and migrate commands

### Phase 2: Enhanced session support

- [ ] End-to-end testing with Claude Code
- [ ] Handle conversation forking (link related sessions)

### Phase 3: Team workflow

- [ ] Convention for session titles (e.g., `[claude-code] <date> <topic>`)
- [ ] Auto-tagging with metadata (user, branch, working directory)
- [ ] Document team usage patterns

## Files to Modify

| File | Purpose |
|------|---------|
| `fly.toml` | Fly.io app configuration |
| `deploy/fly/deploy.sh` | First-time and redeploy script |
| `deploy/fly/README.md` | Deployment documentation |
| `mcp/main.go` | MCP server entry point |
| `mcp/client.go` | HTTP client for memory-service REST API |
| `mcp/tools.go` | MCP tool definitions and handlers |
| `.mcp.json` | Claude Code MCP server configuration |
| `internal/cmd/serve/serve.go` | Accept `DATABASE_URL` fallback |
| `internal/cmd/migrate/migrate.go` | Accept `DATABASE_URL` fallback |

## Testing

- [ ] Manual end-to-end: deploy to Fly.io, connect MCP bridge, save/search/list sessions
- [ ] Verify `DATABASE_URL` fallback works with Fly.io Postgres attach

## Open Questions

1. Should the MCP server auto-save session summaries, or only when explicitly asked?
2. What content format works best for session notes? Plain text vs structured entries?
3. Should we use the `memory` channel (agent-scoped) or `history` channel for session notes?
