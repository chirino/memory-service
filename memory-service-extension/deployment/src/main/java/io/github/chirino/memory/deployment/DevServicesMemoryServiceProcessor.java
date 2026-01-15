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
import java.util.List;
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
                        "io.github.chirino.memory.runtime.MemoryServiceClientStartupObserver")
                .build();
    }

    /**
     * Produces a MemoryServiceDevServicesConfigBuildItem by extracting memory-service
     * configuration from dev services results. This follows the pattern of
     * KeycloakDevServicesConfigBuildItem and allows other extensions to consume
     * the memory-service configuration.
     */
    @BuildStep(onlyIf = {IsDevServicesSupportedByLaunchMode.class, DevServicesConfig.Enabled.class})
    MemoryServiceDevServicesConfigBuildItem produceMemoryServiceConfig(
            List<DevServicesResultBuildItem> devServicesResults) {
        Map<String, String> config = new HashMap<>();
        if (devServicesResults != null) {
            for (DevServicesResultBuildItem result : devServicesResults) {
                Map<String, String> resultConfig = result.getConfig();
                if (resultConfig != null) {
                    // Look for memory-service related config
                    String url = resultConfig.get("memory-service-client.url");
                    if (url != null) {
                        config.put("memory-service-client.url", url);
                    }
                    String apiKey = resultConfig.get("memory-service-client.api-key");
                    if (apiKey != null) {
                        config.put("memory-service-client.api-key", apiKey);
                    }
                    String grpcHost = resultConfig.get("quarkus.grpc.clients.responseresumer.host");
                    if (grpcHost != null) {
                        config.put("quarkus.grpc.clients.responseresumer.host", grpcHost);
                    }
                    String grpcPort = resultConfig.get("quarkus.grpc.clients.responseresumer.port");
                    if (grpcPort != null) {
                        config.put("quarkus.grpc.clients.responseresumer.port", grpcPort);
                    }
                    String grpcPlainText =
                            resultConfig.get("quarkus.grpc.clients.responseresumer.plain-text");
                    if (grpcPlainText != null) {
                        config.put(
                                "quarkus.grpc.clients.responseresumer.plain-text", grpcPlainText);
                    }
                }
            }
        }
        return new MemoryServiceDevServicesConfigBuildItem(config);
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
        // (as MEMORY_SERVICE_API_KEYS_AGENT) and to the application config (as
        // memory-service-client.api-key).
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
            LOG.info("No memory-service-client.api-key configured; generated a random API key.");
        } else {
            effectiveApiKey = configuredApiKey;
            generatedApiKey = false;
            LOG.debug("Using configured memory-service-client.api-key for Dev Services.");
        }

        // Read response resumer configuration if set
        final String responseResumerConfig = getResponseResumerConfig();

        LOG.info("Starting memory-service dev service...");

        Map<String, Function<Startable, String>> configProviders = new HashMap<>();
        configProviders.put(
                "memory-service-client.url",
                (Function<Startable, String>) s -> getConnectionInfo(s));

        // Configure gRPC client using host, port, and TLS settings (as per Quarkus gRPC client
        // configuration)
        configProviders.put(
                "quarkus.grpc.clients.responseresumer.host",
                (Function<Startable, String>) s -> parseHost(getConnectionInfo(s)));
        configProviders.put(
                "quarkus.grpc.clients.responseresumer.port",
                (Function<Startable, String>) s -> parsePort(getConnectionInfo(s)));
        configProviders.put(
                "quarkus.grpc.clients.responseresumer.plain-text",
                (Function<Startable, String>) s -> parsePlainText(getConnectionInfo(s)));

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
                                        GenericContainer<?> container =
                                                new GenericContainer<>(
                                                                DockerImageName.parse(
                                                                        "memory-service-service:latest"))
                                                        .withEnv(
                                                                "MEMORY_SERVICE_API_KEYS_AGENT",
                                                                effectiveApiKey)
                                                        .withEnv(
                                                                "MEMORY_SERVICE_VECTOR_TYPE",
                                                                "pgvector")
                                                        .withExposedPorts(MEMORY_SERVICE_PORT)
                                                        .withLabel(
                                                                DEV_SERVICE_LABEL,
                                                                "memory-service");

                                        // Pass response resumer configuration if set
                                        if (responseResumerConfig != null
                                                && !responseResumerConfig.isBlank()) {
                                            // Set as Quarkus config property via environment
                                            // variable.
                                            // Note: Quarkus converts env vars to config properties
                                            // by replacing
                                            // underscores with dots. Since the property name is
                                            // "memory-service.response-resumer" (with hyphens), we
                                            // need to set
                                            // it in a way that Quarkus can map it correctly.
                                            // We use the format where dots and hyphens become
                                            // underscores.
                                            // Quarkus will attempt to match this to config
                                            // properties.
                                            container.withEnv(
                                                    "MEMORY_SERVICE_RESPONSE_RESUMER",
                                                    responseResumerConfig);
                                            LOG.debugf(
                                                    "Configuring memory-service container with"
                                                            + " response-resumer: %s",
                                                    responseResumerConfig);
                                        }

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

                                        // Configure Redis hosts for the memory-service container
                                        // only if response-resumer is set to "redis".
                                        // First check if Redis hosts are already configured in the
                                        // app config.
                                        // If not, look for the Redis dev service container.
                                        if ("redis".equalsIgnoreCase(responseResumerConfig)) {
                                            String redisHosts = null;
                                            try {
                                                redisHosts =
                                                        ConfigProvider.getConfig()
                                                                .getOptionalValue(
                                                                        "quarkus.redis.hosts",
                                                                        String.class)
                                                                .orElse(null);
                                            } catch (IllegalStateException e) {
                                                // Config not available, will try dev service
                                            }

                                            // If not configured, look for Redis dev service
                                            if (redisHosts == null || redisHosts.isBlank()) {
                                                redisHosts = findRedisDevServiceUrl();
                                            }

                                            if (redisHosts != null && !redisHosts.isBlank()) {
                                                String containerRedisHosts =
                                                        redisHosts.replace(
                                                                "localhost",
                                                                "host.docker.internal");
                                                LOG.infof(
                                                        "Configuring memory-service container with"
                                                                + " QUARKUS_REDIS_HOSTS=%s",
                                                        containerRedisHosts);
                                                container.withEnv(
                                                        "QUARKUS_REDIS_HOSTS", containerRedisHosts);
                                            } else {
                                                LOG.warn(
                                                        "Redis config not available. Container will"
                                                                + " start without Redis"
                                                                + " configuration.");
                                            }
                                        }

                                        // Configure Infinispan server list for the
                                        // memory-service container only if response-resumer is
                                        // set to "infinispan".
                                        if ("infinispan".equalsIgnoreCase(responseResumerConfig)) {
                                            String serverList = null;
                                            try {
                                                serverList =
                                                        ConfigProvider.getConfig()
                                                                .getOptionalValue(
                                                                        "quarkus.infinispan-client.server-list",
                                                                        String.class)
                                                                .orElse(null);
                                            } catch (IllegalStateException e) {
                                                // Config not available, will try dev service
                                            }

                                            if (serverList == null || serverList.isBlank()) {
                                                serverList = findInfinispanDevServiceUrl();
                                            }

                                            if (serverList != null && !serverList.isBlank()) {
                                                String containerServerList =
                                                        serverList.replace(
                                                                "localhost",
                                                                "host.docker.internal");
                                                LOG.infof(
                                                        "Configuring memory-service container with"
                                                            + " QUARKUS_INFINISPAN_CLIENT_SERVER_LIST=%s",
                                                        containerServerList);
                                                container.withEnv(
                                                        "QUARKUS_INFINISPAN_CLIENT_SERVER_LIST",
                                                        containerServerList);
                                            } else {
                                                LOG.warn(
                                                        "Infinispan config not available. Container"
                                                                + " will start without Infinispan"
                                                                + " configuration.");
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

    private String getResponseResumerConfig() {
        try {
            return ConfigProvider.getConfig()
                    .getOptionalValue("memory-service.response-resumer", String.class)
                    .orElse(null);
        } catch (IllegalStateException e) {
            LOG.debug(
                    "Unable to read memory-service.response-resumer from config, using default.",
                    e);
            return null;
        }
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

    private String parseHost(String url) {
        if (url == null || url.isBlank()) {
            return "localhost";
        }
        try {
            java.net.URI uri = java.net.URI.create(url);
            return uri.getHost() != null ? uri.getHost() : "localhost";
        } catch (Exception e) {
            LOG.debugf("Failed to parse host from URL: %s", url, e);
            return "localhost";
        }
    }

    private String parsePort(String url) {
        if (url == null || url.isBlank()) {
            return String.valueOf(MEMORY_SERVICE_PORT);
        }
        try {
            java.net.URI uri = java.net.URI.create(url);
            int port = uri.getPort();
            if (port == -1) {
                // Default port based on scheme
                port = "https".equals(uri.getScheme()) ? 443 : 80;
            }
            return String.valueOf(port);
        } catch (Exception e) {
            LOG.debugf("Failed to parse port from URL: %s", url, e);
            return String.valueOf(MEMORY_SERVICE_PORT);
        }
    }

    private String parsePlainText(String url) {
        if (url == null || url.isBlank()) {
            // Default to plain-text (HTTP) for dev services
            return "true";
        }
        try {
            java.net.URI uri = java.net.URI.create(url);
            // If HTTPS, disable plain-text (enable TLS). If HTTP, enable plain-text.
            return "https".equals(uri.getScheme()) ? "false" : "true";
        } catch (Exception e) {
            LOG.debugf("Failed to parse scheme from URL: %s", url, e);
            // Default to plain-text (HTTP) if parsing fails
            return "true";
        }
    }

    /**
     * Finds the Redis dev service container and returns its connection URL.
     * Since dev services config isn't available via ConfigProvider during concurrent
     * container startup, we directly query Docker for running Redis containers
     * with the Quarkus dev service label.
     *
     * @return the Redis hosts URL, or null if not found
     */
    private String findRedisDevServiceUrl() {
        // Poll for up to 30 seconds for the Redis container to be available
        for (int i = 0; i < 60; i++) {
            try {
                var dockerClient = org.testcontainers.DockerClientFactory.instance().client();
                var containers =
                        dockerClient
                                .listContainersCmd()
                                .withLabelFilter(List.of("quarkus-dev-service-redis"))
                                .withStatusFilter(List.of("running"))
                                .exec();

                if (!containers.isEmpty()) {
                    var redisContainer = containers.get(0);
                    var ports = redisContainer.getPorts();
                    for (var port : ports) {
                        if (port.getPrivatePort() == 6379 && port.getPublicPort() != null) {
                            return "redis://localhost:" + port.getPublicPort();
                        }
                    }
                }
            } catch (Exception e) {
                LOG.debugf("Error querying Docker for Redis container: %s", e.getMessage());
            }

            if (i < 59) {
                try {
                    Thread.sleep(500);
                } catch (InterruptedException e) {
                    Thread.currentThread().interrupt();
                    break;
                }
            }
        }

        LOG.warn("Redis dev service container not found after 30 seconds");
        return null;
    }

    /**
     * Finds the Infinispan dev service container and returns its server list.
     *
     * @return the Infinispan server list, or null if not found
     */
    private String findInfinispanDevServiceUrl() {
        for (int i = 0; i < 60; i++) {
            try {
                var dockerClient = org.testcontainers.DockerClientFactory.instance().client();
                var containers =
                        dockerClient
                                .listContainersCmd()
                                .withLabelFilter(List.of("quarkus-dev-service-infinispan"))
                                .withStatusFilter(List.of("running"))
                                .exec();

                if (!containers.isEmpty()) {
                    var infinispanContainer = containers.get(0);
                    var ports = infinispanContainer.getPorts();
                    for (var port : ports) {
                        if (port.getPrivatePort() == 11222 && port.getPublicPort() != null) {
                            return "localhost:" + port.getPublicPort();
                        }
                    }
                }
            } catch (Exception e) {
                LOG.debugf("Error querying Docker for Infinispan container: %s", e.getMessage());
            }

            if (i < 59) {
                try {
                    Thread.sleep(500);
                } catch (InterruptedException e) {
                    Thread.currentThread().interrupt();
                    break;
                }
            }
        }

        LOG.warn("Infinispan dev service container not found after 30 seconds");
        return null;
    }
}
