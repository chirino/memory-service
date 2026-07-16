package io.github.chirino.memoryservice.process;

import java.io.BufferedReader;
import java.io.IOException;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.net.HttpURLConnection;
import java.net.StandardProtocolFamily;
import java.net.URI;
import java.net.URISyntaxException;
import java.net.UnixDomainSocketAddress;
import java.nio.ByteBuffer;
import java.nio.channels.SocketChannel;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.LinkOption;
import java.nio.file.Path;
import java.nio.file.attribute.PosixFilePermission;
import java.nio.file.attribute.PosixFilePermissions;
import java.time.Duration;
import java.util.ArrayDeque;
import java.util.Arrays;
import java.util.Deque;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Objects;
import java.util.Optional;
import java.util.Set;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.function.Consumer;
import java.util.logging.Level;
import java.util.logging.Logger;

/**
 * Owns a locally launched Memory Service subprocess.
 *
 * <p>The default builder configures SQLite, a protected Unix domain socket, local socket identity,
 * no embedding provider, and explicit plain local encryption through {@code MEMORY_SERVICE_*}
 * environment variables. Startup waits for the service to report ready when the effective
 * configuration exposes a discoverable management endpoint. Closing sends a graceful termination
 * request and then forcibly terminates the child if it exceeds the configured timeout.
 */
public final class MemoryServiceProcess implements AutoCloseable {

    private static final Logger LOG = Logger.getLogger(MemoryServiceProcess.class.getName());
    private static final Duration PROBE_TIMEOUT = Duration.ofMillis(200);
    private static final int RECENT_OUTPUT_LIMIT = 50;

    private final Process process;
    private final MemoryServiceBinary binary;
    private final Path socketPath;
    private final String target;
    private final ReadyEndpoint readinessEndpoint;
    private final Duration shutdownTimeout;
    private final Thread shutdownHook;
    private final AtomicBoolean closed = new AtomicBoolean();
    private final Deque<String> recentOutput = new ArrayDeque<>();

    private MemoryServiceProcess(
            Process process,
            MemoryServiceBinary binary,
            ReadyEndpoint clientEndpoint,
            ReadyEndpoint readinessEndpoint,
            Duration shutdownTimeout,
            boolean installShutdownHook,
            Consumer<String> standardOutput,
            Consumer<String> standardError,
            boolean errorStreamMerged) {
        this.process = process;
        this.binary = binary;
        this.socketPath =
                clientEndpoint instanceof UnixReadyEndpoint unix ? unix.socketPath() : null;
        this.target = clientEndpoint.target();
        this.readinessEndpoint = readinessEndpoint;
        this.shutdownTimeout = shutdownTimeout;
        if (standardOutput != null) {
            bridge(
                    process.getInputStream(),
                    errorStreamMerged ? "output" : "stdout",
                    standardOutput);
        }
        if (standardError != null && !errorStreamMerged) {
            bridge(process.getErrorStream(), "stderr", standardError);
        }
        if (installShutdownHook) {
            this.shutdownHook =
                    Thread.ofPlatform()
                            .name("memory-service-shutdown-" + process.pid())
                            .unstarted(this::terminateQuietly);
            Runtime.getRuntime().addShutdownHook(shutdownHook);
        } else {
            this.shutdownHook = null;
        }
    }

    /** Creates an opinionated local-server builder rooted in {@code stateDirectory}. */
    public static Builder builder(Path stateDirectory) {
        return new Builder(stateDirectory);
    }

    /** The resolved executable used to launch this process. */
    public MemoryServiceBinary binary() {
        return binary;
    }

    /**
     * Unix socket used by the local HTTP and gRPC listener.
     *
     * @throws IllegalStateException when the process uses an HTTP listener
     */
    public Path socketPath() {
        if (socketPath == null) {
            throw new IllegalStateException("Memory Service process does not use a Unix socket");
        }
        return socketPath;
    }

    /** Unix socket when the process uses one. */
    public Optional<Path> unixSocketPath() {
        return Optional.ofNullable(socketPath);
    }

    /** Client target in {@code unix:///path}, {@code http://host:port}, or HTTPS form. */
    public String target() {
        return target;
    }

    /** Native child-process handle. */
    public ProcessHandle processHandle() {
        return process.toHandle();
    }

    /** Whether the child process is currently alive. */
    public boolean isAlive() {
        return process.isAlive();
    }

