---
status: implemented
supersedes:
  - 005-redis-connection-limits.md
  - 006-infinispan-support.md
---

# Simplify Cache Configuration

> **Status**: Implemented. Unifies cache configuration from
> [005](005-redis-connection-limits.md) and [006](006-infinispan-support.md).

## Motivation

Currently, the Memory Service has two separate configuration properties that both relate to distributed caching:

1. `memory-service.cache.type` - Intended for general caching, but **the property exists without any real implementation** - it always returns `NoopConversationCache` regardless of the value set
2. `memory-service.response-resumer` - Selects Redis or Infinispan specifically for the response resumer feature

This separation creates confusion:
- Users must configure two different properties for what is conceptually the same infrastructure
- The `memory-service.cache.type` property exists but has no real implementations
- The response resumer has its own backend selection logic that duplicates concepts

This document proposes unifying cache configuration so that all cache-dependent features (including the response resumer) use the single `memory-service.cache.type` setting.

## Current State

### Configuration Properties

```
memory-service.cache.type = none (default)
memory-service.response-resumer = none | redis | infinispan
memory-service.response-resumer.redis.client = <optional client name>
memory-service.response-resumer.infinispan.startup-timeout = PT30S
memory-service.temp-dir = <optional path>
memory-service.response-resumer.temp-file-retention = PT30M
```

### Current Classes

| Class | Purpose |
|-------|---------|
| `CacheSelector` | Selects cache implementation (only returns noop) |
| `ResponseResumerSelector` | Selects resumer backend based on `memory-service.response-resumer` |
| `ResponseResumerLocatorStoreSelector` | Selects Redis/Infinispan store based on `memory-service.response-resumer` |
| `RedisResponseResumerLocatorStore` | Redis implementation for resumer locator |
| `InfinispanResponseResumerLocatorStore` | Infinispan implementation for resumer locator |

## Proposed Design

### Unified Configuration

Replace the dual configuration with a single cache type selection:

```
memory-service.cache.type = none | redis | infinispan
```

When a cache backend is configured:
- The response resumer automatically uses it for locator storage
- Future caching features will also use the same backend
- No separate `memory-service.response-resumer` property needed

### New Configuration Properties

```
# Primary cache selection
memory-service.cache.type = none | redis | infinispan

# Response resumer settings (feature toggle + operational config)
memory-service.response-resumer.enabled = true | false (default: auto-detect based on cache.type)
memory-service.temp-dir = <optional path>
memory-service.response-resumer.temp-file-retention = PT30M

# Backend-specific settings (moved under cache.*)
memory-service.cache.redis.client = <optional client name>
memory-service.cache.infinispan.startup-timeout = PT30S

# Advertised address for multi-instance deployments
memory-service.grpc-advertised-address = host:port
```

### Behavior

| `cache.type` | `response-resumer.enabled` | Result |
|--------------|---------------------------|--------|
| `none` | `true` | Error: response resumer requires a cache backend |
| `none` | `false` (or unset) | Response resumer disabled |
| `redis` | `true` (or unset) | Response resumer enabled with Redis |
| `redis` | `false` | Response resumer disabled, Redis available for other caching |
| `infinispan` | `true` (or unset) | Response resumer enabled with Infinispan |
| `infinispan` | `false` | Response resumer disabled, Infinispan available for other caching |

## Scope of Changes

### 1. Configuration Properties

**File:** `memory-service/src/main/resources/application.properties`

```properties
# BEFORE
memory-service.cache.type=none
%dev.memory-service.response-resumer=redis
%test.memory-service.response-resumer=redis

# AFTER
memory-service.cache.type=none
%dev.memory-service.cache.type=redis
%test.memory-service.cache.type=redis
```

### 2. CacheSelector Refactoring

**File:** `memory-service/src/main/java/io/github/chirino/memory/config/CacheSelector.java`

Expand to actually select Redis/Infinispan implementations:

