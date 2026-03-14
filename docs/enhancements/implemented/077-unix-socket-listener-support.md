---
status: implemented
---

# Enhancement 077: Unix Socket Listener Support

> **Status**: Implemented.

## Summary

Add first-class Unix domain socket listener support to the Go memory service so it can run without binding a TCP port. The implementation includes secure socket creation and cleanup, CLI and env configuration for main and management listeners, tests, and site documentation updates.

## Motivation

Today the Go service can only bind TCP listeners:

1. The main HTTP+gRPC listener hardcodes `net.Listen("tcp", ...)` in `internal/cmd/serve/singleport.go`.
2. The management listener hardcodes `net.Listen("tcp", ...)` in `internal/cmd/serve/management.go`.
3. `internal/config.ListenerConfig` models listeners as `Port int` only.
4. Response-resumer location and redirect logic assumes `host:port` addresses.
5. Site documentation and site-doc test tooling assume `http://localhost:<port>` access patterns.

For local agent deployments this is unnecessarily exposed. A Unix socket is a better fit when:

1. The service and the calling agent run on the same machine.
2. No browser or remote client needs direct access.
3. The operator wants OS-level filesystem permissions to restrict access to the current user.

This should be a first-class runtime mode rather than a reverse-proxy workaround.

## Design

### Scope

This enhancement covers:

1. Unix socket listeners for the main API listener.
2. Unix socket listeners for the dedicated management listener.
3. Secure socket lifecycle management.
4. CLI/env plumbing.
5. Test coverage for listener behavior.
6. Site documentation updates describing configuration and usage.

This enhancement does not cover:

1. Windows named pipes.
2. Browser access to the service over Unix sockets.
3. Kubernetes deployment support for Unix sockets.
4. Java/Spring/Quarkus/Python/TypeScript client-library changes.
5. Response-recorder redirect changes or `redirect_address` contract changes.
6. `internal/sitebdd` Unix-socket execution support.

### Configuration Surface

Extend `internal/config.ListenerConfig` with an optional Unix socket path:

```go
type ListenerConfig struct {
    Port              int
    UnixSocket        string
    EnablePlainText   bool
    EnableTLS         bool
    TLSCertFile       string
    TLSKeyFile        string
    ReadHeaderTimeout time.Duration
}
```

Add new flags and env vars:

| Listener | CLI flag | Env var | Meaning |
|----------|----------|---------|---------|
| Main | `--unix-socket` | `MEMORY_SERVICE_UNIX_SOCKET` | Absolute path to the main HTTP/gRPC Unix socket |
| Management | `--management-unix-socket` | `MEMORY_SERVICE_MANAGEMENT_UNIX_SOCKET` | Absolute path to the dedicated management Unix socket |

Validation rules:

1. `--port` and `--unix-socket` are mutually exclusive for the same listener when both are explicitly provided by the user via CLI flag or env var.
2. `--management-port` and `--management-unix-socket` are mutually exclusive for the management listener when both are explicitly provided by the user via CLI flag or env var.
3. When `--unix-socket` is explicitly selected, the default port value may remain in config but is ignored for that listener.
4. When `--management-unix-socket` is explicitly selected, the default management port value may remain in config but is ignored for that listener.
5. Unix socket paths must be absolute.
6. `ManagementListenerEnabled` becomes true when either `management-port` or `management-unix-socket` is explicitly set.

### Listener Abstraction

Introduce a shared listener helper used by both the main and management servers:

```go
type PreparedListener struct {
    Listener net.Listener
    Network  string // "tcp" or "unix"
    Address  string // ":8082" or "/abs/path/memory-service.sock"
    Cleanup  func() error
}

func prepareListener(cfg config.ListenerConfig) (*PreparedListener, error)
```

Behavior:

1. TCP mode preserves current behavior.
2. Unix mode calls `net.Listen("unix", path)`.
3. `Cleanup()` removes the socket file on shutdown.
4. `Cleanup()` also runs when startup fails after the listener has been created.