    @Override
    public void close() {
        if (!closed.compareAndSet(false, true)) {
            return;
        }
        terminateProcess();
        removeShutdownHook();
    }

    private void awaitReady(Duration startupTimeout) {
        long deadline = System.nanoTime() + startupTimeout.toNanos();
        Throwable lastError = null;
        ExecutorService executor = Executors.newVirtualThreadPerTaskExecutor();
        try {
            while (System.nanoTime() < deadline) {
                if (!process.isAlive()) {
                    throw startFailure(
                            "Memory Service exited with code "
                                    + process.exitValue()
                                    + " before becoming ready",
                            lastError);
                }
                Future<Boolean> probe = executor.submit(readinessEndpoint::probeReady);
                try {
                    if (probe.get(PROBE_TIMEOUT.toMillis(), TimeUnit.MILLISECONDS)) {
                        return;
                    }
                } catch (TimeoutException e) {
                    probe.cancel(true);
                    lastError = e;
                } catch (ExecutionException e) {
                    lastError = e.getCause();
                } catch (InterruptedException e) {
                    Thread.currentThread().interrupt();
                    throw startFailure("Interrupted while waiting for Memory Service readiness", e);
                }
                try {
                    Thread.sleep(PROBE_TIMEOUT);
                } catch (InterruptedException e) {
                    Thread.currentThread().interrupt();
                    throw startFailure("Interrupted while waiting for Memory Service readiness", e);
                }
            }
            throw startFailure(
                    "Memory Service did not become ready within " + startupTimeout, lastError);
        } finally {
            executor.shutdownNow();
        }
    }

    private MemoryServiceStartException startFailure(String message, Throwable cause) {
        StringBuilder full = new StringBuilder(message);
        synchronized (recentOutput) {
            if (!recentOutput.isEmpty()) {
                full.append(". Recent output:\n");
                recentOutput.forEach(line -> full.append(line).append('\n'));
            }
        }
        return cause == null
                ? new MemoryServiceStartException(full.toString())
                : new MemoryServiceStartException(full.toString(), cause);
    }

    private void bridge(InputStream stream, String name, Consumer<String> consumer) {
        Thread.ofPlatform()
                .daemon(true)
                .name("memory-service-" + name + "-" + process.pid())
                .start(
                        () -> {
                            try (BufferedReader reader =
                                    new BufferedReader(
                                            new InputStreamReader(
                                                    stream, StandardCharsets.UTF_8))) {
                                String line;
                                while ((line = reader.readLine()) != null) {
                                    remember("[" + name + "] " + line);
                                    try {
                                        consumer.accept(line);
                                    } catch (RuntimeException ignored) {
                                        LOG.log(
                                                Level.FINE,
                                                "Memory Service output consumer failed",
                                                ignored);
                                    }
                                }
                            } catch (IOException e) {
                                if (process.isAlive()) {
                                    LOG.log(
                                            Level.FINE,
                                            "Memory Service " + name + " bridge failed",
                                            e);
                                }
                            }
                        });
    }

    private void remember(String line) {
        synchronized (recentOutput) {
            while (recentOutput.size() >= RECENT_OUTPUT_LIMIT) {
                recentOutput.removeFirst();
            }
            recentOutput.addLast(line);
        }
    }

    private sealed interface ReadyEndpoint permits UnixReadyEndpoint, HttpReadyEndpoint {

        String target();

        boolean probeReady() throws IOException;
    }

    private record UnixReadyEndpoint(Path socketPath) implements ReadyEndpoint {

        @Override
        public String target() {
            return "unix://" + socketPath;
        }

        @Override
        public boolean probeReady() throws IOException {
            try (SocketChannel channel = SocketChannel.open(StandardProtocolFamily.UNIX)) {
                channel.connect(UnixDomainSocketAddress.of(socketPath));
                ByteBuffer request =
                        StandardCharsets.US_ASCII.encode(
                                "GET /ready HTTP/1.1\r\n"
                                        + "Host: localhost\r\n"
                                        + "Connection: close\r\n\r\n");
                while (request.hasRemaining()) {
                    channel.write(request);
                }
                ByteBuffer response = ByteBuffer.allocate(4096);
                int count = channel.read(response);
                if (count <= 0) {
                    return false;
                }
                response.flip();
                String text = StandardCharsets.US_ASCII.decode(response).toString();
                return text.startsWith("HTTP/1.1 200") || text.startsWith("HTTP/1.0 200");
            }
        }
    }

