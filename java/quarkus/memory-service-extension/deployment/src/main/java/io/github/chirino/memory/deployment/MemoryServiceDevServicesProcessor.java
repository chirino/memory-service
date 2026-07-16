package io.github.chirino.memory.deployment;

import io.quarkus.deployment.IsDevServicesSupportedByLaunchMode;
import io.quarkus.deployment.IsDevelopment;
import io.quarkus.deployment.annotations.BuildProducer;
import io.quarkus.deployment.annotations.BuildStep;
import io.quarkus.deployment.builditem.DevServicesResultBuildItem;
import io.quarkus.deployment.builditem.DockerStatusBuildItem;
import io.quarkus.deployment.builditem.LaunchModeBuildItem;
import io.quarkus.deployment.builditem.Startable;
import io.quarkus.deployment.dev.devservices.DevServicesConfig;
import io.quarkus.devservices.common.StartableContainer;
import io.quarkus.devui.spi.page.CardPageBuildItem;
import io.quarkus.devui.spi.page.Page;
import java.io.IOException;
import java.io.InputStream;
import java.security.SecureRandom;
import java.time.Duration;
import java.util.Base64;
import java.util.HashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Optional;
import java.util.Properties;
import java.util.function.Function;
import java.util.function.Supplier;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import org.eclipse.microprofile.config.ConfigProvider;
import org.jboss.logging.Logger;
import org.testcontainers.containers.FixedHostPortGenericContainer;
import org.testcontainers.containers.GenericContainer;
import org.testcontainers.containers.wait.strategy.Wait;
import org.testcontainers.images.PullPolicy;
import org.testcontainers.utility.DockerImageName;

public class MemoryServiceDevServicesProcessor {

    private static final Logger LOG = Logger.getLogger(MemoryServiceDevServicesProcessor.class);
    private static final String FEATURE = "memory-service";
    private static final int MEMORY_SERVICE_PORT = 8080;
    private static final String DEV_SERVICE_LABEL = "quarkus-dev-service-memory-service";
    private static final String IMAGE_NAME_CONFIG_KEY = "memory-service.devservices.image-name";
    private static final String IMAGE_REPOSITORY = "ghcr.io/chirino/memory-service";
    private static final String LATEST_IMAGE = IMAGE_REPOSITORY + ":latest";
    private static final String VERSION_RESOURCE = "/META-INF/memory-service-extension.properties";
    private static final Pattern RELEASE_VERSION =
            Pattern.compile("^(\\d+)\\.(\\d+)\\.\\d+(?:[-.].*)?$");
    private static final String EXTENSION_VERSION = loadExtensionVersion();
    private static final int API_KEY_BYTES = 32;
    private static final String OIDC_AUTH_SERVER_URL_CONFIG_KEY = "quarkus.oidc.auth-server-url";
    private static final SecureRandom SECURE_RANDOM = new SecureRandom();