`internal/cmd/serve/singleport.go` and `internal/cmd/serve/management.go` should stop constructing listeners directly and instead use the shared helper.

### Secure Socket Semantics

For Unix sockets, the helper must enforce current-user-only access:

1. Create the parent directory when missing with mode `0700`.
2. Reject a parent directory that is not a directory.
3. If the parent directory already exists, fail fast when it is group- or world-accessible.
4. Remove a stale socket file before listening.
5. Reject an existing non-socket file at the target path.
6. After `net.Listen("unix", ...)`, set socket permissions to `0600`.
7. Remove the socket file during shutdown.

The service should secure only directories it creates itself. It should not auto-`chmod`
an existing parent directory; existing insecure directories are a configuration error.

Stale socket handling:

1. If the path exists and is a socket, attempt a short dial.
2. If the dial succeeds, return an error because another server is already active.
3. If the dial fails, treat the socket as stale and remove it before binding.

The service should log the effective listener address with `addr`, not just `port`, because Unix-socket mode has no useful numeric port.

### Main and Management Listener Behavior

Main listener:

1. Supports either TCP or Unix socket.
2. Continues to multiplex HTTP/1.1, h2c, and gRPC on a single listener.

Management listener:

1. Supports either TCP or Unix socket when explicitly enabled.
2. Continues serving HTTP-only routes.
3. Shares the main listener when no dedicated management listener is configured.

Representative CLI usage:

```bash
memory-service serve \
  --db-url=postgresql://postgres:postgres@localhost:5432/memoryservice \
  --plain-text=true \
  --tls=false \
  --unix-socket="$HOME/.local/run/memory-service/api.sock"
```

Dedicated management socket:

```bash
memory-service serve \
  --db-url=postgresql://postgres:postgres@localhost:5432/memoryservice \
  --plain-text=true \
  --tls=false \
  --unix-socket="$HOME/.local/run/memory-service/api.sock" \
  --management-unix-socket="$HOME/.local/run/memory-service/mgmt.sock"
```

### Response Recorder Scope Boundary

This enhancement does not change response-recorder redirect behavior.

Specifically:

1. No protobuf contract changes are planned for `redirect_address`.
2. No client-library work is planned to interpret Unix-socket redirect targets.
3. Unix-socket support is aimed at local single-instance agent deployments where redirect-following is not needed.
4. Any future cross-instance response-recorder support for Unix endpoints should be proposed separately.

### Running Server Metadata

`RunningServers` currently exposes `Port int`, which is meaningful only for TCP. Expand it so logs and tests can reason about either transport:

```go
type RunningServers struct {
    Addr            net.Addr
    Port            int
    Endpoint        string
    Network         string
    HTTPServerPlain *http.Server
    HTTPServerTLS   *http.Server
    GRPCServer      *grpc.Server
    Close           func(ctx context.Context) error
}
```

Rules:

1. `Port` is populated only for TCP listeners.
2. `Endpoint` is always populated.
3. `Network` is `tcp` or `unix`.

### Site Documentation Updates

Update the public docs to document Unix-socket mode and its limitations.

Primary pages:

1. `site/src/pages/docs/configuration.mdx`
2. `site/src/pages/docs/faq.mdx`

Required content:

1. New config rows for `--unix-socket` / `MEMORY_SERVICE_UNIX_SOCKET`.
2. New config rows for `--management-unix-socket` / `MEMORY_SERVICE_MANAGEMENT_UNIX_SOCKET`.
3. Explain that browser apps cannot connect directly to a Unix socket.
4. Provide `curl --unix-socket` examples.

Example docs snippet:

```bash
curl --unix-socket "$HOME/.local/run/memory-service/api.sock" \
  http://localhost/ready

curl --unix-socket "$HOME/.local/run/memory-service/api.sock" \
  -H "Authorization: Bearer agent-a" \
  http://localhost/v1/conversations
```