    private record HttpReadyEndpoint(String scheme, String host, int port)
            implements ReadyEndpoint {

        @Override
        public String target() {
            try {
                return new URI(scheme, null, host, port, null, null, null).toString();
            } catch (URISyntaxException e) {
                throw new IllegalArgumentException("Invalid HTTP listener host: " + host, e);
            }
        }

        @Override
        public boolean probeReady() throws IOException {
            HttpURLConnection connection =
                    (HttpURLConnection) URI.create(target() + "/ready").toURL().openConnection();
            connection.setConnectTimeout((int) PROBE_TIMEOUT.toMillis());
            connection.setReadTimeout((int) PROBE_TIMEOUT.toMillis());
            connection.setRequestMethod("GET");
            connection.setUseCaches(false);
            try {
                return connection.getResponseCode() == HttpURLConnection.HTTP_OK;
            } finally {
                connection.disconnect();
            }
        }
    }

    private void terminateQuietly() {
        if (closed.compareAndSet(false, true)) {
            terminateProcess();
        }
    }

    private void terminateProcess() {
        terminateProcess(process, shutdownTimeout);
    }

    static void terminateProcess(Process process, Duration shutdownTimeout) {
        if (!process.isAlive()) {
            return;
        }
        process.destroy();
        try {
            if (!process.waitFor(shutdownTimeout.toMillis(), TimeUnit.MILLISECONDS)) {
                process.destroyForcibly();
                process.waitFor(shutdownTimeout.toMillis(), TimeUnit.MILLISECONDS);
            }
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            process.destroyForcibly();
        }
    }

    private void removeShutdownHook() {
        if (shutdownHook == null || Thread.currentThread() == shutdownHook) {
            return;
        }
        try {
            Runtime.getRuntime().removeShutdownHook(shutdownHook);
        } catch (IllegalStateException ignored) {
            // JVM shutdown is already in progress.
        }
    }

    static void configureProcessEnvironment(
            Map<String, String> childEnvironment,
            Map<String, String> defaultEnvironment,
            Set<String> environmentRemovals,
            Map<String, String> environmentOverrides,
            boolean inheritMemoryServiceEnvironment) {
        if (inheritMemoryServiceEnvironment) {
            defaultEnvironment.forEach(childEnvironment::putIfAbsent);
        } else {
            childEnvironment.keySet().removeIf(name -> name.startsWith("MEMORY_SERVICE_"));
            childEnvironment.putAll(defaultEnvironment);
        }
        environmentRemovals.forEach(childEnvironment::remove);
        childEnvironment.putAll(environmentOverrides);
    }

    /** Builder for one owned local Memory Service process. */
    public static final class Builder {

        private static final String DB_URL = "MEMORY_SERVICE_DB_URL";
        private static final String UNIX_SOCKET = "MEMORY_SERVICE_UNIX_SOCKET";
        private static final String UNIX_SOCKET_AUTH = "MEMORY_SERVICE_UNIX_SOCKET_AUTH";
        private static final String HOST = "MEMORY_SERVICE_HOST";
        private static final String PORT = "MEMORY_SERVICE_PORT";
        private static final String PLAIN_TEXT = "MEMORY_SERVICE_PLAIN_TEXT";
        private static final String TLS = "MEMORY_SERVICE_TLS";
        private static final String MANAGEMENT_ON_MAIN =
                "MEMORY_SERVICE_MANAGEMENT_ON_MAIN_LISTENER";
        private static final String MANAGEMENT_UNIX_SOCKET =
                "MEMORY_SERVICE_MANAGEMENT_UNIX_SOCKET";
        private static final String MANAGEMENT_HOST = "MEMORY_SERVICE_MANAGEMENT_HOST";
        private static final String MANAGEMENT_PORT = "MEMORY_SERVICE_MANAGEMENT_PORT";
        private static final String MANAGEMENT_PLAIN_TEXT = "MEMORY_SERVICE_MANAGEMENT_PLAIN_TEXT";
        private static final String MANAGEMENT_TLS = "MEMORY_SERVICE_MANAGEMENT_TLS";
        private static final Consumer<String> DEFAULT_OUTPUT =
                line -> LOG.fine("[memory-service] " + line);

