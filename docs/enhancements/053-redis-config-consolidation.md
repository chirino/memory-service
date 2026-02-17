# Enhancement 053: Redis Configuration Consolidation

## Summary

Consolidate Redis configuration under the `memory-service.*` namespace so that all user-facing configuration uses `MEMORY_SERVICE_*` prefixed environment variables. This eliminates the need for users to set `QUARKUS_REDIS_HOSTS` or `QUARKUS_REDIS_HEALTH_ENABLED` directly, and automatically enables Redis health checks when `memory-service.cache.type=redis`.

## Motivation

Currently, deploying the memory service with Redis caching requires a mix of `MEMORY_SERVICE_*` and `QUARKUS_*` environment variables:

```yaml
# Current: mixed namespaces
MEMORY_SERVICE_CACHE_TYPE: redis          # memory-service namespace
QUARKUS_REDIS_HOSTS: redis://redis:6379   # quarkus namespace
QUARKUS_REDIS_HEALTH_ENABLED: true        # quarkus namespace (currently missing!)
```

Problems:
1. **Inconsistent naming**: Users must know which vars are `MEMORY_SERVICE_*` vs `QUARKUS_*`.
2. **Missing health check**: The kustomize redis component and compose.yaml never set `QUARKUS_REDIS_HEALTH_ENABLED=true`, so Redis health is silently disabled in production even when Redis is deployed.
3. **Leaky abstraction**: Users shouldn't need to know about Quarkus internals to configure the memory service.

## Design

### 1. New property: `memory-service.redis.hosts`

Add a `memory-service.redis.hosts` property and wire it to `quarkus.redis.hosts` via a Quarkus property expression:

```properties
# application.properties
memory-service.redis.hosts=redis://localhost:6379
quarkus.redis.hosts=${memory-service.redis.hosts}
```

Users set `MEMORY_SERVICE_REDIS_HOSTS=redis://redis:6379` instead of `QUARKUS_REDIS_HOSTS`.

### 2. Custom Redis health check

Always disable the built-in Quarkus Redis health check and replace it with a custom `@Readiness` health check that is **automatically conditional** on `memory-service.cache.type`:

- `memory-service.cache.type=none` or `infinispan` -> health check returns UP (skipped)
- `memory-service.cache.type=redis` -> health check pings Redis, returns UP/DOWN

```java
@Readiness
@ApplicationScoped
public class RedisHealthCheck implements HealthCheck {

    @ConfigProperty(name = "memory-service.cache.type", defaultValue = "none")
    String cacheType;

    @Any
    @Inject
    Instance<ReactiveRedisDataSource> redisSources;

    @ConfigProperty(name = "memory-service.cache.redis.client")
    Optional<String> clientName;

    private static final Duration TIMEOUT = Duration.ofSeconds(5);

    @Override
    public HealthCheckResponse call() {
        if (!"redis".equalsIgnoreCase(cacheType)) {
            return HealthCheckResponse.up("Redis");
        }

        try {
            Instance<ReactiveRedisDataSource> selected = clientName
                    .map(name -> redisSources.select(RedisClientName.Literal.of(name)))
                    .orElse(redisSources);

            if (selected.isUnsatisfied()) {
                return HealthCheckResponse.named("Redis")
                        .down()
                        .withData("reason", "No Redis client available")
                        .build();
            }

            ReactiveRedisDataSource ds = selected.get();
            Response response = ds.execute("PING").await().atMost(TIMEOUT);

            return HealthCheckResponse.named("Redis")
                    .up()
                    .withData("response", response.toString())
                    .build();
        } catch (Exception e) {
            return HealthCheckResponse.named("Redis")
                    .down()
                    .withData("error", e.getMessage())
                    .build();
        }
    }
}
```

### 3. Configuration changes

**application.properties** changes:

```properties
# Before:
%prod.quarkus.redis.hosts=redis://localhost:6379
%prod.quarkus.redis.health.enabled=false

# After:
memory-service.redis.hosts=redis://localhost:6379
quarkus.redis.hosts=${memory-service.redis.hosts}
quarkus.redis.health.enabled=false  # Always disabled; replaced by custom check
```

The `quarkus.redis.health.enabled=false` moves from `%prod` profile-specific to global since our custom health check handles all profiles.

### 4. Deployment config updates

**compose.yaml**:
```yaml
# Before:
QUARKUS_REDIS_HOSTS: redis://redis:6379

# After:
MEMORY_SERVICE_REDIS_HOSTS: redis://redis:6379
```

**deploy/kustomize/components/cache/redis/kustomization.yaml**:
```yaml
# Before:
- QUARKUS_REDIS_HOSTS=redis://redis:6379

# After:
- MEMORY_SERVICE_REDIS_HOSTS=redis://redis:6379
```

### 5. Not changing

- **Dev services config** (`quarkus.redis.devservices.enabled`): These are Quarkus-internal dev/test concerns, not user-facing production config. Leave as-is.
- **Quarkus extension dev services processor**: The `QUARKUS_REDIS_HOSTS` env var set on LLM dev service containers is for _those_ containers' Quarkus config, not the memory service. Leave as-is.

## Testing

### Unit test: `RedisHealthCheckTest`

