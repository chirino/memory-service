---
status: implemented
---

# Enhancement 074: Migrate Go HTTP Routing to oapi ServerInterfaceWrapper

> **Status**: Implemented.

## Summary

Move Go HTTP route binding from hand-registered Gin handlers to generated oapi `ServerInterfaceWrapper` bindings. The migration completed endpoint-by-endpoint with parity validation against existing tests.

## Motivation

Current routing is split across multiple manual `MountRoutes(...)` implementations, which increases risk of contract drift (path params, query binding, validation, and response code differences). Generated wrappers centralize parameter binding and route registration behavior from the OpenAPI contract.

A direct cutover is high risk. We need a mixed-mode migration with explicit fallback to legacy handlers when wrapper-bound endpoints fail tests.

## Design

### 1. Route Ownership Model

All Agent and Admin HTTP endpoints are now bound through generated wrappers.

### 2. Registration Strategy

Generated wrappers are registered explicitly per endpoint in `internal/cmd/serve/wrapper_routes.go` to keep binding behavior contract-driven while preserving control over middleware and handler wiring.

### 3. Handler Adapter Strategy

Wrapper handlers now delegate directly to route package logic via exported handler entry points and adapters (for example memories), without runtime legacy-router proxying.

### 4. Completion Criteria

Legacy route registration in `serve` has been removed after parity was validated via existing tests.

## Testing

Use existing tests as parity gate. No behavior change is intended.

```gherkin
Feature: Wrapper migration parity
  Scenario: Wrapper endpoint matches legacy behavior
    Given endpoint ownership for "GET /v1/conversations/:conversationId" is "wrapper"
    When existing feature tests run
    Then all assertions for status, payload shape, and auth behavior remain unchanged

  Scenario: Endpoint fallback after wrapper regression
    Given endpoint ownership for "POST /v1/conversations/:conversationId/entries" is "wrapper"
    And tests fail with a reproducible regression
    When endpoint ownership is switched back to "legacy"
    Then existing tests pass again
    And a workaround entry is added to WORKAROUNDS.md
```

## Tasks

- [x] Add migration scaffolding for per-endpoint ownership (`wrapper` vs `legacy`).
- [x] Add selective wrapper route registration (no full-surface `RegisterHandlers` call).
- [x] Implement adapter/delegation layer to call legacy logic where needed.
- [x] Migrate first endpoint group (`/v1/memories*`) using wrapper+fallback mixed mode.
- [x] Migrate remaining Agent/Admin endpoint groups to wrapper+fallback mixed mode.
- [x] Convert `/v1/memories*` from wrapper-proxy to wrapper-native handler implementation.
- [x] Convert `/v1/ownership-transfers*` from wrapper-proxy to wrapper-native handler implementation.
- [x] Convert memberships endpoints from wrapper-proxy to wrapper-native handler implementation.
- [x] Convert attachments endpoints from wrapper-proxy to wrapper-native handler implementation.
- [x] Convert search/index endpoints (`/v1/conversations/search|index|unindexed`) from wrapper-proxy to wrapper-native handler implementation.
- [x] Run existing unit + integration + site-bdd tests after each migrated endpoint group.
- [x] On any endpoint regression, revert ownership for that endpoint to `legacy` (used during migration while parity issues were fixed).
- [x] Record each fallback in `WORKAROUNDS.md` (failure type, reason, proper fix).
- [x] Complete migration to full wrapper ownership (default path).
- [x] Remove legacy route registration and dead code after parity is proven.

## Files to Modify

| File | Change |
| --- | --- |
| `internal/cmd/serve/server.go` | Register wrapper-native routing and remove legacy runtime route mounting. |
| `internal/cmd/serve/wrapper_routes.go` | Full Agent/Admin wrapper route registration + wrapper-native handler adapters. |
| `internal/generated/api/api.gen.go` | No direct edits; use generated wrapper methods for binding. |
| `internal/generated/admin/admin.gen.go` | No direct edits; use generated wrapper methods for binding. |
| `internal/plugin/route/memories/memories.go` | Legacy memories handlers + shared helper logic used by wrapper-native adapter. |
| `internal/plugin/route/memories/wrapper_adapter.go` | Wrapper-native `generatedapi.ServerInterface` adapter for `/v1/memories*`. |
| `internal/plugin/route/attachments/attachments.go` | Exported helper entry points used by wrapper-native attachment handlers. |
| `internal/plugin/route/memberships/memberships.go` | Exported helper entry points used by wrapper-native membership handlers. |
| `internal/plugin/route/search/search.go` | Exported helper entry points used by wrapper-native search/index handlers. |
| `internal/plugin/route/transfers/transfers.go` | Exported helper entry points used by wrapper-native transfer handlers. |
| `internal/config/config.go` | Removed temporary mixed-mode wrapper routing controls after migration completion. |
| `internal/config/compat.go` | Removed temporary mixed-mode wrapper routing env parsing. |
| `internal/cmd/serve/serve.go` | Removed temporary mixed-mode wrapper routing CLI flags. |
| `internal/**/*_test.go` | Add/update parity tests for wrapper vs legacy routing behavior. |
| `internal/sitebdd/**/*.feature` | Keep existing scenarios as migration parity gate. |
| `WORKAROUNDS.md` | Record fallback workaround entries for wrapper regressions. |

## Verification

```bash
# Fast compile gate
go build ./...

# Targeted route and server tests
go test ./internal/cmd/serve ./internal/plugin/route/... -count=1

# Integration parity gate (devcontainer/worktree required)
wt up
wt exec -- go test ./internal/bdd -run TestFeaturesPgKeycloak -count=1 > bdd.log 2>&1
rg -n "FAIL|panic|error| 500 " bdd.log
```

## Design Decisions

- Endpoint-by-endpoint migration reduced blast radius and made failures easier to isolate.
- Wrapper middleware must mirror old route-group behavior (auth and client-id where required) to avoid authorization regressions.
- `WORKAROUNDS.md` is the single source of truth for temporary fallback decisions.

## Non-Goals

- No API contract changes.
- No feature behavior changes unrelated to route-binding parity.
- No test expectation rewrites during initial migration unless a contract bug is confirmed.

## Open Questions

- None.
