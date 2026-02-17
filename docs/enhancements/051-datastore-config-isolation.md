---
status: implemented
---

# Enhancement 051: Datastore Configuration Isolation

> **Status**: Implemented.

## Summary

Automatically configure and isolate Quarkus subsystems based on `memory-service.datastore.type`, so that selecting a datastore (e.g., `mongo`) cleanly disables unused backends (JDBC, Hibernate ORM, PostgreSQL Liquibase) without manual configuration. Introduce `memory-service.datastore.migrate-at-start` as a unified migration property that routes to the correct Liquibase backend.

## Motivation

Memory Service supports both PostgreSQL and MongoDB as datastores. Previously, switching to MongoDB in production (e.g., via Kubernetes/kustomize) caused startup failures because:

1. **Eager bean injection**: Selector classes (e.g., `MemoryStoreSelector`, `AttachmentStoreSelector`) eagerly injected both the PostgreSQL and MongoDB implementation beans. When using MongoDB, the PostgreSQL beans still tried to resolve `EntityManager` and `DataSource`, which failed because no JDBC URL was configured.

2. **Hibernate ORM initialization**: Quarkus Hibernate ORM eagerly initializes persistence units at startup. Without a JDBC datasource URL, the auto-deactivated datasource caused a `ConfigurationException`.

3. **Liquibase confusion**: Users had to know the correct Quarkus-specific Liquibase property for their backend (`quarkus.liquibase.migrate-at-start` for PostgreSQL vs `quarkus.liquibase-mongodb.migrate-at-start` for MongoDB). Kustomize patches and documentation referenced these Quarkus internals directly.

These issues were masked in dev/test mode because Quarkus Dev Services automatically spun up a PostgreSQL container, providing a JDBC URL even when MongoDB was the selected datastore. The unused PostgreSQL instance kept Hibernate ORM happy but wasted resources.

## Design

### 1. Lazy Bean Injection via `Instance<>`

Changed all datastore selector classes to use CDI `Instance<T>` instead of direct `@Inject T`:

| Selector | Fields changed |
|----------|---------------|
| `MemoryStoreSelector` | `PostgresMemoryStore`, `MongoMemoryStore` |
| `AttachmentStoreSelector` | `PostgresAttachmentStore`, `MongoAttachmentStore` |
| `VectorStoreSelector` | `PgVectorStore`, `MongoVectorStore` |
| `TaskRepositorySelector` | `TaskRepository`, `MongoTaskRepository` |
| `DatabaseFileStore` | `DataSource` (JDBC) |

With `Instance<T>`, the bean is only resolved when `.get()` is called. Selectors only call `.get()` on the implementation matching the configured datastore type, so the unused backend's beans are never instantiated.

### 2. `DatastoreConfigSourceFactory`

A new SmallRye `ConfigSourceFactory` that reads `memory-service.*` properties and derives the appropriate Quarkus subsystem configuration:

**When `memory-service.datastore.type=mongo` or `mongodb` (production — dev services disabled):**
- Provides a dummy JDBC URL (`jdbc:postgresql://unused:5432/unused`) with a zero-size connection pool to keep Hibernate ORM and the datasource bean active without making actual PostgreSQL connections
- Sets `quarkus.liquibase.migrate-at-start=false` to skip PostgreSQL migrations
- Routes migrations to MongoDB Liquibase via `quarkus.liquibase-mongodb.migrate-at-start`

**When `memory-service.datastore.type=mongo` or `mongodb` (dev/test — dev services enabled):**
- Skips the dummy URL so Quarkus Dev Services can provision a real PostgreSQL instance normally
- Runs PostgreSQL Liquibase migrations so the schema exists (needed by test cleanup code)
- Routes migrations to MongoDB Liquibase via `quarkus.liquibase-mongodb.migrate-at-start`

**When `memory-service.datastore.type=postgres` (default):**
- Routes migrations to PostgreSQL Liquibase via `quarkus.liquibase.migrate-at-start`

