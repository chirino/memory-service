package io.github.chirino.memory.deployment;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;

import org.junit.jupiter.api.Test;

class MemoryServiceDevServicesProcessorTest {

    @Test
    void releaseDefaultsToCompatibilityLine() {
        assertEquals(
                "ghcr.io/chirino/memory-service:0.0",
                MemoryServiceDevServicesProcessor.resolveImageName(null, "0.0.3"));
        assertEquals(
                "ghcr.io/chirino/memory-service:12.34",
                MemoryServiceDevServicesProcessor.resolveImageName(" ", "12.34.56"));
    }

    @Test
    void snapshotDefaultsToLatest() {
        assertEquals(
                "ghcr.io/chirino/memory-service:latest",
                MemoryServiceDevServicesProcessor.resolveImageName(null, "0.0.4-SNAPSHOT"));
        assertEquals(
                "ghcr.io/chirino/memory-service:latest",
                MemoryServiceDevServicesProcessor.resolveImageName(null, "999-SNAPSHOT"));
    }

    @Test
    void configuredImageOverridesReleaseDefault() {
        assertEquals(
                "ghcr.io/chirino/memory-service:0.0.3",
                MemoryServiceDevServicesProcessor.resolveImageName(
                        "ghcr.io/chirino/memory-service:0.0.3", "0.0.3"));
        assertEquals(
                "ghcr.io/chirino/memory-service@sha256:abc123",
                MemoryServiceDevServicesProcessor.resolveImageName(
                        "ghcr.io/chirino/memory-service@sha256:abc123", "0.0.3"));
    }

    @Test
    void buildEmbedsExtensionVersion() {
        String version = MemoryServiceDevServicesProcessor.loadExtensionVersion();

        assertNotNull(version);
        assertFalse(version.isBlank());
        assertFalse(version.contains("${"));
    }
}