```java
@ApplicationScoped
public class CacheSelector {

    @ConfigProperty(name = "memory-service.cache.type", defaultValue = "none")
    String cacheType;

    @Inject NoopConversationCache noopConversationCache;
    @Inject RedisConversationCache redisConversationCache;      // new
    @Inject InfinispanConversationCache infinispanConversationCache;  // new

    public ConversationCache getCache() {
        return switch (cacheType.trim().toLowerCase()) {
            case "redis" -> redisConversationCache;
            case "infinispan" -> infinispanConversationCache;
            default -> noopConversationCache;
        };
    }

    public String getCacheType() {
        return cacheType;
    }
}
```

### 3. ResponseResumerLocatorStoreSelector Refactoring

**File:** `memory-service/src/main/java/io/github/chirino/memory/resumer/ResponseResumerLocatorStoreSelector.java`

Change to read from `memory-service.cache.type`:

```java
@ApplicationScoped
public class ResponseResumerLocatorStoreSelector {

    @ConfigProperty(name = "memory-service.cache.type", defaultValue = "none")
    String cacheType;

    @ConfigProperty(name = "memory-service.response-resumer.enabled")
    Optional<Boolean> resumerEnabled;

    @Inject RedisResponseResumerLocatorStore redisStore;
    @Inject InfinispanResponseResumerLocatorStore infinispanStore;
    @Inject NoopResponseResumerLocatorStore noopStore;

    public ResponseResumerLocatorStore select() {
        String type = cacheType.trim().toLowerCase();

        // If explicitly disabled, return noop
        if (resumerEnabled.isPresent() && !resumerEnabled.get()) {
            return noopStore;
        }

        return switch (type) {
            case "redis" -> requireAvailable(redisStore, "redis");
            case "infinispan" -> requireAvailable(infinispanStore, "infinispan");
            case "none" -> {
                // Error if explicitly enabled but no cache configured
                if (resumerEnabled.orElse(false)) {
                    throw new IllegalStateException(
                        "Response resumer is enabled but memory-service.cache.type=none. " +
                        "Configure a cache backend (redis or infinispan).");
                }
                yield noopStore;
            }
            default -> throw new IllegalStateException(
                "Unsupported memory-service.cache.type value: " + cacheType);
        };
    }

    // ... requireAvailable method unchanged
}
```

### 4. ResponseResumerSelector Refactoring

**File:** `memory-service/src/main/java/io/github/chirino/memory/resumer/ResponseResumerSelector.java`

```java
@ApplicationScoped
public class ResponseResumerSelector {

    @ConfigProperty(name = "memory-service.cache.type", defaultValue = "none")
    String cacheType;

    @ConfigProperty(name = "memory-service.response-resumer.enabled")
    Optional<Boolean> resumerEnabled;

    @Inject NoopResponseResumerBackend noopResponseResumerBackend;
    @Inject TempFileResumerBackend tempFileResumerBackend;

    @PostConstruct
    void validateConfiguration() {
        String type = cacheType.trim().toLowerCase();
        if (!"redis".equals(type) && !"infinispan".equals(type) && !"none".equals(type)) {
            throw new IllegalStateException(
                "Unsupported memory-service.cache.type value: " + cacheType);
        }
    }

    public ResponseResumerBackend getBackend() {
        String type = cacheType.trim().toLowerCase();

        // If explicitly disabled, return noop
        if (resumerEnabled.isPresent() && !resumerEnabled.get()) {
            return noopResponseResumerBackend;
        }

        if ("redis".equals(type) || "infinispan".equals(type)) {
            if (tempFileResumerBackend.enabled()) {
                return tempFileResumerBackend;
            }
        }
        return noopResponseResumerBackend;
    }
}
```

### 5. Redis Store Configuration Update

**File:** `memory-service/src/main/java/io/github/chirino/memory/resumer/RedisResponseResumerLocatorStore.java`

Update config property names:

```java
@Inject
public RedisResponseResumerLocatorStore(
        @ConfigProperty(name = "memory-service.cache.type") Optional<String> cacheType,
        @ConfigProperty(name = "memory-service.cache.redis.client") Optional<String> clientName,
        @Any Instance<ReactiveRedisDataSource> redisSources) {
    this.redisEnabled = cacheType.map("redis"::equalsIgnoreCase).orElse(false);
    // ... rest unchanged
}
```

### 6. Infinispan Store Configuration Update

**File:** `memory-service/src/main/java/io/github/chirino/memory/resumer/InfinispanResponseResumerLocatorStore.java`

Update config property names:

```java
@Inject
public InfinispanResponseResumerLocatorStore(
        @ConfigProperty(name = "memory-service.cache.type") Optional<String> cacheType,
        @ConfigProperty(name = "memory-service.cache.infinispan.startup-timeout", defaultValue = "PT30S")
                Duration startupTimeout,
        @Any Instance<RemoteCacheManager> cacheManagers) {
    this.infinispanEnabled = cacheType.map("infinispan"::equalsIgnoreCase).orElse(false);
    // ... rest unchanged
}
```

### 7. Test Profile Updates

**File:** `memory-service/src/test/java/io/github/chirino/memory/MongoRedisTestProfile.java`

```java
// BEFORE
configOverrides.put("memory-service.response-resumer", "redis");

// AFTER
configOverrides.put("memory-service.cache.type", "redis");
```

**File:** `memory-service/src/test/java/io/github/chirino/memory/PostgresqlInfinispanTestProfile.java`

```java
// BEFORE
configOverrides.put("memory-service.response-resumer", "infinispan");

// AFTER
configOverrides.put("memory-service.cache.type", "infinispan");
```

## Migration Path

### Backward Compatibility

To ease migration, support both old and new property names temporarily:

```java
@ConfigProperty(name = "memory-service.cache.type", defaultValue = "none")
String cacheType;

@ConfigProperty(name = "memory-service.response-resumer")
Optional<String> legacyResumerType;

String resolvedCacheType() {
    // Legacy property takes precedence if set and cache.type is default
    if (legacyResumerType.isPresent() && "none".equals(cacheType)) {
        LOG.warn("memory-service.response-resumer is deprecated. " +
                 "Use memory-service.cache.type instead.");
        return legacyResumerType.get();
    }
    return cacheType;
}
```

### Documentation Updates

Update `site/src/pages/docs/configuration.md`:
- Remove the separate "Response Resumer Configuration" section
- Document cache configuration with response resumer as one of its features
- Add migration notes for existing deployments

## Implementation Order

1. Add new `memory-service.cache.redis.client` and `memory-service.cache.infinispan.startup-timeout` properties
2. Update `CacheSelector` to expose cache type
3. Update `RedisResponseResumerLocatorStore` to read new property names
4. Update `InfinispanResponseResumerLocatorStore` to read new property names
5. Update `ResponseResumerLocatorStoreSelector` to read from `cache.type`
6. Update `ResponseResumerSelector` to read from `cache.type`
7. Add backward compatibility shim for `memory-service.response-resumer`
8. Update test profiles
9. Update `application.properties` defaults
10. Update documentation
11. Run full test suite

## Verification

```bash
# Compile all modules
./mvnw compile

# Run tests with Redis
./mvnw test -pl memory-service -Dtest.profile=MongoRedisTestProfile

# Run tests with Infinispan
./mvnw test -pl memory-service -Dtest.profile=PostgresqlInfinispanTestProfile

# Verify backward compatibility
MEMORY_SERVICE_RESPONSE_RESUMER=redis ./mvnw quarkus:dev -pl memory-service
```

## Future Considerations

Once `memory-service.cache.type` is established, additional cache-dependent features can leverage it:

- **ConversationCache implementations** - Actually implement Redis/Infinispan conversation caching
- **Session storage** - Store user sessions in the distributed cache
- **Rate limiting** - Use cache for distributed rate limit counters
- **Query result caching** - Cache expensive database queries

All these features would automatically use the configured cache backend without requiring additional configuration.
