package io.github.chirino.memory.cache;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import io.micrometer.core.instrument.Counter;
import io.micrometer.core.instrument.MeterRegistry;
import io.quarkus.redis.client.RedisClientName;
import io.quarkus.redis.datasource.ReactiveRedisDataSource;
import io.quarkus.redis.datasource.keys.ReactiveKeyCommands;
import io.quarkus.redis.datasource.value.GetExArgs;
import io.quarkus.redis.datasource.value.ReactiveValueCommands;
import io.quarkus.redis.datasource.value.SetArgs;
import jakarta.annotation.PostConstruct;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Any;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import java.time.Duration;
import java.util.Optional;
import java.util.UUID;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

@ApplicationScoped
public class RedisMemoryEntriesCache implements MemoryEntriesCache {
    private static final Logger LOG = Logger.getLogger(RedisMemoryEntriesCache.class);
    private static final String KEY_PREFIX = "memory:entries:";
    private static final Duration REDIS_TIMEOUT = Duration.ofSeconds(5);

    private final boolean redisEnabled;
    private final String clientName;
    private final Duration ttl;
    private final Instance<ReactiveRedisDataSource> redisSources;
    private final ObjectMapper objectMapper;
    private final MeterRegistry meterRegistry;
    private volatile ReactiveKeyCommands<String> keys;
    private volatile ReactiveValueCommands<String, String> values;
    private Counter cacheHits;
    private Counter cacheMisses;
    private Counter cacheErrors;

    @Inject
    public RedisMemoryEntriesCache(
            @ConfigProperty(name = "memory-service.cache.type") Optional<String> cacheType,
            @ConfigProperty(name = "memory-service.cache.redis.client") Optional<String> clientName,
            @ConfigProperty(name = "memory-service.cache.epoch.ttl", defaultValue = "PT10M")
                    Duration ttl,
            @Any Instance<ReactiveRedisDataSource> redisSources,
            ObjectMapper objectMapper,
            MeterRegistry meterRegistry) {
        this.redisEnabled = cacheType.map("redis"::equalsIgnoreCase).orElse(false);
        this.clientName = clientName.filter(it -> !it.isBlank()).orElse(null);
        this.ttl = ttl;
        this.redisSources = redisSources;
        this.objectMapper = objectMapper;
        this.meterRegistry = meterRegistry;
    }

    @PostConstruct
    void init() {
        // Initialize metrics
        cacheHits = meterRegistry.counter("memory.entries.cache.hits", "backend", "redis");
        cacheMisses = meterRegistry.counter("memory.entries.cache.misses", "backend", "redis");
        cacheErrors = meterRegistry.counter("memory.entries.cache.errors", "backend", "redis");

        if (!redisEnabled) {
            return;
        }

        Instance<ReactiveRedisDataSource> selected =
                clientName != null
                        ? redisSources.select(RedisClientName.Literal.of(clientName))
                        : redisSources;
        if (selected.isUnsatisfied()) {
            LOG.warnf(
                    "Memory entries cache is enabled (memory-service.cache.type=redis) but Redis"
                            + " client '%s' is not available.",
                    clientName == null ? "<default>" : clientName);
            return;
        }

        ReactiveRedisDataSource dataSource = selected.get();
        keys = dataSource.key();
        values = dataSource.value(String.class);
    }

    @Override
    public boolean available() {
        return keys != null && values != null;
    }

    @Override
    public Optional<CachedMemoryEntries> get(UUID conversationId, String clientId) {
        if (!available()) {
            cacheMisses.increment();
            return Optional.empty();
        }
        try {
            String key = buildKey(conversationId, clientId);
            // GETEX with EX option atomically gets and refreshes TTL
            String json = values.getex(key, new GetExArgs().ex(ttl)).await().atMost(REDIS_TIMEOUT);
            if (json == null) {
                cacheMisses.increment();
                return Optional.empty();
            }
            cacheHits.increment();
            return Optional.of(objectMapper.readValue(json, CachedMemoryEntries.class));
        } catch (Exception e) {
            LOG.warnf(
                    e,
                    "Failed to get memory entries from Redis cache for %s/%s",
                    conversationId,
                    clientId);
            cacheErrors.increment();
            return Optional.empty();
        }
    }

    @Override
    public void set(UUID conversationId, String clientId, CachedMemoryEntries entries) {
        if (!available()) {
            return;
        }
        try {
            String key = buildKey(conversationId, clientId);
            String json = objectMapper.writeValueAsString(entries);
            // Set with TTL
            values.set(key, json, new SetArgs().ex(ttl))
                    .subscribe()
                    .with(
                            ignored -> {},
                            failure ->
                                    LOG.warnf(
                                            failure,
                                            "Failed to set memory entries in Redis cache for %s/%s",
                                            conversationId,
                                            clientId));
        } catch (JsonProcessingException e) {
            LOG.warnf(e, "Failed to serialize memory entries for Redis cache");
        }
    }

    @Override
    public void remove(UUID conversationId, String clientId) {
        if (!available()) {
            return;
        }
        String key = buildKey(conversationId, clientId);
        keys.del(key)
                .subscribe()
                .with(
                        ignored -> {},
                        failure ->
                                LOG.warnf(
                                        failure,
                                        "Failed to remove memory entries from Redis cache for"
                                                + " %s/%s",
                                        conversationId,
                                        clientId));
    }

    private String buildKey(UUID conversationId, String clientId) {
        return KEY_PREFIX + conversationId + ":" + clientId;
    }
}
