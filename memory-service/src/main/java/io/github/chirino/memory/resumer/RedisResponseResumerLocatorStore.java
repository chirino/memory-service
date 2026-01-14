package io.github.chirino.memory.resumer;

import io.quarkus.redis.client.RedisClientName;
import io.quarkus.redis.datasource.ReactiveRedisDataSource;
import io.quarkus.redis.datasource.keys.ReactiveKeyCommands;
import io.quarkus.redis.datasource.value.ReactiveValueCommands;
import jakarta.annotation.PostConstruct;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Any;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import java.time.Duration;
import java.util.Optional;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

@ApplicationScoped
public class RedisResponseResumerLocatorStore implements ResponseResumerLocatorStore {
    private static final Logger LOG = Logger.getLogger(RedisResponseResumerLocatorStore.class);
    private static final String RESPONSE_KEY_PREFIX = "response:";
    private static final Duration REDIS_TIMEOUT = Duration.ofSeconds(5);

    private final boolean redisEnabled;
    private final String clientName;
    private final Instance<ReactiveRedisDataSource> redisSources;
    private volatile ReactiveKeyCommands<String> keys;
    private volatile ReactiveValueCommands<String, String> values;

    @Inject
    public RedisResponseResumerLocatorStore(
            @ConfigProperty(name = "memory-service.response-resumer") Optional<String> resumerType,
            @ConfigProperty(name = "memory-service.response-resumer.redis.client")
                    Optional<String> clientName,
            @Any Instance<ReactiveRedisDataSource> redisSources) {
        this.redisEnabled = resumerType.map("redis"::equalsIgnoreCase).orElse(false);
        this.clientName = clientName.filter(it -> !it.isBlank()).orElse(null);
        this.redisSources = redisSources;
    }

    @PostConstruct
    void init() {
        if (!redisEnabled) {
            return;
        }

        Instance<ReactiveRedisDataSource> selected =
                clientName != null
                        ? redisSources.select(RedisClientName.Literal.of(clientName))
                        : redisSources;
        if (selected.isUnsatisfied()) {
            LOG.warnf(
                    "Response resumer is enabled (memory-service.response-resumer=redis) but Redis"
                            + " client '%s' is not available. Disabling response resumption.",
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
    public Optional<ResponseResumerLocator> get(String conversationId) {
        if (!available()) {
            return Optional.empty();
        }
        String value = values.get(key(conversationId)).await().atMost(REDIS_TIMEOUT);
        return ResponseResumerLocator.decode(value);
    }

    @Override
    public void upsert(String conversationId, ResponseResumerLocator locator, Duration ttl) {
        if (!available()) {
            return;
        }
        String key = key(conversationId);
        String value = locator.encode();
        values.set(key, value)
                .subscribe()
                .with(
                        ignored ->
                                keys.expire(key, ttl)
                                        .subscribe()
                                        .with(
                                                expire -> {},
                                                failure ->
                                                        LOG.warnf(
                                                                failure,
                                                                "Failed to refresh response locator"
                                                                        + " for %s",
                                                                key)),
                        failure ->
                                LOG.warnf(
                                        failure, "Failed to upsert response locator for %s", key));
    }

    @Override
    public void remove(String conversationId) {
        if (!available()) {
            return;
        }
        keys.del(key(conversationId))
                .subscribe()
                .with(
                        ignored -> {},
                        failure ->
                                LOG.warnf(
                                        failure,
                                        "Failed to delete response locator %s",
                                        key(conversationId)));
    }

    @Override
    public boolean exists(String conversationId) {
        if (!available()) {
            return false;
        }
        return keys.exists(key(conversationId)).await().atMost(REDIS_TIMEOUT) == Boolean.TRUE;
    }

    private String key(String conversationId) {
        return RESPONSE_KEY_PREFIX + conversationId;
    }
}
