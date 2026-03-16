package io.github.chirino.memory.runtime;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

import org.junit.jupiter.api.Test;

class MemoryServiceClientUrlTest {

    @Test
    void parsesHttpUrl() {
        MemoryServiceClientUrl url = MemoryServiceClientUrl.parse("https://memory.local:8443");

        assertThat(url.usesUnixSocket()).isFalse();
        assertThat(url.logicalBaseUrl()).isEqualTo("https://memory.local:8443");
        assertThat(url.tcpUri().getHost()).isEqualTo("memory.local");
    }

    @Test
    void parsesUnixUrl() {
        MemoryServiceClientUrl url =
                MemoryServiceClientUrl.parse("unix:///home/test/.local/run/memory-service.sock");

        assertThat(url.usesUnixSocket()).isTrue();
        assertThat(url.unixSocketPath()).isEqualTo("/home/test/.local/run/memory-service.sock");
        assertThat(url.logicalBaseUrl()).isEqualTo("http://localhost");
    }

    @Test
    void rejectsRelativeUnixPath() {
        assertThatThrownBy(() -> MemoryServiceClientUrl.parse("unix://memory-service.sock"))
                .isInstanceOf(IllegalArgumentException.class)
                .hasMessageContaining("unix:///absolute/path");
    }
}