        private final Path stateDirectory;
        private Path cacheDirectory = MemoryServiceBinary.defaultCacheDirectory();
        private Duration startupTimeout = Duration.ofSeconds(30);
        private Duration shutdownTimeout = Duration.ofSeconds(5);
        private boolean installShutdownHook = true;
        private boolean inheritMemoryServiceEnvironment;
        private List<MemoryServiceBinary.Resolver> binaryResolvers =
                List.of(MemoryServiceBinary.packaged());
        private final Map<String, String> defaultEnvironment = new LinkedHashMap<>();
        private final Set<String> environmentRemovals = new LinkedHashSet<>();
        private final Map<String, String> environmentOverrides = new LinkedHashMap<>();
        private Consumer<String> standardOutput = DEFAULT_OUTPUT;
        private Consumer<String> standardError = DEFAULT_OUTPUT;

        private Builder(Path stateDirectory) {
            this.stateDirectory =
                    Objects.requireNonNull(stateDirectory, "stateDirectory")
                            .toAbsolutePath()
                            .normalize();
            defaultEnvironment.put(PLAIN_TEXT, "true");
            defaultEnvironment.put(TLS, "false");
            defaultEnvironment.put("MEMORY_SERVICE_DB_KIND", "sqlite");
            defaultEnvironment.put(DB_URL, this.stateDirectory.resolve("memory.db").toString());
            defaultEnvironment.put(
                    UNIX_SOCKET, this.stateDirectory.resolve("memory.sock").toString());
            defaultEnvironment.put(UNIX_SOCKET_AUTH, "local");
            defaultEnvironment.put("MEMORY_SERVICE_EMBEDDING_KIND", "none");
            defaultEnvironment.put("MEMORY_SERVICE_ENCRYPTION_ALLOW_PLAIN", "true");
            defaultEnvironment.put(MANAGEMENT_ON_MAIN, "true");
        }

        /** Replaces the ordered executable resolver list. */
        public Builder binaryResolvers(MemoryServiceBinary.Resolver... resolvers) {
            return binaryResolvers(Arrays.asList(resolvers));
        }

        /** Replaces the ordered executable resolver list. */
        public Builder binaryResolvers(List<MemoryServiceBinary.Resolver> resolvers) {
            Objects.requireNonNull(resolvers, "resolvers");
            if (resolvers.isEmpty() || resolvers.stream().anyMatch(Objects::isNull)) {
                throw new IllegalArgumentException(
                        "binaryResolvers must not be empty or contain null");
            }
            this.binaryResolvers = List.copyOf(resolvers);
            return this;
        }

        /** Overrides the packaged-binary digest cache. */
        public Builder cacheDirectory(Path cacheDirectory) {
            this.cacheDirectory = Objects.requireNonNull(cacheDirectory, "cacheDirectory");
            return this;
        }

        /** Overrides the SQLite database path. */
        public Builder databasePath(Path databasePath) {
            return environment(
                    DB_URL,
                    Objects.requireNonNull(databasePath, "databasePath")
                            .toAbsolutePath()
                            .normalize()
                            .toString());
        }

        /** Overrides the Unix socket path. */
        public Builder socketPath(Path socketPath) {
            removeEnvironment(HOST);
            removeEnvironment(PORT);
            environmentRemovals.remove(UNIX_SOCKET_AUTH);
            return environment(
                    UNIX_SOCKET,
                    Objects.requireNonNull(socketPath, "socketPath")
                            .toAbsolutePath()
                            .normalize()
                            .toString());
        }

        /** Configures the main listener as a Unix domain socket. */
        public Builder unixSocketListener(Path socketPath) {
            return socketPath(socketPath);
        }

        /**
         * Disables the default Unix domain socket so the server's main HTTP listener configuration
         * is used. Callers must configure normal API-key or OIDC authentication for TCP access.
         */
        public Builder disableUnixSocket() {
            removeEnvironment(UNIX_SOCKET);
            removeEnvironment(UNIX_SOCKET_AUTH);
            return this;
        }

        /**
         * Configures a plaintext main HTTP listener on loopback. Callers must also configure normal
         * API-key or OIDC authentication.
         */
        public Builder httpListener(int port) {
            return httpListener("127.0.0.1", port);
        }

