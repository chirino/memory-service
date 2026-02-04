package io.github.chirino.memory.config;

import io.micrometer.core.instrument.Meter;
import io.micrometer.core.instrument.Tag;
import io.micrometer.core.instrument.config.MeterFilter;
import io.micrometer.core.instrument.distribution.DistributionStatisticConfig;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Produces;
import jakarta.inject.Singleton;
import java.util.List;

/**
 * Micrometer configuration for memory-service metrics.
 *
 * <p>Configures meters used by the admin stats endpoints:
 *
 * <ul>
 *   <li>http_server_requests_seconds_* - HTTP request metrics
 *   <li>memory_store_operation_seconds_* - Store operation timing
 *   <li>memory_entries_cache_* - Cache hit/miss counters
 *   <li>agroal_* - Database connection pool metrics
 * </ul>
 */
@ApplicationScoped
public class MetricsConfig {

    /**
     * Adds an 'application' tag to all metrics for identification when multiple services are
     * scraped by the same Prometheus instance.
     */
    @Produces
    @Singleton
    public MeterFilter applicationTagFilter() {
        return MeterFilter.commonTags(List.of(Tag.of("application", "memory-service")));
    }

    /**
     * Enables histogram buckets for HTTP and store operation metrics to support percentile
     * calculations in Prometheus using histogram_quantile().
     *
     * @see <a href="https://quarkus.io/guides/telemetry-micrometer">Quarkus Micrometer Guide</a>
     */
    @Produces
    @Singleton
    public MeterFilter histogramFilter() {
        return new MeterFilter() {
            @Override
            public DistributionStatisticConfig configure(
                    Meter.Id id, DistributionStatisticConfig config) {
                if (id.getName().startsWith("http.server.requests")
                        || id.getName().startsWith("memory.store.operation")) {
                    return DistributionStatisticConfig.builder()
                            .percentiles(0.95, 0.99)
                            .percentilesHistogram(true)
                            .build()
                            .merge(config);
                }
                return config;
            }
        };
    }
}
