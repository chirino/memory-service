package io.github.chirino.memory.runtime;

import static org.assertj.core.api.Assertions.assertThat;

import java.util.Map;
import org.junit.jupiter.api.Test;

/**
 * Tests for {@link GrpcFromUrlConfigSource} and its URL parsing logic.
 */
class GrpcFromUrlConfigSourceTest {

    private static final String GRPC_HOST = "quarkus.grpc.clients.responserecorder.host";
    private static final String GRPC_PORT = "quarkus.grpc.clients.responserecorder.port";
    private static final String GRPC_PLAIN_TEXT =
            "quarkus.grpc.clients.responserecorder.plain-text";

    private GrpcFromUrlConfigSource createSource(String url) {
        Map<String, String> props = GrpcFromUrlConfigSource.parseUrl(url);
        return new GrpcFromUrlConfigSource(props);
    }

    @Test
    void shouldDeriveHostFromHttpUrl() {
        GrpcFromUrlConfigSource source = createSource("http://memory-service:8080");

        assertThat(source.getValue(GRPC_HOST)).isEqualTo("memory-service");
    }

    @Test
    void shouldDerivePortFromUrl() {
        GrpcFromUrlConfigSource source = createSource("http://memory-service:9090");

        assertThat(source.getValue(GRPC_PORT)).isEqualTo("9090");
    }

    @Test
    void shouldDefaultPortTo80ForHttp() {
        GrpcFromUrlConfigSource source = createSource("http://memory-service");

        assertThat(source.getValue(GRPC_PORT)).isEqualTo("80");
    }

    @Test
    void shouldDefaultPortTo443ForHttps() {
        GrpcFromUrlConfigSource source = createSource("https://memory-service");

        assertThat(source.getValue(GRPC_PORT)).isEqualTo("443");
    }

    @Test
    void shouldSetPlainTextTrueForHttp() {
        GrpcFromUrlConfigSource source = createSource("http://memory-service:8080");

        assertThat(source.getValue(GRPC_PLAIN_TEXT)).isEqualTo("true");
    }

    @Test
    void shouldSetPlainTextFalseForHttps() {
        GrpcFromUrlConfigSource source = createSource("https://memory-service:443");

        assertThat(source.getValue(GRPC_PLAIN_TEXT)).isEqualTo("false");
    }

    @Test
    void shouldReturnNullWhenNoUrlConfigured() {
        GrpcFromUrlConfigSource source = createSource(null);

        assertThat(source.getValue(GRPC_HOST)).isNull();
        assertThat(source.getValue(GRPC_PORT)).isNull();
        assertThat(source.getValue(GRPC_PLAIN_TEXT)).isNull();
    }

    @Test
    void shouldReturnNullForUnrelatedProperties() {
        GrpcFromUrlConfigSource source = createSource("http://memory-service:8080");

        assertThat(source.getValue("some.other.property")).isNull();
        assertThat(source.getValue("quarkus.grpc.clients.other.host")).isNull();
    }

    @Test
    void shouldReturnCorrectPropertyNames() {
        GrpcFromUrlConfigSource source = createSource("http://memory-service:8080");

        assertThat(source.getPropertyNames())
                .containsExactlyInAnyOrder(GRPC_HOST, GRPC_PORT, GRPC_PLAIN_TEXT);
    }

    @Test
    void shouldReturnEmptyPropertyNamesWhenNoUrlConfigured() {
        GrpcFromUrlConfigSource source = createSource(null);

        assertThat(source.getPropertyNames()).isEmpty();
    }

    @Test
    void shouldReturnAllPropertiesViaGetProperties() {
        GrpcFromUrlConfigSource source = createSource("http://test-host:7070");

        assertThat(source.getProperties())
                .containsEntry(GRPC_HOST, "test-host")
                .containsEntry(GRPC_PORT, "7070")
                .containsEntry(GRPC_PLAIN_TEXT, "true");
    }

    @Test
    void shouldHaveCorrectOrdinal() {
        GrpcFromUrlConfigSource source = createSource("http://test:8080");

        // Should be 150 - higher than application.properties (100) but lower than env vars
        // (300)
        assertThat(source.getOrdinal()).isEqualTo(150);
    }

    @Test
    void shouldHaveCorrectName() {
        GrpcFromUrlConfigSource source = createSource("http://test:8080");

        assertThat(source.getName()).isEqualTo("memory-service-grpc-url-derived");
    }

    @Test
    void shouldHandleInvalidUrl() {
        GrpcFromUrlConfigSource source = createSource("not-a-valid-url");

        // Should return null for invalid URL, letting other sources provide defaults
        assertThat(source.getValue(GRPC_HOST)).isNull();
    }

    @Test
    void shouldHandleEmptyUrl() {
        GrpcFromUrlConfigSource source = createSource("");

        assertThat(source.getValue(GRPC_HOST)).isNull();
    }

    @Test
    void shouldHandleBlankUrl() {
        GrpcFromUrlConfigSource source = createSource("   ");

        assertThat(source.getValue(GRPC_HOST)).isNull();
    }

    @Test
    void shouldReturnConsistentValues() {
        GrpcFromUrlConfigSource source = createSource("http://cached-host:8080");

        // Call multiple times - values should be consistent
        String host1 = source.getValue(GRPC_HOST);
        String host2 = source.getValue(GRPC_HOST);
        String port1 = source.getValue(GRPC_PORT);

        assertThat(host1).isEqualTo("cached-host");
        assertThat(host2).isEqualTo("cached-host");
        assertThat(port1).isEqualTo("8080");
    }

    // Tests for parseUrl static method

    @Test
    void parseUrl_shouldReturnEmptyMapForNullUrl() {
        Map<String, String> props = GrpcFromUrlConfigSource.parseUrl(null);
        assertThat(props).isEmpty();
    }

    @Test
    void parseUrl_shouldReturnEmptyMapForBlankUrl() {
        Map<String, String> props = GrpcFromUrlConfigSource.parseUrl("   ");
        assertThat(props).isEmpty();
    }

    @Test
    void parseUrl_shouldReturnEmptyMapForInvalidScheme() {
        Map<String, String> props = GrpcFromUrlConfigSource.parseUrl("ftp://host:21");
        assertThat(props).isEmpty();
    }

    @Test
    void parseUrl_shouldReturnEmptyMapForMissingHost() {
        Map<String, String> props = GrpcFromUrlConfigSource.parseUrl("http:///path");
        assertThat(props).isEmpty();
    }

    @Test
    void parseUrl_shouldParseHttpUrl() {
        Map<String, String> props = GrpcFromUrlConfigSource.parseUrl("http://myhost:8082");
        assertThat(props)
                .containsEntry(GRPC_HOST, "myhost")
                .containsEntry(GRPC_PORT, "8082")
                .containsEntry(GRPC_PLAIN_TEXT, "true");
    }

    @Test
    void parseUrl_shouldParseHttpsUrl() {
        Map<String, String> props = GrpcFromUrlConfigSource.parseUrl("https://secure-host:9443");
        assertThat(props)
                .containsEntry(GRPC_HOST, "secure-host")
                .containsEntry(GRPC_PORT, "9443")
                .containsEntry(GRPC_PLAIN_TEXT, "false");
    }
}
