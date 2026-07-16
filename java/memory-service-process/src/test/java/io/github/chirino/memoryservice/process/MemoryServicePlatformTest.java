package io.github.chirino.memoryservice.process;

import static org.junit.jupiter.api.Assertions.assertEquals;

import org.junit.jupiter.api.Test;

class MemoryServicePlatformTest {

    @Test
    void normalizesSupportedAliases() {
        assertEquals(
                new MemoryServicePlatform("linux", "amd64"),
                MemoryServicePlatform.from("Linux", "x86_64"));
        assertEquals(
                new MemoryServicePlatform("linux", "arm64"),
                MemoryServicePlatform.from("GNU/Linux", "aarch64"));
        assertEquals(
                new MemoryServicePlatform("macos", "arm64"),
                MemoryServicePlatform.from("Mac OS X", "arm64"));
        assertEquals(
                new MemoryServicePlatform("macos", "amd64"),
                MemoryServicePlatform.from("Darwin", "amd64"));
    }
}
