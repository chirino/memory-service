---
status: proposed
---

# Enhancement 054: Configurable Infinispan Cache Names

> **Status**: Proposed.

## Summary

Make Infinispan cache names configurable via `memory-service.cache.infinispan.*` properties, following the project convention that all user-facing configuration uses `MEMORY_SERVICE_*` prefixed environment variables.

## Motivation

Currently, the Infinispan cache names are hardcoded as `static final String` constants:

- `"memory-entries"` in `InfinispanMemoryEntriesCache` (line 28)
- `"response-recordings"` in `InfinispanResponseResumerLocatorStore` (line 24)

This is problematic when:
1. **Multiple instances share an Infinispan cluster**: Different environments (staging, production) or different tenants cannot use distinct cache names to avoid collisions.
2. **Naming conventions**: Organizations may have naming standards for cache names (e.g., prefixed with service name or environment).
3. **Configuration consistency**: Other Infinispan settings like `startup-timeout` are already configurable via `memory-service.cache.infinispan.*`, but the cache names are not.

## Design

### New Properties

| Property | Env Var | Default | Description |
|----------|---------|---------|-------------|
| `memory-service.cache.infinispan.memory-entries-cache-name` | `MEMORY_SERVICE_CACHE_INFINISPAN_MEMORY_ENTRIES_CACHE_NAME` | `memory-entries` | Cache name for memory entries |
| `memory-service.cache.infinispan.response-recordings-cache-name` | `MEMORY_SERVICE_CACHE_INFINISPAN_RESPONSE_RECORDINGS_CACHE_NAME` | `response-recordings` | Cache name for response resumer locator |

These follow the existing pattern where `memory-service.cache.infinispan.startup-timeout` is already configurable.

### Code Changes

#### 1. `InfinispanMemoryEntriesCache`

Replace the hardcoded constant with a `@ConfigProperty`:

```java
// BEFORE
private static final String CACHE_NAME = "memory-entries";

// AFTER
private final String cacheName;

@Inject
public InfinispanMemoryEntriesCache(
        @ConfigProperty(name = "memory-service.cache.type") Optional<String> cacheType,
        @ConfigProperty(name = "memory-service.cache.infinispan.startup-timeout", defaultValue = "PT30S")
                Duration startupTimeout,
        @ConfigProperty(name = "memory-service.cache.infinispan.memory-entries-cache-name", defaultValue = "memory-entries")
                String cacheName,
        @ConfigProperty(name = "memory-service.cache.epoch.ttl", defaultValue = "PT10M")
                Duration ttl,
        @Any Instance<RemoteCacheManager> cacheManagers,
        ObjectMapper objectMapper,
        MeterRegistry meterRegistry) {
    this.cacheName = cacheName;
    // ...
}
```

The `CACHE_CONFIG` must also become dynamic since it embeds the cache name in the XML:

```java
private XMLStringConfiguration buildCacheConfig(String name) {
    return new XMLStringConfiguration(
            "<distributed-cache name=\"" + name + "\">"
                    + "<encoding media-type=\"text/plain\"/>"
                    + "</distributed-cache>");
}
```

Then in `init()`:

```java
cache = cacheManager.administration()
        .getOrCreateCache(cacheName, buildCacheConfig(cacheName));
```

#### 2. `InfinispanResponseResumerLocatorStore`

Same pattern:

```java
// BEFORE
private static final String CACHE_NAME = "response-recordings";

// AFTER
private final String cacheName;

@Inject
public InfinispanResponseResumerLocatorStore(
        @ConfigProperty(name = "memory-service.cache.type") Optional<String> cacheType,
        @ConfigProperty(name = "memory-service.cache.infinispan.startup-timeout", defaultValue = "PT30S")
                Duration startupTimeout,
        @ConfigProperty(name = "memory-service.cache.infinispan.response-recordings-cache-name", defaultValue = "response-recordings")
                String cacheName,
        @Any Instance<RemoteCacheManager> cacheManagers) {
    this.cacheName = cacheName;
    // ...
}
```

#### 3. Deployment Config

The kustomize Infinispan component already configures cache XML via `QUARKUS_INFINISPAN_CLIENT_CACHE__MEMORY_ENTRIES__CONFIGURATION`. If the cache name is changed, the corresponding Quarkus cache configuration property must also use the new name in the double-underscore escaped form. Document this in the deployment guide.

## Testing

### Unit Tests

Following the existing `VectorStoreSelectorTest` pattern (pure unit test, no `@QuarkusTest`), we can test configuration wiring by constructing the beans manually.

The key testable behaviors are:
1. The cache name defaults are correct when no override is provided.
2. Custom cache names are propagated to cache creation calls.
3. The XML configuration uses the configured cache name.

