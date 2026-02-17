---
status: proposed
---

# Enhancement 057: Unified Config Key Naming

> **Status**: Proposed.

## Summary

Audit all configuration keys and consolidate them under a consistent `memory-service.*` prefix with uniform naming conventions. This continues the work started by [Enhancement 051 (Datastore Config Isolation)](051-datastore-config-isolation.md) and [Enhancement 053 (Redis Config Consolidation)](053-redis-config-consolidation.md).

## Motivation

The configuration surface has evolved organically over many enhancements. While the `memory-service.*` prefix is established as the user-facing namespace, several inconsistencies remain:

### 1. Mixed Naming Conventions

Some keys use hyphens, others don't. Most follow `kebab-case` consistently, but the hierarchy depth varies:

```properties
# Flat
memory-service.cors.enabled=true
memory-service.cors.origins=...

# Nested
memory-service.attachments.s3.bucket=...
memory-service.attachments.s3.direct-download=true

# Inconsistent depth
memory-service.admin.require-justification=false   # "admin" is a feature area
memory-service.eviction.batch-size=1000            # "eviction" is a feature area
memory-service.tasks.processor-interval=1m         # "tasks" is a feature area
```

### 2. Data Encryption Uses a Different Prefix

```properties
# Current: separate namespace
data.encryption.providers=plain
data.encryption.provider.vault.type=vault

# Expected: under memory-service
memory-service.encryption.providers=plain
memory-service.encryption.provider.vault.type=vault
```

### 3. Some Quarkus Keys Still Leak to Users

Users sometimes need to set raw `quarkus.*` keys that should be abstracted:

```properties
# Users set this directly for OIDC
quarkus.oidc.auth-server-url=...
quarkus.oidc.client-id=...
quarkus.oidc.credentials.secret=...

# Could be abstracted as:
memory-service.oidc.auth-server-url=...
memory-service.oidc.client-id=...
memory-service.oidc.credentials.secret=...
```

### 4. No Central Documentation of All Keys

There is no single reference that lists all `memory-service.*` keys with their types, defaults, and descriptions.

## Design

### Phase 1: Naming Convention Standard

Establish the canonical naming convention for all keys:

```
memory-service.<feature-area>.<sub-area>.<property-name>
```

Rules:
- **All keys start with `memory-service.`** — no exceptions for user-facing config.
- **`kebab-case`** for all segments.
- **Feature areas** group related settings: `datastore`, `cache`, `vector`, `search`, `attachments`, `tasks`, `eviction`, `cors`, `roles`, `encryption`, `grpc`, `oidc`.
- **Depth limit**: Maximum 4 segments (e.g., `memory-service.cache.infinispan.startup-timeout`).
- **Boolean properties**: Use positive naming (e.g., `enabled` not `disabled`).

### Phase 2: Consolidate Remaining Outliers

#### Data Encryption

Move `data.encryption.*` under `memory-service.encryption.*`:

| Current Key | New Key |
|-------------|---------|
| `data.encryption.providers` | `memory-service.encryption.providers` |
| `data.encryption.provider.<id>.type` | `memory-service.encryption.provider.<id>.type` |
| `data.encryption.provider.<id>.enabled` | `memory-service.encryption.provider.<id>.enabled` |

Implementation: Update the `@ConfigMapping(prefix = "data.encryption")` annotation to `@ConfigMapping(prefix = "memory-service.encryption")`.

#### OIDC Abstraction

Provide `memory-service.oidc.*` aliases that map to `quarkus.oidc.*`:

| New Key | Maps To |
|---------|---------|
| `memory-service.oidc.auth-server-url` | `quarkus.oidc.auth-server-url` |
| `memory-service.oidc.client-id` | `quarkus.oidc.client-id` |
| `memory-service.oidc.credentials.secret` | `quarkus.oidc.credentials.secret` |
| `memory-service.oidc.token.issuer` | `quarkus.oidc.token.issuer` |

Implementation: Extend the existing `ConfigSourceFactory` pattern used in `DatastoreConfigSourceFactory` and `BodySizeConfigSourceFactory`.

#### API Keys Naming

The current `memory-service.api-keys.<client-id>` and `memory-service.api-keys.port` keys are already well-named. No changes needed.

### Phase 3: Configuration Reference Page

Generate a comprehensive reference table for the site documentation:

| Key | Env Var | Type | Default | Description |
|-----|---------|------|---------|-------------|
| `memory-service.datastore.type` | `MEMORY_SERVICE_DATASTORE_TYPE` | String | `postgres` | Datastore backend: `postgres` or `mongodb` |
| `memory-service.datastore.migrate-at-start` | `MEMORY_SERVICE_DATASTORE_MIGRATE_AT_START` | Boolean | `true` | Run migrations on startup |
| `memory-service.cache.type` | `MEMORY_SERVICE_CACHE_TYPE` | String | `none` | Cache backend: `none`, `redis`, `infinispan` |
| ... | ... | ... | ... | ... |

