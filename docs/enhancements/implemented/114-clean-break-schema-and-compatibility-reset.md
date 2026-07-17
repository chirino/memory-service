---
status: implemented
---

# Enhancement 114: Clean-Break Schema and Compatibility Reset

> **Status**: Implemented.

## Summary

Establish the breaking release as a clean baseline. PostgreSQL, SQLite, and MongoDB restart at core schema version 1 and reject every earlier layout. The runtime supports only current MSEH field and attachment formats, canonical API error shapes, typed search cursors, strict OIDC audience validation, `X-API-Key` API-key authentication, generated HTTP route wrappers, and Rego v1 policies.

This release requires operators to reset both datastore and attachment contents. It does not provide an in-place or rolling upgrade path from pre-release builds.

## Motivation

Before a compatibility commitment, the service accumulated reset-only schema work, one-time encryption migration commands, old encryption readers, configuration aliases, and duplicate API response fields. Carrying these branches into the compatibility boundary would permanently enlarge the runtime and its test matrix. The breaking release is the opportunity to make the current contracts the only contracts.

## Design

### Datastore baseline

- Squash the current PostgreSQL and SQLite schemas into core schema version 1.
- Initialize MongoDB with the equivalent version-1 collections and indexes.
- Permit an empty datastore or the exact current version; reject missing, older, or otherwise incompatible schema metadata with reset guidance.
- Keep `conversation_ancestry` as the fork-lineage source of truth and remove dependencies on historical direct-fork persistence.
- Remove the PostgreSQL attachment-chunk table fallback; PostgreSQL attachments use large objects only.

### Encryption baseline

- Persisted fields use MSEH v4; encrypted attachment streams use MSEH v3.
- Reject MSEH v1 and v2 and remove their provider implementations, read flags, warnings, and metrics.
- Accept headerless values only when `plain` is the primary provider.
- Remove one-time attachment and encrypted-field migration commands and their atomic replacement interfaces.
- Preserve multi-key decryption for deliberate key rotation; rotation is current functionality, not a historical-format fallback.

### API and authentication baseline

- Return structured error details only under `details`; remove deprecated duplicate top-level fields.
- Accept only opaque typed search cursors.
- Require accepted audiences whenever OIDC is enabled; remove issuer-only compatibility mode.
- Accept API keys only in `X-API-Key`; bearer tokens remain OIDC tokens.
- Require structured authorization-policy decisions instead of accepting boolean results.

### CLI and configuration cleanup

- Remove compatibility-only encryption and OIDC flags.
- Remove deprecated `MEMORY_SERVICE_ENCRYPTION_KEY` and `MEMORY_SERVICE_QDRANT_HOST` aliases.
- Reduce `migrate` to current datastore initialization/convergence and document all supported datastore kinds.
- Rename environment parsing helpers so their names describe current behavior rather than Java compatibility.
- Remove parsed-but-unused configuration fields and flags, including the SSE membership-cache TTL and the misleading Infinispan vector TLS toggles. Infinispan TLS is selected by using an `https://` server URL.

### Runtime implementation cleanup

- Remove the old Gin `MountRoutes` registration stack; generated Agent/Admin wrappers are the only HTTP route registration path.
- Fold episodic memory wrapper delegation into the main wrapper server and remove the duplicate adapter implementation.
- Remove obsolete datastore pagination, ancestry, filtering, and attachment-listing helpers left behind by the current query paths.
- Return generated invalid-identifier errors without a route-specific response translation.
- Compile episodic authorization policies with OPA's v1 Rego API and use Rego v1 syntax in current policies and fixtures.
- Require Quarkus clients to use the canonical `memory-service.client.url` property; remove the REST-client property alias/fallback.

## Testing

Tests cover strict MSEH parsing, key rotation, canonical error payloads, typed cursor rejection, strict authentication, and version-1 SQLite initialization/rejection. Generated Go and TypeScript clients were refreshed from the OpenAPI source. Broad Go compilation plus affected Go, frontend, and Java module checks validate removed interfaces and configuration fields.

## Tasks

- [x] Restart all datastore baselines at core schema version 1.
- [x] Remove historical datastore and attachment migration paths.
- [x] Drop MSEH v1/v2 support and compatibility flags.
- [x] Remove deprecated API, cursor, authentication, policy, and config aliases.
- [x] Review and simplify migrate/serve CLI flags.
- [x] Regenerate clients and update operator documentation.
- [x] Remove obsolete route, datastore, configuration, and analyzer-confirmed runtime code.
- [x] Move episodic policies to OPA Rego v1 and remove the Quarkus client URL fallback.
- [x] Verify affected Go, frontend, and Java modules.

## Files to Modify

| Area | Files |
| --- | --- |
| Schema/store baseline | `internal/plugin/store/{postgres,sqlite,mongo}/`, `internal/plugin/attach/pgstore/` |
| Encryption | `internal/dataencryption/`, `internal/plugin/encrypt/`, `internal/plugin/attach/`, `internal/registry/` |
| CLI/config/auth | `internal/cmd/`, `internal/config/`, `internal/security/` |
| API contracts | `contracts/openapi/openapi.yml`, `internal/generated/`, `frontends/chat-frontend/src/client/` |
| Documentation | `AGENTS.md`, `internal/FACTS.md`, `docs/`, `site/`, Java module `FACTS.md` files |

## Verification

```bash
go test ./internal/dataencryption ./internal/plugin/encrypt/dek ./internal/security ./internal/plugin/route/search ./internal/plugin/route/transfers ./internal/config ./internal/episodic
go test ./internal/plugin/attach/...
CGO_ENABLED=1 go test -tags='sqlite_fts5' ./internal/plugin/store/sqlite -run TestSQLiteMigrator -count=1
go build ./...
npm --prefix frontends/chat-frontend run lint
npm --prefix frontends/chat-frontend run build
npm --prefix frontends/developer run lint
npm --prefix frontends/developer run build
./java/mvnw -f java/pom.xml -pl quarkus/memory-service-rest-quarkus,quarkus/memory-service-extension/deployment -am clean compile
./java/mvnw -f java/pom.xml -pl quarkus/memory-service-extension/deployment -am test -Dtest=MemoryServiceDevServicesProcessorTest -Dsurefire.failIfNoSpecifiedTests=false
```

## Non-goals

- Migrating or preserving datastore or attachment contents created by earlier builds.
- Supporting rolling upgrades between the earlier layout and this baseline.
- Removing supported key rotation or provider fallback for current MSEH formats.
- Rewriting historical enhancement documents beyond explicit supersession notes.
