package io.github.chirino.memory.cache;

import io.quarkus.redis.client.RedisClientName;
import io.quarkus.redis.datasource.ReactiveRedisDataSource;
import io.vertx.mutiny.redis.client.Response;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Any;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import java.time.Duration;
import java.util.Optional;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.eclipse.microprofile.health.HealthCheck;
import org.eclipse.microprofile.health.HealthCheckResponse;
import org.eclipse.microprofile.health.Readiness;

@Readiness
@ApplicationScoped
public class RedisHealthCheck implements HealthCheck {

    private static final Duration TIMEOUT = Duration.ofSeconds(5);

    @ConfigProperty(name = "memory-service.cache.type", defaultValue = "none")
    String cacheType;

    @Any @Inject Instance<ReactiveRedisDataSource> redisSources;

    @ConfigProperty(name = "memory-service.cache.redis.client")
    Optional<String> clientName;

    @Override
    public HealthCheckResponse call() {
        if (!"redis".equalsIgnoreCase(cacheType)) {
            return HealthCheckResponse.up("Redis");
        }

        try {
            Instance<ReactiveRedisDataSource> selected =
                    clientName
                            .filter(name -> !name.isBlank())
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
