package io.github.chirino.memory.runtime;

import static org.assertj.core.api.Assertions.assertThat;

import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import uk.org.webcompere.systemstubs.environment.EnvironmentVariables;
import uk.org.webcompere.systemstubs.jupiter.SystemStub;
import uk.org.webcompere.systemstubs.jupiter.SystemStubsExtension;
import uk.org.webcompere.systemstubs.properties.SystemProperties;

@ExtendWith(SystemStubsExtension.class)
class GrpcFromUrlConfigSourceTest {

    private static final String GRPC_HOST = "quarkus.grpc.clients.responseresumer.host";
    private static final String GRPC_PORT = "quarkus.grpc.clients.responseresumer.port";
    private static final String GRPC_PLAIN_TEXT = "quarkus.grpc.clients.responseresumer.plain-text";

    @SystemStub private EnvironmentVariables environmentVariables;

    @SystemStub private SystemProperties systemProperties;

    @AfterEach
    void cleanup() {
        // Ensure clean state between tests
        systemProperties.remove("memory-service-client.url");
    }

    @Test
    void shouldDeriveHostFromHttpUrl() {
        environmentVariables.set("MEMORY_SERVICE_CLIENT_URL", "http://memory-service:8080");

        GrpcFromUrlConfigSource source = new GrpcFromUrlConfigSource();

        assertThat(source.getValue(GRPC_HOST)).isEqualTo("memory-service");
    }

    @Test
    void shouldDerivePortFromUrl() {
        environmentVariables.set("MEMORY_SERVICE_CLIENT_URL", "http://memory-service:9090");

        GrpcFromUrlConfigSource source = new GrpcFromUrlConfigSource();

        assertThat(source.getValue(GRPC_PORT)).isEqualTo("9090");
    }

    @Test
    void shouldDefaultPortTo80ForHttp() {
        environmentVariables.set("MEMORY_SERVICE_CLIENT_URL", "http://memory-service");

        GrpcFromUrlConfigSource source = new GrpcFromUrlConfigSource();

        assertThat(source.getValue(GRPC_PORT)).isEqualTo("80");
    }

    @Test
    void shouldDefaultPortTo443ForHttps() {
        environmentVariables.set("MEMORY_SERVICE_CLIENT_URL", "https://memory-service");

        GrpcFromUrlConfigSource source = new GrpcFromUrlConfigSource();

        assertThat(source.getValue(GRPC_PORT)).isEqualTo("443");
    }

    @Test
    void shouldSetPlainTextTrueForHttp() {
        environmentVariables.set("MEMORY_SERVICE_CLIENT_URL", "http://memory-service:8080");

        GrpcFromUrlConfigSource source = new GrpcFromUrlConfigSource();

        assertThat(source.getValue(GRPC_PLAIN_TEXT)).isEqualTo("true");
    }

    @Test
    void shouldSetPlainTextFalseForHttps() {
        environmentVariables.set("MEMORY_SERVICE_CLIENT_URL", "https://memory-service:443");

        GrpcFromUrlConfigSource source = new GrpcFromUrlConfigSource();

        assertThat(source.getValue(GRPC_PLAIN_TEXT)).isEqualTo("false");
    }

    @Test
    void shouldReturnNullWhenNoUrlConfigured() {
        // No URL configured
        GrpcFromUrlConfigSource source = new GrpcFromUrlConfigSource();

        assertThat(source.getValue(GRPC_HOST)).isNull();
        assertThat(source.getValue(GRPC_PORT)).isNull();
        assertThat(source.getValue(GRPC_PLAIN_TEXT)).isNull();
    }

    @Test
    void shouldPreferEnvVarOverSystemProperty() {
        environmentVariables.set("MEMORY_SERVICE_CLIENT_URL", "http://from-env:8080");
        systemProperties.set("memory-service-client.url", "http://from-sysprop:9090");

        GrpcFromUrlConfigSource source = new GrpcFromUrlConfigSource();

        // Env var should win
        assertThat(source.getValue(GRPC_HOST)).isEqualTo("from-env");
        assertThat(source.getValue(GRPC_PORT)).isEqualTo("8080");
    }