## Testing

### Go Unit and Integration Tests

Add tests for:

1. Listener validation rejects `port + unix-socket` and `management-port + management-unix-socket`.
2. Unix socket startup creates a socket with mode `0600`.
3. Startup creates a missing parent directory with mode `0700`.
4. Startup fails fast when an existing parent directory is group/world-accessible.
5. Startup removes a stale socket file.
6. Shutdown removes the socket file.
7. HTTP `/ready` works over Unix sockets.
8. gRPC health and service RPCs work over Unix sockets using a custom dialer.
9. Management listener works over a dedicated Unix socket.
10. `RunningServers.Endpoint` and `RunningServers.Network` are populated correctly.

## Tasks

- [x] Extend `ListenerConfig` with Unix-socket configuration for main and management listeners.
- [x] Add `--unix-socket` / `MEMORY_SERVICE_UNIX_SOCKET` and `--management-unix-socket` / `MEMORY_SERVICE_MANAGEMENT_UNIX_SOCKET`.
- [x] Add listener validation for mutually exclusive TCP and Unix-socket settings.
- [x] Introduce a shared listener-preparation helper for TCP and Unix transports.
- [x] Implement secure Unix-socket directory creation, permission checks, stale-socket cleanup, and shutdown cleanup.
- [x] Refactor the main single-port server to use the shared listener helper.
- [x] Refactor the management server to use the shared listener helper.
- [x] Expand `RunningServers` to expose transport-independent endpoint metadata.
- [x] Add unit and integration tests for listener lifecycle and Unix-socket transport behavior.
- [x] Update site docs for configuration and local Unix-socket usage.
- [x] Run Go build/test and site build verification for all touched areas.

## Files to Modify

| File | Purpose |
|------|---------|
| `docs/enhancements/077-unix-socket-listener-support.md` | Enhancement plan |
| `internal/config/config.go` | Add Unix-socket fields to listener configuration |
| `internal/cmd/serve/serve.go` | Add CLI/env flags and validation wiring |
| `internal/cmd/serve/singleport.go` | Use shared listener helper for main HTTP+gRPC listener |
| `internal/cmd/serve/management.go` | Use shared listener helper for management listener |
| `internal/cmd/serve/server.go` | Log and expose transport-independent endpoint metadata |
| `internal/cmd/serve/listener.go` | New shared helper for TCP/Unix listener preparation and cleanup |
| `internal/cmd/serve/listener_test.go` | Listener lifecycle, permissions, and Unix transport tests |
| `site/src/pages/docs/configuration.mdx` | Document Unix-socket config surface |
| `site/src/pages/docs/faq.mdx` | Document local-only usage and browser limitations |

## Design Decisions

### No Redirect Contract Changes

Do not change `redirect_address`, `--advertised-address`, or the response-recorder client contracts in this enhancement. Unix-socket listener support is intentionally limited to local deployments that do not need redirect-following behavior.

### Explicit-Source Validation

Mutual exclusivity between `port` and `unix-socket` should be based on whether
the user explicitly set those values via CLI flags or env vars, not on the
resolved config value alone. This avoids false conflicts with existing default
port values such as `8080`.

### Create-or-Fail Directory Policy

If the socket parent directory does not exist, create it with mode `0700`.
If it already exists and is insecure, fail fast rather than mutating it.

## Security Considerations

1. The socket path must be absolute so the effective location is unambiguous.
2. Parent directories must not be group- or world-accessible if we claim current-user-only access.
3. The server must not overwrite an existing non-socket file.
4. Stale socket cleanup must distinguish between an inactive socket file and a live server.
5. Shutdown cleanup must remove the socket file to avoid accidental reuse and startup confusion.

## Verification

```bash
# Build the Go module after the listener/config changes
go build ./...

# Run focused serve-package tests
go test ./internal/cmd/serve

# Install site dependencies and verify the docs build
cd site && npm ci && npm run build
```
