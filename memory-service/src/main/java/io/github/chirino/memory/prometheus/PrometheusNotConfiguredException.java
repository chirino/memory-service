package io.github.chirino.memory.prometheus;

/**
 * Exception thrown when admin stats endpoints are called but Prometheus is not configured.
 *
 * <p>This results in a 501 Not Implemented HTTP response.
 */
public class PrometheusNotConfiguredException extends RuntimeException {

    public PrometheusNotConfiguredException() {
        super(
                "Prometheus is not configured. Set memory-service.prometheus.url to enable admin"
                        + " stats.");
    }

    public PrometheusNotConfiguredException(String message) {
        super(message);
    }
}