        /**
         * Configures a plaintext main HTTP listener. Callers must also configure normal API-key or
         * OIDC authentication. Non-loopback hosts require the caller to explicitly configure
         * {@code MEMORY_SERVICE_ALLOW_NON_LOOPBACK_PLAINTEXT=true}.
         */
        public Builder httpListener(String host, int port) {
            disableUnixSocket();
            environment(HOST, validHost(host, "host"));
            environment(PORT, Integer.toString(validPort(port, "port")));
            environment(PLAIN_TEXT, "true");
            environment(TLS, "false");
            return this;
        }

        /** Configures a plaintext dedicated management HTTP listener on loopback. */
        public Builder managementHttpListener(int port) {
            return managementHttpListener("127.0.0.1", port);
        }

        /**
         * Configures a plaintext dedicated management HTTP listener. Non-loopback hosts also
         * require {@code MEMORY_SERVICE_MANAGEMENT_ALLOW_NON_LOOPBACK=true}.
         */
        public Builder managementHttpListener(String host, int port) {
            removeEnvironment(MANAGEMENT_UNIX_SOCKET);
            environment(MANAGEMENT_ON_MAIN, "false");
            environment(MANAGEMENT_HOST, validHost(host, "management host"));
            environment(MANAGEMENT_PORT, Integer.toString(validPort(port, "management port")));
            environment(MANAGEMENT_PLAIN_TEXT, "true");
            environment(MANAGEMENT_TLS, "false");
            return this;
        }

        /** Sets the maximum readiness wait. */
        public Builder startupTimeout(Duration startupTimeout) {
            this.startupTimeout = positive(startupTimeout, "startupTimeout");
            return this;
        }

        /** Sets the graceful shutdown wait before forced termination. */
        public Builder shutdownTimeout(Duration shutdownTimeout) {
            this.shutdownTimeout = positive(shutdownTimeout, "shutdownTimeout");
            return this;
        }

        /** Enables or disables the JVM shutdown hook. */
        public Builder installShutdownHook(boolean installShutdownHook) {
            this.installShutdownHook = installShutdownHook;
            return this;
        }

        /** Adds or replaces a child-process environment entry. */
        public Builder environment(String name, String value) {
            String key = Objects.requireNonNull(name, "name").trim();
            if (key.isEmpty()) {
                throw new IllegalArgumentException("environment name must not be blank");
            }
            environmentRemovals.remove(key);
            environmentOverrides.put(key, Objects.requireNonNull(value, "value"));
            return this;
        }

        /** Adds or replaces child-process environment entries. */
        public Builder environment(Map<String, String> entries) {
            Objects.requireNonNull(entries, "entries").forEach(this::environment);
            return this;
        }

        /** Removes an inherited or default child-process environment entry. */
        public Builder removeEnvironment(String name) {
            String key = Objects.requireNonNull(name, "name").trim();
            if (key.isEmpty()) {
                throw new IllegalArgumentException("environment name must not be blank");
            }
            environmentOverrides.remove(key);
            environmentRemovals.add(key);
            return this;
        }

        /**
         * Controls whether inherited {@code MEMORY_SERVICE_*} values can replace local defaults.
         * Explicit builder environment entries always take precedence.
         */
        public Builder inheritMemoryServiceEnvironment(boolean inherit) {
            this.inheritMemoryServiceEnvironment = inherit;
            return this;
        }

        /**
         * Receives unprefixed stdout lines, or discards stdout when {@code null}. Using the same
         * non-null consumer instance for stdout and stderr merges both into one bridge.
         */
        public Builder standardOutput(Consumer<String> consumer) {
            this.standardOutput = consumer;
            return this;
        }

        /**
         * Receives unprefixed stderr lines, or discards stderr when {@code null}. Using the same
         * non-null consumer instance for stdout and stderr merges both into one bridge.
         */
        public Builder standardError(Consumer<String> consumer) {
            this.standardError = consumer;
            return this;
        }

