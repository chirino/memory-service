package io.github.chirino.memory.deployment;

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
import org.testcontainers.containers.FixedHostPortGenericContainer;
import org.testcontainers.containers.GenericContainer;
import org.testcontainers.containers.wait.strategy.Wait;
import org.testcontainers.utility.DockerImageName;

public class MemoryServiceDevServicesProcessor {

    private static final Logger LOG = Logger.getLogger(MemoryServiceDevServicesProcessor.class);
    private static final String FEATURE = "memory-service";
    private static final int MEMORY_SERVICE_PORT = 8080;
    private static final String DEV_SERVICE_LABEL = "quarkus-dev-service-memory-service";
    private static final int API_KEY_BYTES = 32;
    private static final SecureRandom SECURE_RANDOM = new SecureRandom();

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
                    String url = resultConfig.get("memory-service.client.url");
                    if (url != null) {
                        config.put("memory-service.client.url", url);
                    }
                    String apiKey = resultConfig.get("memory-service.client.api-key");
                    if (apiKey != null) {
                        config.put("memory-service.client.api-key", apiKey);
                    }
                    String grpcHost =
                            resultConfig.get("quarkus.grpc.clients.responserecorder.host");
                    if (grpcHost != null) {
                        config.put("quarkus.grpc.clients.responserecorder.host", grpcHost);
                    }
                    String grpcPort =
                            resultConfig.get("quarkus.grpc.clients.responserecorder.port");
                    if (grpcPort != null) {
                        config.put("quarkus.grpc.clients.responserecorder.port", grpcPort);
                    }
                    String grpcPlainText =
                            resultConfig.get("quarkus.grpc.clients.responserecorder.plain-text");
                    if (grpcPlainText != null) {
                        config.put(
                                "quarkus.grpc.clients.responserecorder.plain-text", grpcPlainText);
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
                    "Docker is not available. Please configure memory-service.client.url manually"
                            + " or start Docker.");
            return;
        }

        String memoryServiceUrl = null;
        try {
            memoryServiceUrl =
                    ConfigProvider.getConfig()
                            .getOptionalValue("memory-service.client.url", String.class)
                            .orElse(null);
        } catch (IllegalStateException e) {
        }
        if (memoryServiceUrl != null && !memoryServiceUrl.isEmpty()) {
            LOG.debugf(
                    "Not starting memory-service dev service as memory-service.client.url is set"
                            + " to: %s",
                    memoryServiceUrl);
            return;
        }

        // Determine the API key to use. Prefer an explicitly configured
        // memory-service.client.api-key.
        // If not present, check if memory-service.devservices.env.MEMORY_SERVICE_API_KEYS_AGENT
        // is configured and extract the key from there.
        // If not present, generate a random Base64-encoded key and expose it both to the container
        // (as MEMORY_SERVICE_API_KEYS_AGENT) and to the application config (as
        // memory-service.client.api-key).
        String configuredApiKey = null;
        try {
            configuredApiKey =
                    ConfigProvider.getConfig()
                            .getOptionalValue("memory-service.client.api-key", String.class)
                            .orElse(null);
        } catch (IllegalStateException e) {
            // Config may not be available in some edge cases; fall through to other sources.
            LOG.debug("Unable to read memory-service.client.api-key from config.", e);
        }

        // Read additional environment variables to pass to the container
        final Map<String, String> additionalEnvVars = getAdditionalEnvVars();

        // If no explicit api-key configured, check if it's provided via devservices env vars
        if (configuredApiKey == null || configuredApiKey.isBlank()) {
            String envApiKey = additionalEnvVars.get("MEMORY_SERVICE_API_KEYS_AGENT");
            if (envApiKey != null && !envApiKey.isBlank()) {
                configuredApiKey = envApiKey;
                LOG.debug(
                        "Using memory-service.client.api-key from"
                                + " memory-service.devservices.env.MEMORY_SERVICE_API_KEYS_AGENT");
            }
        }

        final boolean generatedApiKey;
        final String effectiveApiKey;
        if (configuredApiKey == null || configuredApiKey.isBlank()) {
            effectiveApiKey = generateRandomApiKey();
            generatedApiKey = true;
            LOG.info("No memory-service.client.api-key configured; generated a random API key.");
        } else {
            effectiveApiKey = configuredApiKey;
            generatedApiKey = false;
            LOG.debug("Using configured memory-service.client.api-key for Dev Services.");
        }

        // Read response resumer configuration if set
        final String responseResumerConfig = getResponseResumerConfig();

        // Read optional fixed port configuration
        final Integer fixedPort = getFixedPortConfig();

        LOG.info("Starting memory-service dev service...");

        Map<String, Function<Startable, String>> configProviders = new HashMap<>();
        configProviders.put(
                "memory-service.client.url",
                (Function<Startable, String>) s -> getConnectionInfo(s));

        // Configure gRPC client using host, port, and TLS settings (as per Quarkus gRPC client
        // configuration)
        configProviders.put(
                "quarkus.grpc.clients.responserecorder.host",
                (Function<Startable, String>) s -> parseHost(getConnectionInfo(s)));
        configProviders.put(
                "quarkus.grpc.clients.responserecorder.port",
                (Function<Startable, String>) s -> parsePort(getConnectionInfo(s)));
        configProviders.put(
                "quarkus.grpc.clients.responserecorder.plain-text",
                (Function<Startable, String>) s -> parsePlainText(getConnectionInfo(s)));

        if (generatedApiKey) {
            configProviders.put("memory-service.client.api-key", s -> effectiveApiKey);
        }

        devServicesResult.produce(
                DevServicesResultBuildItem.owned()
                        .feature(FEATURE)
                        .serviceName("memory-service")
                        .startable(
                                new Supplier<Startable>() {
                                    @Override
                                    public Startable get() {
                                        GenericContainer<?> container;
                                        if (fixedPort != null) {
                                            container =
                                                    new FixedHostPortGenericContainer<>(
                                                                    "ghcr.io/chirino/memory-service:latest")
                                                            .withFixedExposedPort(
                                                                    fixedPort, MEMORY_SERVICE_PORT);
                                            LOG.infof(
                                                    "Configuring memory-service dev service with"
                                                            + " fixed port: %d",
                                                    fixedPort);
                                        } else {
                                            container =
                                                    new GenericContainer<>(
                                                                    DockerImageName.parse(
                                                                            "ghcr.io/chirino/memory-service:latest"))
                                                            .withExposedPorts(MEMORY_SERVICE_PORT);
                                        }
                                        container
                                                .withEnv(
                                                        "MEMORY_SERVICE_API_KEYS_AGENT",
                                                        effectiveApiKey)
                                                .withEnv("MEMORY_SERVICE_VECTOR_KIND", "pgvector")
                                                .withLabel(DEV_SERVICE_LABEL, "memory-service");

                                        // Apply additional environment variables from config
                                        for (Map.Entry<String, String> entry :
                                                additionalEnvVars.entrySet()) {
                                            container.withEnv(entry.getKey(), entry.getValue());
                                            LOG.debugf(
                                                    "Setting container env: %s=%s",
                                                    entry.getKey(), entry.getValue());
                                        }

                                        // Pass cache configuration if set
                                        if (responseResumerConfig != null
                                                && !responseResumerConfig.isBlank()) {
                                            container.withEnv(
                                                    "MEMORY_SERVICE_CACHE_KIND",
                                                    responseResumerConfig);
                                            LOG.debugf(
                                                    "Configuring memory-service container with"
                                                            + " cache kind: %s",
                                                    responseResumerConfig);
                                        }

                                        // Wait for the Go service's /ready endpoint to return 200
                                        // (all initialization complete: migrations, connections,
                                        // listeners). Avoids log-scraping and works with nc-less
                                        // images.
                                        container.waitingFor(
                                                Wait.forHttp("/ready")
                                                        .forPort(MEMORY_SERVICE_PORT)
                                                        .forStatusCode(200)
                                                        .withStartupTimeout(Duration.ofMinutes(2)));

                                        // Connect the memory-service container to the postgres dev
                                        // service.
                                        // DevServicesDatasourceResultBuildItem is only produced on
                                        // first start; on hot-reload of the extension with an
                                        // already-running PG container Quarkus reuses the container
                                        // without re-producing the build item. Fall back to
                                        // ConfigProvider in that case.
                                        String jdbcUrl = null;
                                        String dbUsername = null;
                                        String dbPassword = null;

                                        if (datasourceResult.isPresent()
                                                && datasourceResult.get().getDefaultDatasource()
                                                        != null) {
                                            Map<String, String> dsProps =
                                                    datasourceResult
                                                            .get()
                                                            .getDefaultDatasource()
                                                            .getConfigProperties();
                                            jdbcUrl = dsProps.get("quarkus.datasource.jdbc.url");
                                            dbUsername = dsProps.get("quarkus.datasource.username");
                                            dbPassword = dsProps.get("quarkus.datasource.password");
                                        }

                                        // Fallback: read from live config (hot-reload case)
                                        if (jdbcUrl == null) {
                                            try {
                                                var cfg = ConfigProvider.getConfig();
                                                jdbcUrl =
                                                        cfg.getOptionalValue(
                                                                        "quarkus.datasource.jdbc.url",
                                                                        String.class)
                                                                .orElse(null);
                                                dbUsername =
                                                        cfg.getOptionalValue(
                                                                        "quarkus.datasource.username",
                                                                        String.class)
                                                                .orElse(null);
                                                dbPassword =
                                                        cfg.getOptionalValue(
                                                                        "quarkus.datasource.password",
                                                                        String.class)
                                                                .orElse(null);
                                            } catch (IllegalStateException e) {
                                                LOG.debug(
                                                        "Unable to read datasource config from"
                                                                + " ConfigProvider.",
                                                        e);
                                            }
                                        }

                                        if (jdbcUrl != null) {
                                            // Strip "jdbc:" prefix and any query params.
                                            // Quarkus/Vert.x may append driver-specific params
                                            // (e.g. "loggerLevel=OFF") that PostgreSQL rejects
                                            // as unrecognised startup parameters.
                                            String standardUrl = jdbcUrl.replaceFirst("^jdbc:", "");
                                            String baseUrl =
                                                    standardUrl.contains("?")
                                                            ? standardUrl.substring(
                                                                    0, standardUrl.indexOf("?"))
                                                            : standardUrl;

                                            // Embed credentials into the URL
                                            String dbUrl;
                                            if (dbUsername != null && !dbUsername.isBlank()) {
                                                String credentials =
                                                        dbPassword != null && !dbPassword.isBlank()
                                                                ? dbUsername + ":" + dbPassword
                                                                : dbUsername;
                                                dbUrl =
                                                        baseUrl.replaceFirst(
                                                                "//", "//" + credentials + "@");
                                            } else {
                                                dbUrl = baseUrl;
                                            }

                                            // Dev service PG has no TLS; disable SSL so the
                                            // Go pgx driver doesn't attempt a TLS handshake.
                                            String containerDbUrl =
                                                    dbUrl.replace(
                                                                    "localhost",
                                                                    "host.docker.internal")
                                                            + "?sslmode=disable";
                                            LOG.infof(
                                                    "Configuring memory-service container with"
                                                            + " DB URL: %s",
                                                    containerDbUrl);
                                            container.withEnv(
                                                    "MEMORY_SERVICE_DB_URL", containerDbUrl);
                                            container.withEnv("MEMORY_SERVICE_DB_KIND", "postgres");
                                            container.withEnv(
                                                    "MEMORY_SERVICE_DB_MIGRATE_AT_START", "true");
                                        }

                                        // Connect the memory-service container to the Keycloak dev
                                        // service. MEMORY_SERVICE_OIDC_ISSUER must match the "iss"
                                        // claim in JWTs (the external localhost URL); the container
                                        // reaches Keycloak via host.docker.internal. Use
                                        // MEMORY_SERVICE_OIDC_DISCOVERY_URL for the internal
                                        // endpoint.
                                        String authServerUrl = null;
                                        if (keycloakResult.isPresent()) {
                                            authServerUrl =
                                                    keycloakResult
                                                            .get()
                                                            .getConfig()
                                                            .get("quarkus.oidc.auth-server-url");
                                        }
                                        // Fallback: read from live config (hot-reload case)
                                        if (authServerUrl == null) {
                                            try {
                                                authServerUrl =
                                                        ConfigProvider.getConfig()
                                                                .getOptionalValue(
                                                                        "quarkus.oidc.auth-server-url",
                                                                        String.class)
                                                                .orElse(null);
                                            } catch (IllegalStateException e) {
                                                LOG.debug(
                                                        "Unable to read OIDC config from"
                                                                + " ConfigProvider.",
                                                        e);
                                            }
                                        }
                                        if (authServerUrl != null) {
                                            String containerAuthServerUrl =
                                                    authServerUrl.replace(
                                                            "localhost", "host.docker.internal");
                                            LOG.infof(
                                                    "Configuring memory-service container with"
                                                            + " OIDC issuer=%s"
                                                            + " discoveryUrl=%s",
                                                    authServerUrl, containerAuthServerUrl);
                                            // Issuer = external URL (matches JWT "iss" claim)
                                            container.withEnv(
                                                    "MEMORY_SERVICE_OIDC_ISSUER", authServerUrl);
                                            // Discovery URL = internal URL reachable from container
                                            container.withEnv(
                                                    "MEMORY_SERVICE_OIDC_DISCOVERY_URL",
                                                    containerAuthServerUrl);
                                        }

                                        // Configure Redis hosts for the memory-service container
                                        // only if cache.kind is set to "redis".
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
                                                                + " MEMORY_SERVICE_REDIS_HOSTS=%s",
                                                        containerRedisHosts);
                                                container.withEnv(
                                                        "MEMORY_SERVICE_REDIS_HOSTS",
                                                        containerRedisHosts);
                                            } else {
                                                LOG.warn(
                                                        "Redis config not available. Container will"
                                                                + " start without Redis"
                                                                + " configuration.");
                                            }
                                        }

                                        // Configure Infinispan server list for the
                                        // memory-service container only if cache.kind is
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
                                                            + " MEMORY_SERVICE_INFINISPAN_HOST=%s",
                                                        containerServerList);
                                                container.withEnv(
                                                        "MEMORY_SERVICE_INFINISPAN_HOST",
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
                    .getOptionalValue("memory-service.cache.kind", String.class)
                    .filter(kind -> !"none".equals(kind))
                    .orElse(null);
        } catch (IllegalStateException e) {
            LOG.debug("Unable to read cache configuration from config, using default.", e);
            return null;
        }
    }

    private Integer getFixedPortConfig() {
        try {
            return ConfigProvider.getConfig()
                    .getOptionalValue("memory-service.devservices.port", Integer.class)
                    .orElse(null);
        } catch (IllegalStateException e) {
            LOG.debug("Unable to read port configuration from config.", e);
            return null;
        }
    }

    /**
     * Reads additional environment variables from config properties with the prefix
     * "memory-service.devservices.env.". For example:
     * <pre>
     * memory-service.devservices.env."MEMORY_SERVICE_CORS_ENABLED"=true
     * memory-service.devservices.env."MEMORY_SERVICE_CORS_ORIGINS"=http://localhost:3000
     * </pre>
     *
     * @return a map of environment variable names to values
     */
    private Map<String, String> getAdditionalEnvVars() {
        Map<String, String> envVars = new HashMap<>();
        String prefix = "memory-service.devservices.env.";
        try {
            var config = ConfigProvider.getConfig();
            for (String propertyName : config.getPropertyNames()) {
                if (propertyName.startsWith(prefix)) {
                    String envVarName = propertyName.substring(prefix.length());
                    // Remove surrounding quotes if present
                    if (envVarName.startsWith("\"") && envVarName.endsWith("\"")) {
                        envVarName = envVarName.substring(1, envVarName.length() - 1);
                    }
                    String value = config.getOptionalValue(propertyName, String.class).orElse(null);
                    if (value != null) {
                        envVars.put(envVarName, value);
                    }
                }
            }
        } catch (IllegalStateException e) {
            LOG.debug("Unable to read additional env vars from config.", e);
        }
        return envVars;
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
