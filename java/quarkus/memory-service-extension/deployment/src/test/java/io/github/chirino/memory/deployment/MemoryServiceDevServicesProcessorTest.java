package io.github.chirino.memory.deployment;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;

import org.junit.jupiter.api.Test;
import org.testcontainers.containers.GenericContainer;
import org.testcontainers.utility.DockerImageName;

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

    @Test
    void defaultEnvironmentConfiguresDevelopmentDek() {
        GenericContainer<?> container =
                new GenericContainer<>(
                        DockerImageName.parse("example.invalid/memory-service:test"));

        MemoryServiceDevServicesProcessor.configureDefaultEnvironment(container, "test-api-key");

        assertEquals("0.0.0.0", container.getEnvMap().get("MEMORY_SERVICE_HOST"));
        assertEquals("true", container.getEnvMap().get("MEMORY_SERVICE_PLAIN_TEXT"));
        assertEquals("false", container.getEnvMap().get("MEMORY_SERVICE_TLS"));
        assertEquals(
                "true", container.getEnvMap().get("MEMORY_SERVICE_ALLOW_NON_LOOPBACK_PLAINTEXT"));
        assertFalse(container.getEnvMap().containsKey("MEMORY_SERVICE_TLS_SELF_SIGNED"));
        assertEquals("dek", container.getEnvMap().get("MEMORY_SERVICE_ENCRYPTION_KIND"));
        assertEquals(
                "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
                container.getEnvMap().get("MEMORY_SERVICE_ENCRYPTION_DEK_KEY"));
        assertEquals(
                "true", container.getEnvMap().get("MEMORY_SERVICE_MANAGEMENT_ON_MAIN_LISTENER"));
        assertFalse(container.getEnvMap().containsKey("MEMORY_SERVICE_ENCRYPTION_ALLOW_PLAIN"));
        assertFalse(container.getEnvMap().containsKey("MEMORY_SERVICE_DEVELOPER_FRONTEND_ENABLED"));
    }

    @Test
    void fixedPortConfiguresDeveloperFrontendBaseUrl() {
        GenericContainer<?> container =
                new GenericContainer<>(
                        DockerImageName.parse("example.invalid/memory-service:test"));

        MemoryServiceDevServicesProcessor.configureDefaultEnvironment(container, "agent-key");
        MemoryServiceDevServicesProcessor.configureFixedPortEnvironment(
                container, 8082, "developer-console-key");

        assertEquals(
                "true", container.getEnvMap().get("MEMORY_SERVICE_DEVELOPER_FRONTEND_ENABLED"));
        assertEquals("http://localhost:8082", container.getEnvMap().get("MEMORY_SERVICE_BASE_URL"));
        assertEquals(
                "developer_frontend",
                container.getEnvMap().get("MEMORY_SERVICE_DEVELOPER_FRONTEND_CLIENT_ID"));
        assertEquals(
                "api-key",
                container.getEnvMap().get("MEMORY_SERVICE_DEVELOPER_FRONTEND_AUTH_MODE"));
        assertEquals(
                "developer-console-key",
                container.getEnvMap().get("MEMORY_SERVICE_DEVELOPER_FRONTEND_API_KEY"));
        assertEquals(
                "developer-console-key",
                container.getEnvMap().get("MEMORY_SERVICE_API_KEYS_DEVELOPER_FRONTEND"));
        assertEquals(
                "developer_frontend",
                container.getEnvMap().get("MEMORY_SERVICE_ROLES_ADMIN_CLIENTS"));
        assertNotEquals(
                container.getEnvMap().get("MEMORY_SERVICE_API_KEYS_AGENT"),
                container.getEnvMap().get("MEMORY_SERVICE_API_KEYS_DEVELOPER_FRONTEND"));
    }

    @Test
    void explicitOidcModeOverridesFixedPortDeveloperFrontendDefault() {
        GenericContainer<?> container =
                new GenericContainer<>(
                        DockerImageName.parse("example.invalid/memory-service:test"));

        MemoryServiceDevServicesProcessor.configureFixedPortEnvironment(
                container, 8082, "developer-console-key");
        container.withEnv("MEMORY_SERVICE_DEVELOPER_FRONTEND_AUTH_MODE", "oidc");
        container.withEnv("MEMORY_SERVICE_DEVELOPER_FRONTEND_CLIENT_ID", "developer-frontend");

        assertEquals(
                "oidc", container.getEnvMap().get("MEMORY_SERVICE_DEVELOPER_FRONTEND_AUTH_MODE"));
        assertEquals(
                "developer-frontend",
                container.getEnvMap().get("MEMORY_SERVICE_DEVELOPER_FRONTEND_CLIENT_ID"));
    }

    @Test
    void keycloakEnvironmentRelaxesAudienceRequirement() {
        GenericContainer<?> container =
                new GenericContainer<>(
                        DockerImageName.parse("example.invalid/memory-service:test"));

        MemoryServiceDevServicesProcessor.configureKeycloakDevService(
                container, "http://localhost:8081/realms/memory-service");

        assertEquals(
                "http://localhost:8081/realms/memory-service",
                container.getEnvMap().get("MEMORY_SERVICE_OIDC_ISSUER"));
        assertEquals(
                "http://host.docker.internal:8081/realms/memory-service",
                container.getEnvMap().get("MEMORY_SERVICE_OIDC_DISCOVERY_URL"));
        assertEquals(
                "true", container.getEnvMap().get("MEMORY_SERVICE_OIDC_ALLOW_MISSING_AUDIENCE"));
        assertFalse(container.getEnvMap().containsKey("MEMORY_SERVICE_OIDC_ALLOWED_AUDIENCES"));
    }

    @Test
    void keycloakEnvironmentPreservesStrictAudienceOverride() {
        GenericContainer<?> container =
                new GenericContainer<>(DockerImageName.parse("example.invalid/memory-service:test"))
                        .withEnv("MEMORY_SERVICE_OIDC_ALLOW_MISSING_AUDIENCE", "false")
                        .withEnv("MEMORY_SERVICE_OIDC_ALLOWED_AUDIENCES", "custom-audience");

        MemoryServiceDevServicesProcessor.configureKeycloakDevService(
                container, "http://localhost:8081/realms/custom");

        assertEquals(
                "false", container.getEnvMap().get("MEMORY_SERVICE_OIDC_ALLOW_MISSING_AUDIENCE"));
        assertEquals(
                "custom-audience",
                container.getEnvMap().get("MEMORY_SERVICE_OIDC_ALLOWED_AUDIENCES"));
    }
}
