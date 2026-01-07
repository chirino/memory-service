package io.github.chirino.memory.deployment;

import io.quarkus.arc.deployment.AdditionalBeanBuildItem;
import io.quarkus.datasource.deployment.spi.DevServicesDatasourceResultBuildItem;
import io.quarkus.deployment.IsDevServicesSupportedByLaunchMode;
import io.quarkus.deployment.annotations.BuildProducer;
import io.quarkus.deployment.annotations.BuildStep;
import io.quarkus.deployment.builditem.DevServicesResultBuildItem;
import io.quarkus.deployment.builditem.DockerStatusBuildItem;
import io.quarkus.deployment.builditem.LaunchModeBuildItem;
import io.quarkus.deployment.builditem.Startable;
import io.quarkus.deployment.dev.devservices.DevServicesConfig;
import io.quarkus.devservices.common.StartableContainer;
import io.quarkus.devservices.keycloak.KeycloakDevServicesConfigBuildItem;
import java.security.SecureRandom;
import java.time.Duration;
import java.util.Base64;
import java.util.HashMap;
import java.util.Map;
import java.util.Optional;
import java.util.function.Function;
import java.util.function.Supplier;
import org.eclipse.microprofile.config.ConfigProvider;
import org.jboss.logging.Logger;
import org.testcontainers.containers.GenericContainer;
import org.testcontainers.containers.wait.strategy.Wait;
import org.testcontainers.utility.DockerImageName;

public class DevServicesMemoryServiceProcessor {

    private static final Logger LOG = Logger.getLogger(DevServicesMemoryServiceProcessor.class);
    private static final String FEATURE = "memory-service";
    private static final int MEMORY_SERVICE_PORT = 8080;
    private static final String DEV_SERVICE_LABEL = "quarkus-dev-service-memory-service";
    private static final int API_KEY_BYTES = 32;
    private static final SecureRandom SECURE_RANDOM = new SecureRandom();

    @BuildStep
    AdditionalBeanBuildItem registerBeans() {
        return AdditionalBeanBuildItem.builder()
                .setUnremovable()
                .addBeanClasses(
                        "io.github.chirino.memory.langchain4j.MemoryServiceChatMemoryProvider",
                        "io.github.chirino.memory.langchain4j.RequestContextExecutor",
                        "io.github.chirino.memory.conversation.runtime.DefaultConversationStore",
                        "io.github.chirino.memory.runtime.MemoryServiceClientStartupObserver")
                .build();
    }

    @BuildStep
    AdditionalBeanBuildItem registerResponseResumerBeans() {
        return AdditionalBeanBuildItem.builder()
                .setUnremovable()
                .addBeanClasses(
                        "io.github.chirino.memory.conversation.runtime.NoopResponseResumer",
                        "io.github.chirino.memory.conversation.runtime.RedisResponseResumer")
                .build();
    }

