package io.github.chirino.memory.cache;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.when;

import io.github.chirino.memory.config.TestInstance;
import io.quarkus.redis.datasource.ReactiveRedisDataSource;
import io.smallrye.mutiny.Uni;
import io.vertx.mutiny.redis.client.Response;
import java.util.Optional;
import org.eclipse.microprofile.health.HealthCheckResponse;
import org.junit.jupiter.api.Test;

class RedisHealthCheckTest {

    @Test
    void returnsUpWhenCacheTypeIsNone() {
        RedisHealthCheck check = createCheck("none", null);
        HealthCheckResponse response = check.call();
        assertEquals(HealthCheckResponse.Status.UP, response.getStatus());
    }

    @Test
    void returnsUpWhenCacheTypeIsInfinispan() {
        RedisHealthCheck check = createCheck("infinispan", null);
        HealthCheckResponse response = check.call();
        assertEquals(HealthCheckResponse.Status.UP, response.getStatus());
    }

    @Test
    void returnsDownWhenRedisClientUnavailable() {
        RedisHealthCheck check = createCheck("redis", null);
        check.redisSources = TestInstance.unsatisfied();
        HealthCheckResponse response = check.call();
        assertEquals(HealthCheckResponse.Status.DOWN, response.getStatus());
    }

    @Test
    void returnsUpWhenRedisIsHealthy() {
        ReactiveRedisDataSource mockDs = mock(ReactiveRedisDataSource.class);
        Response mockResponse = mock(Response.class);
        when(mockResponse.toString()).thenReturn("PONG");
        when(mockDs.execute("PING")).thenReturn(Uni.createFrom().item(mockResponse));

        RedisHealthCheck check = createCheck("redis", mockDs);
        HealthCheckResponse response = check.call();
        assertEquals(HealthCheckResponse.Status.UP, response.getStatus());
    }

    @Test
    void returnsDownWhenRedisPingFails() {
        ReactiveRedisDataSource mockDs = mock(ReactiveRedisDataSource.class);
        when(mockDs.execute("PING"))
                .thenReturn(Uni.createFrom().failure(new RuntimeException("Connection refused")));

        RedisHealthCheck check = createCheck("redis", mockDs);
        HealthCheckResponse response = check.call();
        assertEquals(HealthCheckResponse.Status.DOWN, response.getStatus());
    }

    private RedisHealthCheck createCheck(String cacheType, ReactiveRedisDataSource ds) {
        RedisHealthCheck check = new RedisHealthCheck();
        check.cacheType = cacheType;
        check.clientName = Optional.empty();
        check.redisSources = ds != null ? TestInstance.of(ds) : TestInstance.unsatisfied();
        return check;
    }
}