Following the existing `VectorStoreSelectorTest` pattern (pure unit test, no `@QuarkusTest`), we can test the health check logic by constructing the bean manually and using `TestInstance` to mock the CDI `Instance<ReactiveRedisDataSource>`:

```java
class RedisHealthCheckTest {

    // --- cache type = none: should return UP (skipped) ---
    @Test
    void returnsUpWhenCacheTypeIsNone() {
        RedisHealthCheck check = createCheck("none", null);
        HealthCheckResponse response = check.call();
        assertEquals(HealthCheckResponse.Status.UP, response.getStatus());
    }

    // --- cache type = infinispan: should return UP (skipped) ---
    @Test
    void returnsUpWhenCacheTypeIsInfinispan() {
        RedisHealthCheck check = createCheck("infinispan", null);
        HealthCheckResponse response = check.call();
        assertEquals(HealthCheckResponse.Status.UP, response.getStatus());
    }

    // --- cache type = redis, no client available: should return DOWN ---
    @Test
    void returnsDownWhenRedisClientUnavailable() {
        RedisHealthCheck check = createCheck("redis", null);
        // redisSources is unsatisfied (empty TestInstance)
        check.redisSources = TestInstance.unsatisfied();
        HealthCheckResponse response = check.call();
        assertEquals(HealthCheckResponse.Status.DOWN, response.getStatus());
    }

    // --- cache type = redis, PING succeeds: should return UP ---
    @Test
    void returnsUpWhenRedisIsHealthy() {
        ReactiveRedisDataSource mockDs = mock(ReactiveRedisDataSource.class);
        when(mockDs.execute("PING")).thenReturn(
                Uni.createFrom().item(Response.of("PONG")));

        RedisHealthCheck check = createCheck("redis", mockDs);
        HealthCheckResponse response = check.call();
        assertEquals(HealthCheckResponse.Status.UP, response.getStatus());
    }

    // --- cache type = redis, PING throws: should return DOWN ---
    @Test
    void returnsDownWhenRedisPingFails() {
        ReactiveRedisDataSource mockDs = mock(ReactiveRedisDataSource.class);
        when(mockDs.execute("PING")).thenReturn(
                Uni.createFrom().failure(new RuntimeException("Connection refused")));

        RedisHealthCheck check = createCheck("redis", mockDs);
        HealthCheckResponse response = check.call();
        assertEquals(HealthCheckResponse.Status.DOWN, response.getStatus());
    }

    private RedisHealthCheck createCheck(String cacheType, ReactiveRedisDataSource ds) {
        RedisHealthCheck check = new RedisHealthCheck();
        check.cacheType = cacheType;
        check.clientName = Optional.empty();
        check.redisSources = ds != null
                ? TestInstance.of(ds)
                : TestInstance.unsatisfied();
        return check;
    }
}
```

Note: `TestInstance` already exists at `memory-service/src/test/java/.../config/TestInstance.java`. It may need a static `unsatisfied()` factory method added (currently only has `of(value)`).

### Integration validation

The existing Cucumber tests with `MongoRedisTestProfile` (which sets `memory-service.cache.type=redis`) will exercise the health check in a running Quarkus instance. No new integration tests are needed — the health check either activates correctly or fails the app startup/readiness.

## Files to Modify

| File | Change |
|------|--------|
| `memory-service/src/main/resources/application.properties` | Add `memory-service.redis.hosts`, wire to `quarkus.redis.hosts`, make health disabled globally |
| `memory-service/src/main/java/.../cache/RedisHealthCheck.java` | **New**: Custom `@Readiness` health check |
| `memory-service/src/test/java/.../cache/RedisHealthCheckTest.java` | **New**: Unit test |
| `memory-service/src/test/java/.../config/TestInstance.java` | Add `unsatisfied()` factory method |
| `compose.yaml` | `QUARKUS_REDIS_HOSTS` -> `MEMORY_SERVICE_REDIS_HOSTS` |
| `deploy/kustomize/components/cache/redis/kustomization.yaml` | `QUARKUS_REDIS_HOSTS` -> `MEMORY_SERVICE_REDIS_HOSTS` |
| `site/src/pages/docs/configuration.mdx` | Add `memory-service.redis.hosts` to Cache Configuration table; update Redis Backend example to use `memory-service.redis.hosts` instead of `quarkus.redis.hosts`; update Docker Compose example to use `MEMORY_SERVICE_REDIS_HOSTS` instead of `QUARKUS_REDIS_HOSTS` |
| `spring/examples/chat-spring/compose.yaml` | `QUARKUS_REDIS_HOSTS` -> `MEMORY_SERVICE_REDIS_HOSTS` |
| `site/src/pages/docs/spring/docker-compose.mdx` | `QUARKUS_REDIS_HOSTS` -> `MEMORY_SERVICE_REDIS_HOSTS` |
| `TODO.md` | Remove `fix QUARKUS_REDIS_HEALTH_ENABLED` item |

## Verification

```bash
# Compile
./mvnw compile

# Run unit tests
./mvnw test -pl memory-service -Dtest=RedisHealthCheckTest

# Run full test suite
./mvnw test -pl memory-service > test.log 2>&1

# Manual verification in dev mode
./mvnw quarkus:dev -pl memory-service
# Then: curl http://localhost:8080/q/health/ready
# Should show Redis health check status
```