    @BuildStep(onlyIf = {IsDevServicesSupportedByLaunchMode.class, DevServicesConfig.Enabled.class})
    public void startMemoryServiceDevService(
            LaunchModeBuildItem launchMode,
            DockerStatusBuildItem dockerStatus,
            DevServicesConfig devServicesConfig,
            Optional<DevServicesDatasourceResultBuildItem> datasourceResult,
            Optional<KeycloakDevServicesConfigBuildItem> keycloakResult,
            BuildProducer<DevServicesResultBuildItem> devServicesResult) {

        if (!dockerStatus.isContainerRuntimeAvailable()) {
            LOG.warn(
                    "Docker is not available. Please configure memory-service-client.url manually"
                            + " or start Docker.");
            return;
        }

        String memoryServiceUrl = null;
        try {
            memoryServiceUrl =
                    ConfigProvider.getConfig()
                            .getOptionalValue("memory-service-client.url", String.class)
                            .orElse(null);
        } catch (IllegalStateException e) {
        }
        if (memoryServiceUrl != null && !memoryServiceUrl.isEmpty()) {
            LOG.debugf(
                    "Not starting memory-service dev service as memory-service-client.url is set"
                            + " to: %s",
                    memoryServiceUrl);
            return;
        }

        // Determine the API key to use. Prefer an explicitly configured
        // memory-service-client.api-key.
        // If not present, generate a random Base64-encoded key and expose it both to the container
        // (as MEMORY_API_KEYS) and to the application config (as memory-service-client.api-key).
        String configuredApiKey = null;
        try {
            configuredApiKey =
                    ConfigProvider.getConfig()
                            .getOptionalValue("memory-service-client.api-key", String.class)
                            .orElse(null);
        } catch (IllegalStateException e) {
            // Config may not be available in some edge cases; fall through to generating a key.
            LOG.debug(
                    "Unable to read memory-service-client.api-key from config, generating a random"
                            + " key.",
                    e);
        }

        final boolean generatedApiKey;
        final String effectiveApiKey;
        if (configuredApiKey == null || configuredApiKey.isBlank()) {
            effectiveApiKey = generateRandomApiKey();
            generatedApiKey = true;
            LOG.info(
                    "No memory-service-client.api-key configured; generated a random API key for"
                            + " Dev Services.");
        } else {
            effectiveApiKey = configuredApiKey;
            generatedApiKey = false;
            LOG.debug("Using configured memory-service-client.api-key for Dev Services.");
        }

        LOG.info("Starting memory-service dev service...");

        Map<String, Function<Startable, String>> configProviders = new HashMap<>();
        configProviders.put(
                "memory-service-client.url",
                (Function<Startable, String>) s -> getConnectionInfo(s));

        if (generatedApiKey) {
            configProviders.put("memory-service-client.api-key", s -> effectiveApiKey);
        }

        devServicesResult.produce(
                DevServicesResultBuildItem.owned()
                        .feature(FEATURE)
                        .serviceName("memory-service")
                        .startable(
                                new Supplier<Startable>() {
                                    @Override
                                    public Startable get() {
                                        LOG.infof(
                                                "Starting memory-service container (image:"
                                                        + " memory-service-service:latest)");
                                        GenericContainer<?> container =
                                                new GenericContainer<>(
                                                                DockerImageName.parse(
                                                                        "memory-service-service:latest"))
                                                        .withEnv("MEMORY_API_KEYS", effectiveApiKey)
                                                        .withEnv("MEMORY_VECTOR_TYPE", "pgvector")
                                                        .withExposedPorts(MEMORY_SERVICE_PORT)
                                                        .withLabel(
                                                                DEV_SERVICE_LABEL,
                                                                "memory-service");

                                        // Wait for Quarkus to report that it is listening instead
                                        // of relying on
                                        // Testcontainers' default port check, which expects `nc`
                                        // inside the image
                                        // and leads to noisy warnings when it's not present.
                                        container.waitingFor(
                                                Wait.forLogMessage(".*Listening on.*", 1)
                                                        .withStartupTimeout(Duration.ofMinutes(2)));

                                        // If we have a postgres dev service, tell the
                                        // memory-service container about it.
                                        if (datasourceResult.isPresent()) {
                                            String jdbcUrl =
                                                    datasourceResult
                                                            .get()
                                                            .getDefaultDatasource()
                                                            .getConfigProperties()
                                                            .get("quarkus.datasource.jdbc.url");
                                            if (jdbcUrl != null) {
                                                String containerJdbcUrl =
                                                        jdbcUrl.replace(
                                                                "localhost",
                                                                "host.docker.internal");
                                                LOG.infof(
                                                        "Configuring memory-service container with"
                                                                + " JDBC URL: %s",
                                                        containerJdbcUrl);
                                                container.withEnv(
                                                        "QUARKUS_DATASOURCE_JDBC_URL",
                                                        containerJdbcUrl);
                                                container.withEnv(
                                                        "QUARKUS_DATASOURCE_USERNAME",
                                                        datasourceResult
                                                                .get()
                                                                .getDefaultDatasource()
                                                                .getConfigProperties()
                                                                .get(
                                                                        "quarkus.datasource.username"));
                                                container.withEnv(
                                                        "QUARKUS_DATASOURCE_PASSWORD",
                                                        datasourceResult
                                                                .get()
                                                                .getDefaultDatasource()
                                                                .getConfigProperties()
                                                                .get(
                                                                        "quarkus.datasource.password"));
                                                container.withEnv(
                                                        "QUARKUS_DATASOURCE_DB_KIND", "postgresql");
                                                // Ensure migrations run on startup in the container
                                                container.withEnv(
                                                        "QUARKUS_LIQUIBASE_MIGRATE_AT_START",
                                                        "true");
                                            }
                                        }

                                        // If we have a keycloak dev service, tell the
                                        // memory-service container about it.
                                        if (keycloakResult.isPresent()) {
                                            String authServerUrl =
                                                    keycloakResult
                                                            .get()
                                                            .getConfig()
                                                            .get("quarkus.oidc.auth-server-url");
                                            if (authServerUrl != null) {
                                                String containerAuthServerUrl =
                                                        authServerUrl.replace(
                                                                "localhost",
                                                                "host.docker.internal");
                                                LOG.infof(
                                                        "Configuring memory-service container with"
                                                                + " OIDC Auth Server URL: %s",
                                                        containerAuthServerUrl);
                                                container.withEnv(
                                                        "QUARKUS_OIDC_AUTH_SERVER_URL",
                                                        containerAuthServerUrl);
                                                // The token issuer in the JWT will be 'localhost'
                                                // (from the browser/agent perspective).
                                                // We must tell the containerized service to accept
                                                // this issuer.
                                                container.withEnv(
                                                        "QUARKUS_OIDC_TOKEN_ISSUER", authServerUrl);
                                            }
                                        }

                                        container.start();

                                        String url =
                                                "http://"
                                                        + container.getHost()
                                                        + ":"
                                                        + container.getMappedPort(
                                                                MEMORY_SERVICE_PORT);
                                        LOG.infof(
                                                "Dev Services for memory-service started. The"
                                                        + " service is available at %s",
                                                url);

                                        StartableContainer<GenericContainer<?>> startable =
                                                new StartableContainer<>(container, c -> url);
                                        return startable;
                                    }
                                })
                        .configProvider(configProviders)
                        .build());
    }

    private String generateRandomApiKey() {
        byte[] bytes = new byte[API_KEY_BYTES];
        SECURE_RANDOM.nextBytes(bytes);
        return Base64.getUrlEncoder().withoutPadding().encodeToString(bytes);
    }

    private String getConnectionInfo(Startable startable) {
        if (startable instanceof StartableContainer) {
            StartableContainer<?> sc = (StartableContainer<?>) startable;
            GenericContainer<?> container = sc.getContainer();
            return "http://"
                    + container.getHost()
                    + ":"
                    + container.getMappedPort(MEMORY_SERVICE_PORT);
        }
        return startable.getConnectionInfo();
    }
}
