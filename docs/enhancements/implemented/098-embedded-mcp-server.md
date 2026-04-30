---
status: implemented
---

# Enhancement 098: Embedded Memory Service in `mcp embedded`

> **Status**: Implemented.

## Summary

Add a dedicated `memory-service mcp embedded` subcommand that runs an embedded, in-process memory service instead of connecting to a remote one. The MCP tool handlers invoke the HTTP handlers of the embedded service directly (no network or unix socket hop), and the embedded subcommand accepts the same configuration inputs as `serve`.

## Motivation

Today [internal/cmd/mcp/cmd.go](internal/cmd/mcp/cmd.go) only supports a remote bridge to a separately-running memory service. That makes the simplest "single binary, single user, local memory" setup awkward:

- Users must run `memory-service serve` and `memory-service mcp` as two processes.
- Local CLI tools (Claude Code, etc.) need credentials and a reachable URL just to talk to a service that lives on the same machine.
- The bridge introduces an extra serialization, transport, and auth hop for every tool call even though both ends are in the same process.

An embedded subcommand collapses this into a single `memory-service mcp embedded ...` invocation that spins up the full memory service in-process and dispatches MCP tool calls straight into the HTTP handler — no sockets, no API keys, no extra latency.

The standalone `memory-service-mcp` binary remains a single-command remote wrapper. It should continue to expose today's remote-bridge UX rather than mirroring the new subcommand tree.

## Design

### Command structure

The `mcp` command becomes a parent with two explicit subcommands:

1. `memory-service mcp remote`: build an HTTP `apiclient` and proxy MCP tool calls to an already-running memory service.
2. `memory-service mcp embedded`: build the full memory-service stack in-process using the same configuration as `serve`, with embedded-specific defaults (`db-kind=sqlite`, `cache-kind=none`, `attachments-kind=fs`, `embedding-kind=none`, semantic search disabled) and `--db-url` still required, and back the MCP tools with an in-process client that calls the `http.Handler` directly via `httptest.NewRecorder` / `handler.ServeHTTP`.

There is no mode-switching on flags. `remote` keeps its connection/auth flags, while `embedded` accepts datastore/cache/vector/auth settings for the in-process server. Because the project is pre-release, `memory-service mcp` does not keep its old direct-action behavior; users must choose `remote` or `embedded` explicitly.

The standalone `memory-service-mcp` binary continues to behave like today's remote bridge. Internally it can either keep constructing the remote MCP server directly or delegate to the same implementation used by `memory-service mcp remote`, but it should not require `remote` or `embedded` as an extra command word.

### Flag composition

The `serve` package currently builds its flag set in `flags(...)`. We extract that into an exported helper so `mcp embedded` can reuse the same config struct and nearly all of the same flag definitions:

```go
// internal/cmd/serve/serve.go
func Flags(cfg *Config, ...) []cli.Flag { return flags(cfg, ...) }

// internal/cmd/serve/server.go
func BuildServer(ctx context.Context, cfg *Config) (*Server, error) { ... }
```

`BuildServer` factors out the body of the current `serve` startup flow up to the point where listeners are started, returning the existing `*serve.Server` shape with `Router`, `GRPCServer`, store handles, and shutdown support populated but without binding sockets. `serve` continues to call it and then starts listeners; `mcp embedded` calls it and keeps the handler in memory.

### In-process API client

`apiclient.ClientWithResponses` is constructed today with `WithHTTPClient(&http.Client{...})`. We introduce an `http.RoundTripper` that dispatches against an `http.Handler` instead of the network:

```go
// internal/cmd/mcp/inprocess.go
type handlerTransport struct{ h http.Handler }

func (t *handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
    rec := httptest.NewRecorder()
    t.h.ServeHTTP(rec, req)
    return rec.Result(), nil
}
```

The embedded MCP server is then wired with:

```go
client, _ := apiclient.NewClientWithResponses(
    "http://embedded.local",
    apiclient.WithHTTPClient(&http.Client{Transport: &handlerTransport{h: srv.Router}}),
    apiclient.WithRequestEditorFn(injectEmbeddedAuth),
)
```

This means **zero changes** to the existing `registerTools` code path — it keeps using `apiclient.ClientWithResponses`, but every call short-circuits through the in-process handler.

### Authentication in embedded mode

`mcp embedded` should not assume a special auth bypass. The current dev setup still uses the normal auth stack (API key and/or OIDC bearer-token resolution), and the current MCP client path already injects those headers. Embedded mode should preserve that model by injecting a synthetic local principal into the in-process requests or by configuring a dedicated embedded-only token/API-key pair during startup. The design should pick one explicit approach rather than relying on undocumented dev behavior.