    @BuildStep(onlyIf = IsDevelopment.class)
    CardPageBuildItem devUiPages(
            Optional<MemoryServiceDevServicesConfigBuildItem> memoryServiceConfig) {
        CardPageBuildItem cardPage = new CardPageBuildItem();
        String memoryServiceUrl =
                memoryServiceConfig
                        .map(MemoryServiceDevServicesConfigBuildItem::getUrl)
                        .filter(url -> !url.isBlank())
                        .orElseGet(MemoryServiceDevServicesProcessor::configuredMemoryServiceUrl);
        if (memoryServiceUrl == null || memoryServiceUrl.isBlank()) {
            return cardPage;
        }

        String developerUrl = developerUrl(memoryServiceUrl);
        cardPage.addPage(
                Page.externalPageBuilder("Developer Console")
                        .url(developerUrl, developerUrl)
                        .isHtmlContent()
                        .doNotEmbed(true)
                        .icon("font-awesome-solid:code"));
        return cardPage;
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

    private static String configuredMemoryServiceUrl() {
        try {
            var config = ConfigProvider.getConfig();
            String configuredUrl =
                    config.getOptionalValue("memory-service.client.url", String.class).orElse(null);
            if (configuredUrl != null && !configuredUrl.isBlank()) {
                return configuredUrl;
            }
            return config.getOptionalValue("memory-service.devservices.port", Integer.class)
                    .map(port -> "http://localhost:" + port)
                    .orElse(null);
        } catch (IllegalStateException e) {
            LOG.debug("Unable to read memory-service.client.url from config.", e);
            return null;
        }
    }

    private static String developerUrl(String memoryServiceUrl) {
        return memoryServiceUrl.replaceAll("/+$", "") + "/developer/";
    }

    @BuildStep(onlyIf = {IsDevServicesSupportedByLaunchMode.class, DevServicesConfig.Enabled.class})
    public void startMemoryServiceDevService(
            LaunchModeBuildItem launchMode,
            DockerStatusBuildItem dockerStatus,
            DevServicesConfig devServicesConfig,
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

        // Read optional fixed port configuration
        final Integer fixedPort = getFixedPortConfig();
        final String configuredImageName = getConfiguredImageName();
        final String imageName = resolveImageName(configuredImageName, EXTENSION_VERSION);
        final boolean usingDefaultImage =
                configuredImageName == null || configuredImageName.isBlank();

        LOG.infof("Starting memory-service dev service using image %s...", imageName);

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
                                                    new FixedHostPortGenericContainer<>(imageName)
                                                            .withExposedPorts(MEMORY_SERVICE_PORT)
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
                                                                            imageName))
                                                            .withExposedPorts(MEMORY_SERVICE_PORT);
                                        }
                                        if (usingDefaultImage) {
                                            // Release defaults use a mutable X.Y compatibility tag,
                                            // while snapshots use mutable latest. Always check the
                                            // registry so a locally cached tag cannot go stale.
                                            container.withImagePullPolicy(PullPolicy.alwaysPull());
                                        }
                                        container
                                                .withEnv(
                                                        "MEMORY_SERVICE_API_KEYS_AGENT",
                                                        effectiveApiKey)
                                                .withEnv("MEMORY_SERVICE_TLS_SELF_SIGNED", "true")
                                                .withEnv("MEMORY_SERVICE_DB_KIND", "sqlite")
                                                .withEnv(
                                                        "MEMORY_SERVICE_DB_URL",
                                                        "file:/tmp/memory-service-dev/memory-service.db")
                                                .withEnv(
                                                        "MEMORY_SERVICE_DB_MIGRATE_AT_START",
                                                        "true")
                                                .withEnv("MEMORY_SERVICE_CACHE_KIND", "local")
                                                .withEnv(
                                                        "MEMORY_SERVICE_DEVELOPER_FRONTEND_ENABLED",
                                                        "true")
                                                .withEnv("MEMORY_SERVICE_VECTOR_KIND", "sqlite")
                                                .withEnv("MEMORY_SERVICE_EMBEDDING_KIND", "local")
                                                .withEnv("MEMORY_SERVICE_ATTACHMENTS_KIND", "fs")
                                                .withEnv(
                                                        "MEMORY_SERVICE_ATTACHMENTS_FS_DIR",
                                                        "/tmp/memory-service-dev/attachments")
                                                .withEnv(
                                                        "MEMORY_SERVICE_TEMP_DIR",
                                                        "/tmp/memory-service-dev/tmp")
                                                .withLabel(DEV_SERVICE_LABEL, "memory-service");

                                        if (fixedPort != null) {
                                            container.withEnv(
                                                    "MEMORY_SERVICE_BASE_URL",
                                                    "http://localhost:" + fixedPort);
                                        }

                                        // Apply additional environment variables from config
                                        for (Map.Entry<String, String> entry :
                                                additionalEnvVars.entrySet()) {
                                            container.withEnv(entry.getKey(), entry.getValue());
                                            LOG.debugf(
                                                    "Setting container env: %s=%s",
                                                    entry.getKey(), entry.getValue());
                                        }

                                        // Connect the memory-service container to the Keycloak dev
                                        // service. MEMORY_SERVICE_OIDC_ISSUER must match the "iss"
                                        // claim in JWTs (the external localhost URL); the container
                                        // reaches Keycloak via host.docker.internal. Use
                                        // MEMORY_SERVICE_OIDC_DISCOVERY_URL for the internal
                                        // endpoint. Quarkus 3.35 exposes Keycloak Dev Services
                                        // config through DevServicesResultBuildItem dependency
                                        // injection below; this live-config read covers explicit
                                        // user config and hot-reload reuse.
                                        configureKeycloakDevService(
                                                container, findConfiguredAuthServerUrl());

                                        // Wait for the Go service's /ready endpoint to return 200
                                        // (all initialization complete: migrations, connections,
                                        // listeners). Avoids log-scraping and works with nc-less
                                        // images.
                                        container.waitingFor(
                                                Wait.forHttp("/ready")
                                                        .forPort(MEMORY_SERVICE_PORT)
                                                        .forStatusCode(200)
                                                        .withStartupTimeout(Duration.ofMinutes(2)));

                                        StartableContainer<GenericContainer<?>> startable =
                                                new StartableContainer<>(
                                                        container,
                                                        c -> {
                                                            String url =
                                                                    "http://"
                                                                            + c.getHost()
                                                                            + ":"
                                                                            + c.getMappedPort(
                                                                                    MEMORY_SERVICE_PORT);
                                                            LOG.infof(
                                                                    "Dev Services for"
                                                                        + " memory-service started."
                                                                        + " The service is"
                                                                        + " available at %s",
                                                                    url);
                                                            return url;
                                                        });
                                        return startable;
                                    }
                                })
                        .configProvider(configProviders)
                        .dependsOnConfig(
                                OIDC_AUTH_SERVER_URL_CONFIG_KEY,
                                MemoryServiceDevServicesProcessor::configureKeycloakDevService,
                                true)
                        .build());
    }

    static String resolveImageName(String configuredImageName, String extensionVersion) {
        if (configuredImageName != null && !configuredImageName.isBlank()) {
            return configuredImageName.trim();
        }
        if (extensionVersion == null
                || extensionVersion.isBlank()
                || extensionVersion.toUpperCase(Locale.ROOT).endsWith("-SNAPSHOT")) {
            return LATEST_IMAGE;
        }

        Matcher matcher = RELEASE_VERSION.matcher(extensionVersion.trim());
        if (!matcher.matches()) {
            LOG.warnf(
                    "Unable to derive a compatibility image tag from extension version %s; using"
                            + " latest",
                    extensionVersion);
            return LATEST_IMAGE;
        }
        return IMAGE_REPOSITORY + ":" + matcher.group(1) + "." + matcher.group(2);
    }

    private static String getConfiguredImageName() {
        try {
            return ConfigProvider.getConfig()
                    .getOptionalValue(IMAGE_NAME_CONFIG_KEY, String.class)
                    .orElse(null);
        } catch (IllegalStateException e) {
            LOG.debugf(e, "Unable to read %s from config.", IMAGE_NAME_CONFIG_KEY);
            return null;
        }
    }

    static String loadExtensionVersion() {
        try (InputStream input =
                MemoryServiceDevServicesProcessor.class.getResourceAsStream(VERSION_RESOURCE)) {
            if (input == null) {
                LOG.warnf("Unable to load %s; Dev Services will use latest", VERSION_RESOURCE);
                return null;
            }
            Properties properties = new Properties();
            properties.load(input);
            return properties.getProperty("version");
        } catch (IOException e) {
            LOG.warnf(e, "Unable to load %s; Dev Services will use latest", VERSION_RESOURCE);
            return null;
        }
    }

    private static String findConfiguredAuthServerUrl() {
        try {
            return ConfigProvider.getConfig()
                    .getOptionalValue(OIDC_AUTH_SERVER_URL_CONFIG_KEY, String.class)
                    .orElse(null);
        } catch (IllegalStateException e) {
            LOG.debug("Unable to read OIDC config from ConfigProvider.", e);
            return null;
        }
    }

    private static void configureKeycloakDevService(Startable startable, String authServerUrl) {
        if (startable instanceof StartableContainer<?> container) {
            configureKeycloakDevService(container.getContainer(), authServerUrl);
        }
    }

    private static void configureKeycloakDevService(
            GenericContainer<?> container, String authServerUrl) {
        if (authServerUrl == null || authServerUrl.isBlank()) {
            return;
        }
        String containerAuthServerUrl = hostReachableUrl(authServerUrl);
        LOG.infof(
                "Configuring memory-service container with OIDC issuer=%s discoveryUrl=%s",
                authServerUrl, containerAuthServerUrl);
        // Issuer = external URL (matches JWT "iss" claim)
        container.withEnv("MEMORY_SERVICE_OIDC_ISSUER", authServerUrl);
        // Discovery URL = internal URL reachable from container
        container.withEnv("MEMORY_SERVICE_OIDC_DISCOVERY_URL", containerAuthServerUrl);
    }

    private static String hostReachableUrl(String url) {
        return url.replace("localhost", "host.docker.internal")
                .replace("127.0.0.1", "host.docker.internal")
                .replace("0.0.0.0", "host.docker.internal");
    }

    private String generateRandomApiKey() {
        byte[] bytes = new byte[API_KEY_BYTES];
        SECURE_RANDOM.nextBytes(bytes);
        return Base64.getUrlEncoder().withoutPadding().encodeToString(bytes);
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
}