**Why a dummy JDBC URL instead of `quarkus.hibernate-orm.active=false`:**
Setting `quarkus.hibernate-orm.active=false` at runtime deactivates Hibernate beans, but Quarkus registers *synthetic CDI lifecycle observers* for the Hibernate `Session` at build time. These observers fire during CDI lifecycle events regardless of the active flag, causing `InactiveBeanException`. The dummy URL keeps the beans active (avoiding the exception) while `Instance<>` lazy injection ensures no PostgreSQL code path is ever executed.

The factory uses ordinal 275 (higher than `application.properties` at 250, lower than system properties at 300 and environment variables at 400), so users can still override any derived property explicitly.

### 3. Test Profile Adjustments

- **`MongoRedisTestProfile`**: Disables unused Infinispan dev services. The factory detects that dev services are enabled and skips the dummy JDBC URL, allowing Dev Services to provision PostgreSQL normally.
- **`PostgresqlInfinispanTestProfile`**: Disables unused MongoDB and Redis dev services (`quarkus.mongodb.devservices.enabled=false`, `quarkus.redis.devservices.enabled=false`).

### 4. Kustomize Patch Updates

- PostgreSQL patch: `QUARKUS_LIQUIBASE_MIGRATE_AT_START` → `MEMORY_SERVICE_DATASTORE_MIGRATE_AT_START`
- MongoDB patch: no Quarkus subsystem env vars needed (the factory handles it)

## Files Changed

| File | Change |
|------|--------|
| `memory-service/.../config/DatastoreConfigSourceFactory.java` | **New** — derives Quarkus config from `memory-service.*` properties |
| `memory-service/.../config/MemoryStoreSelector.java` | `@Inject T` → `@Inject Instance<T>` |
| `memory-service/.../config/VectorStoreSelector.java` | `@Inject T` → `@Inject Instance<T>` |
| `memory-service/.../config/TaskRepositorySelector.java` | `@Inject T` → `@Inject Instance<T>` |
| `memory-service/.../attachment/AttachmentStoreSelector.java` | `@Inject T` → `@Inject Instance<T>` |
| `memory-service/.../attachment/DatabaseFileStore.java` | `@Inject DataSource` → `@Inject Instance<DataSource>` |
| `memory-service/.../resources/application.properties` | Replace Liquibase properties with `memory-service.datastore.migrate-at-start` |
| `memory-service/.../resources/META-INF/services/io.smallrye.config.ConfigSourceFactory` | Register `DatastoreConfigSourceFactory` |
| `memory-service/.../test/.../MongoRedisTestProfile.java` | Override factory settings, remove Liquibase overrides |
| `memory-service/.../test/.../PostgresqlInfinispanTestProfile.java` | Disable unused dev services, remove Liquibase overrides |
| `memory-service/.../test/.../config/TestInstance.java` | **New** — minimal `Instance<T>` for unit tests |
| `memory-service/.../test/.../config/VectorStoreSelectorTest.java` | Use `TestInstance` wrapper |
| `deploy/kustomize/components/db/postgresql/patch-memory-service.yaml` | Use `MEMORY_SERVICE_DATASTORE_MIGRATE_AT_START` |
| `site/src/pages/docs/configuration.mdx` | Document `memory-service.datastore.migrate-at-start`, auto-config behavior |

## Design Principle: `memory-service.*` Config Prefix

Going forward, all user-facing configuration should live under the `memory-service.*` prefix. Users should not need to set Quarkus-internal properties (e.g., `quarkus.datasource.*`, `quarkus.hibernate-orm.*`, `quarkus.liquibase.*`) for standard operational tasks. The `DatastoreConfigSourceFactory` pattern establishes the approach:

1. **Expose a `memory-service.*` property** with clear semantics (e.g., `memory-service.datastore.migrate-at-start`).
2. **Derive Quarkus properties** in a `ConfigSourceFactory` at ordinal 275, so the user's intent is automatically translated to the correct Quarkus settings.
3. **Allow explicit overrides** — since env vars (400) and system properties (300) have higher ordinal, advanced users can still set Quarkus properties directly when needed.

This gives users a stable, backend-agnostic configuration surface while keeping the flexibility of the underlying Quarkus configuration system.