```java
class InfinispanMemoryEntriesCacheConfigTest {

    @Test
    void defaultCacheNameIsUsed() {
        // Construct with default cache name
        InfinispanMemoryEntriesCache cache = new InfinispanMemoryEntriesCache(
                Optional.of("infinispan"),
                Duration.ofSeconds(30),
                "memory-entries",       // default
                Duration.ofMinutes(10),
                TestInstance.unsatisfied(),  // no RemoteCacheManager needed for config test
                new ObjectMapper(),
                new SimpleMeterRegistry());

        assertEquals("memory-entries", cache.getCacheName());
    }

    @Test
    void customCacheNameIsUsed() {
        InfinispanMemoryEntriesCache cache = new InfinispanMemoryEntriesCache(
                Optional.of("infinispan"),
                Duration.ofSeconds(30),
                "prod-memory-entries",  // custom
                Duration.ofMinutes(10),
                TestInstance.unsatisfied(),
                new ObjectMapper(),
                new SimpleMeterRegistry());

        assertEquals("prod-memory-entries", cache.getCacheName());
    }

    @Test
    void cacheConfigXmlContainsCacheName() {
        String name = "my-custom-cache";
        XMLStringConfiguration config = InfinispanMemoryEntriesCache.buildCacheConfig(name);
        assertTrue(config.toXMLString().contains("name=\"my-custom-cache\""));
    }
}
```

```java
class InfinispanResponseResumerLocatorStoreConfigTest {

    @Test
    void defaultCacheNameIsUsed() {
        InfinispanResponseResumerLocatorStore store = new InfinispanResponseResumerLocatorStore(
                Optional.of("infinispan"),
                Duration.ofSeconds(30),
                "response-recordings",  // default
                TestInstance.unsatisfied());

        assertEquals("response-recordings", store.getCacheName());
    }

    @Test
    void customCacheNameIsUsed() {
        InfinispanResponseResumerLocatorStore store = new InfinispanResponseResumerLocatorStore(
                Optional.of("infinispan"),
                Duration.ofSeconds(30),
                "staging-response-recordings",  // custom
                TestInstance.unsatisfied());

        assertEquals("staging-response-recordings", store.getCacheName());
    }
}
```

**Note**: These tests require adding a `getCacheName()` accessor method to each class (package-private is fine). Alternatively, we can make `buildCacheConfig()` static/package-private and test just the XML generation without needing the accessor.

### Why Unit Tests Work Here

- The configuration wiring (`@ConfigProperty` injection) is standard MicroProfile Config — Quarkus resolves it at startup, but we can verify the constructor stores the value correctly.
- The `buildCacheConfig()` XML generation is pure logic with no external dependencies.
- Actual Infinispan interaction (creating/getting caches) is already covered by existing integration tests (`PostgresqlInfinispanCucumberTest`).
- The `TestInstance` utility ([TestInstance.java](memory-service/src/test/java/io/github/chirino/memory/config/TestInstance.java)) already exists for mocking CDI `Instance<T>` in unit tests.

### Integration Test Coverage

The existing `PostgresqlInfinispanCucumberTest` and `PostgresqlInfinispanS3CucumberTest` profiles already exercise the Infinispan cache path with default cache names. To test custom names in integration:

Add an optional test profile override in `PostgresqlInfinispanTestProfile`:

```java
// Optionally test with custom cache names
configOverrides.put(
        "memory-service.cache.infinispan.memory-entries-cache-name",
        "test-memory-entries");
configOverrides.put(
        "memory-service.cache.infinispan.response-recordings-cache-name",
        "test-response-recordings");
```

This validates end-to-end that custom names work with a real Infinispan instance via dev services.

## Files to Modify

| File | Change |
|------|--------|
| `memory-service/.../cache/InfinispanMemoryEntriesCache.java` | Replace `CACHE_NAME` constant with `@ConfigProperty`-injected field; extract `buildCacheConfig()` |
| `memory-service/.../resumer/InfinispanResponseResumerLocatorStore.java` | Replace `CACHE_NAME` constant with `@ConfigProperty`-injected field; extract `buildCacheConfig()` |
| `memory-service/.../test/.../cache/InfinispanMemoryEntriesCacheConfigTest.java` | **New**: Unit test for config wiring |
| `memory-service/.../test/.../resumer/InfinispanResponseResumerLocatorStoreConfigTest.java` | **New**: Unit test for config wiring |
| `site/src/pages/docs/configuration.mdx` | Add `memory-service.cache.infinispan.memory-entries-cache-name` and `memory-service.cache.infinispan.response-recordings-cache-name` to the Cache Configuration table; update Infinispan Backend example to show the new cache name properties |

## Verification

```bash
# Compile
./mvnw compile

# Run unit tests
./mvnw test -pl memory-service -Dtest=InfinispanMemoryEntriesCacheConfigTest
./mvnw test -pl memory-service -Dtest=InfinispanResponseResumerLocatorStoreConfigTest

# Run full Infinispan integration tests
./mvnw test -pl memory-service > test.log 2>&1
# Then grep for failures
```