    @Test
    void shouldFallbackToSystemProperty() {
        // Only system property is set (no env var)
        systemProperties.set("memory-service-client.url", "http://from-sysprop:9090");

        GrpcFromUrlConfigSource source = new GrpcFromUrlConfigSource();

        assertThat(source.getValue(GRPC_HOST)).isEqualTo("from-sysprop");
        assertThat(source.getValue(GRPC_PORT)).isEqualTo("9090");
    }

    @Test
    void shouldReturnNullForUnrelatedProperties() {
        environmentVariables.set("MEMORY_SERVICE_CLIENT_URL", "http://memory-service:8080");

        GrpcFromUrlConfigSource source = new GrpcFromUrlConfigSource();

        assertThat(source.getValue("some.other.property")).isNull();
        assertThat(source.getValue("quarkus.grpc.clients.other.host")).isNull();
    }

    @Test
    void shouldReturnCorrectPropertyNames() {
        environmentVariables.set("MEMORY_SERVICE_CLIENT_URL", "http://memory-service:8080");

        GrpcFromUrlConfigSource source = new GrpcFromUrlConfigSource();

        assertThat(source.getPropertyNames())
                .containsExactlyInAnyOrder(GRPC_HOST, GRPC_PORT, GRPC_PLAIN_TEXT);
    }

    @Test
    void shouldReturnEmptyPropertyNamesWhenNoUrlConfigured() {
        GrpcFromUrlConfigSource source = new GrpcFromUrlConfigSource();

        assertThat(source.getPropertyNames()).isEmpty();
    }

    @Test
    void shouldReturnAllPropertiesViaGetProperties() {
        environmentVariables.set("MEMORY_SERVICE_CLIENT_URL", "http://test-host:7070");

        GrpcFromUrlConfigSource source = new GrpcFromUrlConfigSource();

        assertThat(source.getProperties())
                .containsEntry(GRPC_HOST, "test-host")
                .containsEntry(GRPC_PORT, "7070")
                .containsEntry(GRPC_PLAIN_TEXT, "true");
    }

    @Test
    void shouldHaveCorrectOrdinal() {
        GrpcFromUrlConfigSource source = new GrpcFromUrlConfigSource();

        // Should be 150 - higher than application.properties (100) but lower than env vars
        // (300)
        assertThat(source.getOrdinal()).isEqualTo(150);
    }

    @Test
    void shouldHaveCorrectName() {
        GrpcFromUrlConfigSource source = new GrpcFromUrlConfigSource();

        assertThat(source.getName()).isEqualTo("memory-service-grpc-url-derived");
    }

    @Test
    void shouldHandleInvalidUrl() {
        environmentVariables.set("MEMORY_SERVICE_CLIENT_URL", "not-a-valid-url");

        GrpcFromUrlConfigSource source = new GrpcFromUrlConfigSource();

        // Should return null for invalid URL, letting other sources provide defaults
        assertThat(source.getValue(GRPC_HOST)).isNull();
    }

    @Test
    void shouldHandleEmptyUrl() {
        environmentVariables.set("MEMORY_SERVICE_CLIENT_URL", "");

        GrpcFromUrlConfigSource source = new GrpcFromUrlConfigSource();

        assertThat(source.getValue(GRPC_HOST)).isNull();
    }

    @Test
    void shouldHandleBlankUrl() {
        environmentVariables.set("MEMORY_SERVICE_CLIENT_URL", "   ");

        GrpcFromUrlConfigSource source = new GrpcFromUrlConfigSource();

        assertThat(source.getValue(GRPC_HOST)).isNull();
    }

    @Test
    void shouldCacheResultsForPerformance() {
        environmentVariables.set("MEMORY_SERVICE_CLIENT_URL", "http://cached-host:8080");

        GrpcFromUrlConfigSource source = new GrpcFromUrlConfigSource();

        // Call multiple times
        String host1 = source.getValue(GRPC_HOST);
        String host2 = source.getValue(GRPC_HOST);
        String port1 = source.getValue(GRPC_PORT);

        assertThat(host1).isEqualTo("cached-host");
        assertThat(host2).isEqualTo("cached-host");
        assertThat(port1).isEqualTo("8080");
    }
}
