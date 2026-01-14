# Infinispan Cache Support for memory-service (Draft)

## Problem Summary
The memory-service currently uses Redis only for the response-resumer locator
store (stream resume/cancel metadata). The conversation cache is present as an
API (`ConversationCache`) but is currently a no-op implementation. We need to
make the response-resumer store (and the future conversation cache) pluggable
so deployments can switch between Redis and Infinispan via configuration.

## Goals
- Allow selecting Redis or Infinispan for the response-resumer locator store via
  application config.
- Prepare for a Redis/Infinispan-backed `ConversationCache` when implemented.
- Keep cache/resumer semantics consistent across backends.
- Provide equal support in the `memory-service-extension` dev services.
- Keep configuration ergonomic and aligned with Quarkus patterns.

## Non-Goals
- Replacing the primary datastore (PostgreSQL or MongoDB).
- Changing user-visible API behavior.
- Implementing a custom cache API; prefer Quarkus extensions and config.

## Current State (Code Scan)
- Conversation cache:
  - Config: `memory-service.cache.type` (default `none`).
  - `CacheSelector` only returns `NoopConversationCache`.
  - README notes Redis/Infinispan cache are planned, not implemented.
- Response resumer:
  - Config: `memory-service.response-resumer` (default `none`).
  - Redis implementation exists (`RedisResponseResumerLocatorStore`).
  - Redis client selection via `memory-service.response-resumer.redis.client`.
- Extension dev services:
  - Redis Dev Service wiring happens only when
    `memory-service.response-resumer=redis`.
  - No Infinispan Dev Service wiring yet.

## Proposed Design

### 1) Response-Resumer Backend Abstraction
Use a provider selector for response-resumer locator storage:

- `memory-service.response-resumer=redis|infinispan|none`
- Backends configured using Quarkus extensions:
  - Redis via `quarkus-redis-client` (already used)
  - Infinispan via `quarkus-infinispan-client`

The memory-service should:
- Provide a `ResponseResumerLocatorStore` implementation for Infinispan.
- Keep the selector (`ResponseResumerSelector`) symmetric: redis/infinispan/none.

### 2) Configuration Shapes
Add configuration examples for the response-resumer:

Redis example:
```
memory-service.response-resumer=redis
memory-service.response-resumer.redis.client=default
quarkus.redis.hosts=redis://localhost:6379
quarkus.redis.devservices.enabled=true
```

Infinispan example:
```
memory-service.response-resumer=infinispan
quarkus.infinispan-client.server-list=localhost:11222
quarkus.infinispan-client.devservices.enabled=true
```

Optional tuning should map to backend-specific settings, for example:
- Redis connection pool size and timeouts.
- Infinispan connection pool size and auth.

### 3) Service Wiring
Response resumer:
- Add `InfinispanResponseResumerLocatorStore` implementing
  `ResponseResumerLocatorStore`.
- Mirror TTL/key semantics from Redis to keep resume behavior consistent.

Conversation cache (future):
- Keep `ConversationCache` abstraction.
- Add `RedisConversationCache` and `InfinispanConversationCache` when cache is
  implemented; select via `memory-service.cache.type`.

### 4) Dev Services and the Extension
The `memory-service-extension` should support both response-resumer backends:

- When `memory-service.response-resumer=redis`:
  - Start Redis Dev Service when not configured externally.
  - Wire `quarkus.redis.hosts` to the container.
- When `memory-service.response-resumer=infinispan`:
  - Start Infinispan Dev Service when not configured externally.
  - Wire `quarkus.infinispan-client.server-list` to the container.

No preference should be given to either backend; both are first-class options.

### 5) Compatibility and Data Model
Response-resumer keys and value schemas must be compatible across backends:
- Keys should be stable and use ASCII-safe strings.
- TTL semantics should be explicit and consistent.
- If structured values are stored, use a common serialization format
  (e.g., JSON or protobuf bytes).

## Implementation Plan
1) Define response-resumer configuration
   - Extend `memory-service.response-resumer` to accept `infinispan`.
   - Add example config blocks for Redis and Infinispan.
2) Implement Infinispan locator store
   - Add `InfinispanResponseResumerLocatorStore`.
   - Match Redis key/TTL behavior.
3) Update response-resumer selector
   - Add `infinispan` case to `ResponseResumerSelector`.
   - Add clear startup errors when config is invalid or client missing.
4) Update extension dev services
   - Detect `memory-service.response-resumer`.
   - Start Redis or Infinispan Dev Services accordingly.
   - Configure corresponding client URLs in the container.
5) Conversation cache (future follow-up)
   - Implement Redis/Infinispan `ConversationCache`.
   - Wire `CacheSelector` to choose based on `memory-service.cache.type`.
6) Documentation updates
   - Update `README.md` and `docs/design.md` as needed.

## Testing Plan
### Unit Tests
- Response-resumer selection logic:
  - `redis` -> `RedisResponseResumerLocatorStore`
  - `infinispan` -> `InfinispanResponseResumerLocatorStore`
  - Unknown value -> startup failure

### Integration Tests
- Add Cucumber scenarios to validate resume/cancel behaviors under both
  backends 
- To avoid a matrix explosion, map cache backends to datastore profiles:
  - MongoDB Cucumber profile runs with Redis response-resumer.
  - PostgreSQL Cucumber profile runs with Infinispan response-resumer.  

### Extension Tests
- Verify `memory-service-extension` dev services bring up the correct container
  for each response-resumer backend.
- Validate that the container receives the correct client URL config.

## Open Questions
- Should `memory-service.response-resumer` default to `redis` when enabled by
  profiles, or remain explicit?  Remain explicit.
- How should we expose Infinispan auth config in Dev Services? NOt sure.
- Do we need a migration strategy for existing Redis-only deployments?  No.