### Lifecycle

`mcp embedded`:

1. Parses `serve`-style flags into `serve.Config`.
2. Calls `serve.BuildServer(ctx, &cfg)` to construct stores, plugins, router, etc.
3. Constructs the in-process `apiclient` against `srv.Router`.
4. Calls `registerTools(s)` and `mcpserver.ServeStdio(s.server)` as today.
5. On stdio shutdown, runs `srv.Shutdown(...)`.

No TCP listener and no unix socket are started in embedded mode. The current MCP implementation is HTTP/OpenAPI-based, so `mcp embedded` only needs the in-process HTTP handler for this enhancement. Keeping gRPC construction reusable may still be useful as a follow-on refactor, but it is not required to land embedded MCP support.

## Non-Goals

- Multi-user auth in embedded mode. `mcp embedded` assumes a single local trusted user.
- Sharing the embedded service with other processes. If you need that, run `serve` and use remote mode.

## Testing

### Unit

- `handlerTransport` round-trips a simple request against a stub `http.Handler` and returns the recorded response, headers, and status.
- Embedded startup with an in-memory SQLite + local cache config builds a router without error.
- Remote startup still uses the existing bridge behavior unchanged.

### Cucumber / integration

```gherkin
Feature: Embedded MCP server

  Scenario: MCP tool call hits the in-process handler
    Given memory-service mcp embedded is started with sqlite and local cache
    When the MCP client calls the "save_session_notes" tool with sample notes
    Then the notes are persisted in the embedded store
    And no TCP or unix socket listener was opened
```

## Tasks

- [x] Refactor `internal/cmd/mcp/cmd.go` so `mcp` becomes a parent command with `remote` and `embedded` subcommands and no direct action.
- [x] Keep `memory-service mcp remote` wired to the current bridge behavior and existing remote-only flags.
- [x] Keep `memory-service-mcp` as a single-command remote wrapper; do not require `remote` or `embedded` there.
- [x] Extract `serve.Flags(...)` and `serve.BuildServer(...)` from the current `serve` startup flow.
- [x] Update `internal/cmd/serve/serve.go` to call `BuildServer` so behavior is unchanged.
- [x] Add `internal/cmd/mcp/inprocess.go` with `handlerTransport`.
- [x] Update `mcp embedded` to reuse `serve` config parsing and wire the embedded path through `handlerTransport`.
- [x] Decide and implement the embedded auth strategy (for example synthetic principal headers vs an embedded-only API key/bearer pair).
- [x] Add unit tests for `handlerTransport` and embedded startup.
- [x] Add unit tests covering `mcp remote` and `mcp embedded` command construction.
- [x] Add a Cucumber scenario covering an embedded MCP tool call end-to-end.
- [x] Update `memory-service-mcp/` standalone wrapper docs/README to mention embedded mode.

## Files to Modify

| File | Change |
|------|--------|
| [internal/cmd/serve/serve.go](internal/cmd/serve/serve.go) | Export `Flags` and update `serve` to call `BuildServer`. |
| [internal/cmd/serve/server.go](internal/cmd/serve/server.go) | Extract `BuildServer` so router construction is reusable without starting a listener. |
| [internal/cmd/mcp/cmd.go](internal/cmd/mcp/cmd.go) | Convert `mcp` into a parent command with `remote` and `embedded` subcommands. |
| `internal/cmd/mcp/inprocess.go` (new) | `handlerTransport` implementing `http.RoundTripper` over an `http.Handler`. |
| `internal/cmd/mcp/cmd_test.go` (new) | Unit tests for subcommand wiring, transport, and embedded startup. |
| `internal/bdd/features/mcp_embedded.feature` (new) | Cucumber scenarios for embedded MCP. |
| [memory-service-mcp/main.go](memory-service-mcp/main.go) | Keep standalone wrapper behavior as single-command remote mode while reusing shared remote setup where practical. |
| [memory-service-mcp/](memory-service-mcp/) | README/usage updates describing wrapper-vs-subcommand behavior. |

## Verification

```bash
# Compile
go build ./...

# Unit tests for cmd packages
go test ./internal/cmd/mcp/... ./internal/cmd/serve/... > test.log 2>&1

# Smoke test embedded mode against a sqlite config
./memory-service mcp embedded --db-url 'file:embedded.db?cache=shared' < /dev/null
```