        /**
         * Resolves and starts a local Memory Service, waiting for readiness when the effective
         * configuration exposes a discoverable management endpoint.
         */
        public MemoryServiceProcess start() {
            MemoryServiceBinary binary;
            try {
                binary = MemoryServiceBinary.resolve(binaryResolvers, cacheDirectory);
            } catch (IOException e) {
                throw new MemoryServiceStartException("Could not resolve Memory Service binary", e);
            }

            ProcessBuilder processBuilder =
                    new ProcessBuilder(binary.executable().toString(), "serve")
                            .redirectErrorStream(false);
            configureProcessEnvironment(
                    processBuilder.environment(),
                    defaultEnvironment,
                    environmentRemovals,
                    environmentOverrides,
                    inheritMemoryServiceEnvironment);
            Map<String, String> effectiveEnvironment = processBuilder.environment();
            ReadyEndpoint clientEndpoint = mainEndpoint(effectiveEnvironment);
            ReadyEndpoint readinessEndpoint =
                    readinessEndpoint(effectiveEnvironment, clientEndpoint);
            Path databaseDirectory = databaseDirectory(effectiveEnvironment.get(DB_URL));
            prepareStateDirectories(databaseDirectory, clientEndpoint, readinessEndpoint);
            boolean errorStreamMerged = standardOutput != null && standardOutput == standardError;
            processBuilder.redirectErrorStream(errorStreamMerged);
            if (standardOutput == null) {
                processBuilder.redirectOutput(ProcessBuilder.Redirect.DISCARD);
            }
            if (standardError == null && !errorStreamMerged) {
                processBuilder.redirectError(ProcessBuilder.Redirect.DISCARD);
            }

            Process child;
            try {
                child = processBuilder.start();
            } catch (IOException e) {
                throw new MemoryServiceStartException(
                        "Could not launch Memory Service from " + binary.executable(), e);
            }

            MemoryServiceProcess result =
                    new MemoryServiceProcess(
                            child,
                            binary,
                            clientEndpoint,
                            readinessEndpoint,
                            shutdownTimeout,
                            installShutdownHook,
                            standardOutput,
                            standardError,
                            errorStreamMerged);
            try {
                if (readinessEndpoint == null) {
                    LOG.fine(
                            "Memory Service management endpoint is not configured; skipping"
                                    + " readiness wait");
                } else {
                    result.awaitReady(startupTimeout);
                }
                return result;
            } catch (RuntimeException e) {
                result.close();
                throw e;
            }
        }

        private void prepareStateDirectories(Path databaseDirectory, ReadyEndpoint... endpoints) {
            try {
                prepareSecureDirectory(stateDirectory);
                if (databaseDirectory != null) {
                    Files.createDirectories(databaseDirectory);
                }
                Set<Path> socketDirectories = new LinkedHashSet<>();
                for (ReadyEndpoint endpoint : endpoints) {
                    if (endpoint instanceof UnixReadyEndpoint unix) {
                        socketDirectories.add(unix.socketPath().getParent());
                    }
                }
                for (Path socketDirectory : socketDirectories) {
                    prepareSecureDirectory(
                            Objects.requireNonNull(socketDirectory, "socketDirectory"));
                }
            } catch (IOException e) {
                throw new MemoryServiceStartException(
                        "Could not prepare Memory Service state directory " + stateDirectory, e);
            }
        }

        private static ReadyEndpoint mainEndpoint(Map<String, String> environment) {
            ReadyEndpoint unix = unixEndpoint(environment.get(UNIX_SOCKET), UNIX_SOCKET);
            if (unix != null) {
                return unix;
            }
            String scheme =
                    listenerScheme(environment, PLAIN_TEXT, TLS, true, true)
                            .orElseThrow(
                                    () ->
                                            new MemoryServiceStartException(
                                                    "Main HTTP listener must enable exactly one of"
                                                            + " plaintext or TLS"));
            int port = configuredPort(environment.get(PORT), 8080);
            if (port <= 0) {
                throw new MemoryServiceStartException(
                        "MEMORY_SERVICE_PORT must be between 1 and 65535 for a managed HTTP"
                                + " listener");
            }
            return new HttpReadyEndpoint(scheme, configuredHost(environment.get(HOST)), port);
        }