This table should be generated from code annotations or maintained as a single source of truth in the site docs.

### Current Key Inventory (Complete)

The following keys are already correctly namespaced and need no changes:

**Datastore**: `memory-service.datastore.type`, `memory-service.datastore.migrate-at-start`
**Cache**: `memory-service.cache.type`, `memory-service.cache.epoch.ttl`, `memory-service.cache.redis.client`, `memory-service.cache.infinispan.*`
**Vector**: `memory-service.vector.type`
**Search**: `memory-service.search.semantic.enabled`, `memory-service.search.fulltext.enabled`
**Embedding**: `memory-service.embedding.enabled`
**Attachments**: `memory-service.attachments.*` (all sub-keys)
**Tasks**: `memory-service.tasks.*` (all sub-keys)
**Eviction**: `memory-service.eviction.*` (all sub-keys)
**CORS**: `memory-service.cors.*` (all sub-keys)
**Roles**: `memory-service.roles.*` (all sub-keys)
**Admin**: `memory-service.admin.require-justification`
**API Keys**: `memory-service.api-keys.*`
**Redis**: `memory-service.redis.hosts`
**gRPC**: `memory-service.grpc-advertised-address`
**Prometheus**: `memory-service.prometheus.url`
**Response Resumer**: `memory-service.response-resumer.enabled`, `memory-service.temp-dir`
**Client** (extensions): `memory-service.client.url`, `memory-service.client.api-key`, `memory-service.client.base-url`, `memory-service.client.log-requests`, `memory-service.client.temp-dir`

## Testing

### Unit Tests

Test the new `ConfigSourceFactory` mappings:

```java
class OidcConfigSourceFactoryTest {

    @Test
    void mapsMemoryServiceOidcToQuarkusOidc() {
        Map<String, String> input = Map.of(
                "memory-service.oidc.auth-server-url", "http://keycloak:8080/realms/test",
                "memory-service.oidc.client-id", "my-client");

        ConfigSource source = createConfigSource(input);

        assertEquals("http://keycloak:8080/realms/test",
                source.getValue("quarkus.oidc.auth-server-url"));
        assertEquals("my-client",
                source.getValue("quarkus.oidc.client-id"));
    }
}
```

```java
class EncryptionConfigMigrationTest {

    @Test
    void newPrefixIsRecognized() {
        // Verify @ConfigMapping reads from memory-service.encryption.*
        // Use SmallRyeConfig test utilities
    }
}
```

### Integration Tests

Existing Cucumber tests already exercise the configuration indirectly. The key risk is breaking existing deployments that use `data.encryption.*` — since the project is pre-release and doesn't need backward compatibility, this is acceptable.

### Verification

```bash
# Compile
./mvnw compile

# Run all tests to verify no configuration regressions
./mvnw test -pl memory-service > test.log 2>&1
# Search for failures using Grep tool on test.log
```

## Files to Modify

| File | Change |
|------|--------|
| `memory-service/.../config/DataEncryptionConfig.java` | Change `@ConfigMapping` prefix from `data.encryption` to `memory-service.encryption` |
| `memory-service/.../config/OidcConfigSourceFactory.java` | **New**: Maps `memory-service.oidc.*` → `quarkus.oidc.*` |
| `memory-service/src/main/resources/META-INF/services/io.smallrye.config.ConfigSourceFactory` | Register new factory |
| `memory-service/src/main/resources/application.properties` | Update `data.encryption.*` references to `memory-service.encryption.*` |
| `deploy/*/kustomization.yaml` | Update any `DATA_ENCRYPTION_*` env vars to `MEMORY_SERVICE_ENCRYPTION_*` |
| `site/src/pages/docs/configuration.mdx` | Add comprehensive configuration reference table |
| `memory-service/.../test/.../config/OidcConfigSourceFactoryTest.java` | **New**: Unit test |

## Design Decisions

1. **No backward compatibility shims**: Per CLAUDE.md, the project is pre-release. Old key names are simply replaced, not aliased.
2. **OIDC abstraction is optional**: Advanced users can still set `quarkus.oidc.*` directly if they need fine-grained control. The `memory-service.oidc.*` keys are convenience aliases at a lower ordinal.
3. **Encryption prefix change**: Moving from `data.encryption.*` to `memory-service.encryption.*` is a breaking change for deployments using encryption, but aligns with the project's single-prefix convention.
4. **Documentation as deliverable**: The configuration reference page is a first-class output of this enhancement, not an afterthought.
