package io.github.chirino.memory.prometheus;

/**
 * Exception thrown when Prometheus is configured but not reachable.
 *
 * <p>This results in a 503 Service Unavailable HTTP response.
 */
public class PrometheusUnavailableException extends RuntimeException {

    public PrometheusUnavailableException() {
        super("Could not connect to Prometheus server.");
    }

    public PrometheusUnavailableException(String message) {
        super(message);
    }

    public PrometheusUnavailableException(String message, Throwable cause) {
        super(message, cause);
    }
}