        private static ReadyEndpoint readinessEndpoint(
                Map<String, String> environment, ReadyEndpoint mainEndpoint) {
            if (booleanValue(environment, MANAGEMENT_ON_MAIN, false)) {
                if (mainEndpoint instanceof UnixReadyEndpoint
                        && !booleanValue(environment, PLAIN_TEXT, true)) {
                    return null;
                }
                return mainEndpoint;
            }

            ReadyEndpoint managementUnix =
                    unixEndpoint(environment.get(MANAGEMENT_UNIX_SOCKET), MANAGEMENT_UNIX_SOCKET);
            if (managementUnix != null) {
                return booleanValue(environment, MANAGEMENT_PLAIN_TEXT, true)
                        ? managementUnix
                        : null;
            }

            String configuredManagementPort = environment.get(MANAGEMENT_PORT);
            if (configuredManagementPort == null || configuredManagementPort.isBlank()) {
                return null;
            }
            int port = configuredPort(configuredManagementPort, -1);
            if (port <= 0) {
                return null;
            }
            Optional<String> scheme =
                    listenerScheme(environment, MANAGEMENT_PLAIN_TEXT, MANAGEMENT_TLS, true, true);
            if (scheme.isEmpty()) {
                return null;
            }
            return new HttpReadyEndpoint(
                    scheme.get(), configuredHost(environment.get(MANAGEMENT_HOST)), port);
        }

        private static String configuredHost(String value) {
            return value == null || value.isBlank() ? "127.0.0.1" : value.trim();
        }

        private static ReadyEndpoint unixEndpoint(String value, String name) {
            if (value == null || value.isBlank()) {
                return null;
            }
            Path path = Path.of(value).normalize();
            if (!path.isAbsolute()) {
                throw new MemoryServiceStartException(name + " must be an absolute path: " + value);
            }
            return new UnixReadyEndpoint(path);
        }

        private static Optional<String> listenerScheme(
                Map<String, String> environment,
                String plainTextName,
                String tlsName,
                boolean plainTextDefault,
                boolean tlsDefault) {
            boolean plainText = booleanValue(environment, plainTextName, plainTextDefault);
            boolean tls = booleanValue(environment, tlsName, tlsDefault);
            if (plainText == tls) {
                return Optional.empty();
            }
            return Optional.of(tls ? "https" : "http");
        }

        private static boolean booleanValue(
                Map<String, String> environment, String name, boolean defaultValue) {
            String value = environment.get(name);
            return value == null || value.isBlank() ? defaultValue : Boolean.parseBoolean(value);
        }

        private static int configuredPort(String value, int defaultValue) {
            if (value == null || value.isBlank()) {
                return defaultValue;
            }
            try {
                int port = Integer.parseInt(value);
                return port <= 65535 ? port : -1;
            } catch (NumberFormatException ignored) {
                return -1;
            }
        }

        private static Path databaseDirectory(String value) {
            if (value == null || value.isBlank()) {
                return null;
            }
            try {
                Path path = Path.of(value).normalize();
                return path.isAbsolute() ? path.getParent() : null;
            } catch (RuntimeException ignored) {
                return null;
            }
        }

        private static String validHost(String value, String name) {
            String host = Objects.requireNonNull(value, name).trim();
            if (host.isEmpty()) {
                throw new IllegalArgumentException(name + " must not be blank");
            }
            return host;
        }

        private static int validPort(int value, String name) {
            if (value <= 0 || value > 65535) {
                throw new IllegalArgumentException(name + " must be between 1 and 65535");
            }
            return value;
        }

        private static void prepareSecureDirectory(Path directory) throws IOException {
            boolean existed = Files.exists(directory, LinkOption.NOFOLLOW_LINKS);
            if (existed && !Files.isDirectory(directory, LinkOption.NOFOLLOW_LINKS)) {
                throw new IOException("Path is not a directory: " + directory);
            }
            Files.createDirectories(directory);
            try {
                if (!existed) {
                    Files.setPosixFilePermissions(
                            directory, PosixFilePermissions.fromString("rwx------"));
                }
                Set<PosixFilePermission> permissions = Files.getPosixFilePermissions(directory);
                boolean exposed =
                        permissions.stream()
                                .anyMatch(
                                        permission ->
                                                permission.name().startsWith("GROUP_")
                                                        || permission.name().startsWith("OTHERS_"));
                if (exposed) {
                    throw new IOException("Directory is not owner-only: " + directory);
                }
            } catch (UnsupportedOperationException ignored) {
                // Current packaged platforms are POSIX; retain portability for explicit binaries.
            }
        }

        private static Duration positive(Duration value, String name) {
            Objects.requireNonNull(value, name);
            if (value.isZero() || value.isNegative()) {
                throw new IllegalArgumentException(name + " must be positive");
            }
            return value;
        }
    }
}
