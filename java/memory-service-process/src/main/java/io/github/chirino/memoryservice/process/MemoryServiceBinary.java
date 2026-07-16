package io.github.chirino.memoryservice.process;

import java.io.File;
import java.io.IOException;
import java.io.InputStream;
import java.nio.channels.FileChannel;
import java.nio.channels.FileLock;
import java.nio.file.AtomicMoveNotSupportedException;
import java.nio.file.Files;
import java.nio.file.LinkOption;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.nio.file.StandardOpenOption;
import java.nio.file.attribute.PosixFilePermissions;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.util.ArrayList;
import java.util.HexFormat;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Objects;
import java.util.Optional;
import java.util.ServiceLoader;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ConcurrentMap;

/** A resolved executable and factories for ordered binary-resolution strategies. */
public final class MemoryServiceBinary {

    private static final ConcurrentMap<Path, Object> JVM_EXTRACTION_LOCKS =
            new ConcurrentHashMap<>();

    /** Marker interface for a binary resolution strategy. */
    public sealed interface Resolver permits PackagedResolver, FileResolver, PathResolver {}

    private record PackagedResolver() implements Resolver {}

    private record FileResolver(Path path) implements Resolver {}

    private record PathResolver(String command) implements Resolver {}

    private final Path executable;
    private final MemoryServicePlatform platform;
    private final String source;

    private MemoryServiceBinary(Path executable, MemoryServicePlatform platform, String source) {
        this.executable = executable;
        this.platform = platform;
        this.source = source;
    }

    /** Absolute executable path. */
    public Path executable() {
        return executable;
    }

    /** Platform detected when this binary was resolved. */
    public MemoryServicePlatform platform() {
        return platform;
    }

    /** Human-readable description of the selected source. */
    public String source() {
        return source;
    }

    /** Resolves a matching provider from the application classpath. */
    public static Resolver packaged() {
        return new PackagedResolver();
    }

    /** Resolves an explicit executable file when it exists. */
    public static Resolver file(Path executable) {
        return new FileResolver(Objects.requireNonNull(executable, "executable"));
    }

    /** Resolves a command by searching the process {@code PATH}. */
    public static Resolver onPath(String command) {
        String value = Objects.requireNonNull(command, "command").trim();
        if (value.isEmpty() || value.contains(File.separator)) {
            throw new IllegalArgumentException("command must be a simple executable name");
        }
        return new PathResolver(value);
    }

    /** Extracts the current platform's packaged binary into the default digest cache. */
    public static Path extract() throws IOException {
        return extract(defaultCacheDirectory());
    }

    /** Extracts the current platform's packaged binary into the supplied digest cache. */
    public static Path extract(Path cacheDirectory) throws IOException {
        return resolve(List.of(packaged()), cacheDirectory).executable();
    }

    /**
     * Resolves the first available binary. Errors from an available resolver are terminal and are
     * not converted into fallback.
     */
    public static MemoryServiceBinary resolve(List<Resolver> resolvers, Path cacheDirectory)
            throws IOException {
        ClassLoader loader = Thread.currentThread().getContextClassLoader();
        if (loader == null) {
            loader = MemoryServiceBinary.class.getClassLoader();
        }
        return resolve(
                resolvers,
                cacheDirectory,
                MemoryServicePlatform.current(),
                loader,
                System.getenv());
    }

    static MemoryServiceBinary resolve(
            List<Resolver> resolvers,
            Path cacheDirectory,
            MemoryServicePlatform platform,
            ClassLoader loader,
            Map<String, String> environment)
            throws IOException {
        Objects.requireNonNull(resolvers, "resolvers");
        Objects.requireNonNull(cacheDirectory, "cacheDirectory");
        if (resolvers.isEmpty()) {
            throw new IllegalArgumentException("at least one binary resolver is required");
        }

        List<String> attempted = new ArrayList<>();
        for (Resolver resolver : resolvers) {
            Optional<MemoryServiceBinary> resolved;
            if (resolver instanceof PackagedResolver) {
                attempted.add("packaged provider for " + platform.id());
                resolved = resolvePackaged(cacheDirectory, platform, loader);
            } else if (resolver instanceof FileResolver explicit) {
                attempted.add("file " + explicit.path());
                resolved = resolveFile(explicit.path(), platform, "explicit file");
            } else if (resolver instanceof PathResolver path) {
                attempted.add("PATH command " + path.command());
                resolved = resolveOnPath(path.command(), platform, environment);
            } else {
                throw new IllegalArgumentException("unsupported resolver: " + resolver);
            }
            if (resolved.isPresent()) {
                return resolved.get();
            }
        }
        throw new IOException(
                "No Memory Service executable resolved for "
                        + platform.id()
                        + "; attempted "
                        + String.join(", ", attempted));
    }

    /** Default service-specific cache directory. */
    public static Path defaultCacheDirectory() {
        MemoryServicePlatform platform = MemoryServicePlatform.current();
        String userHome = System.getProperty("user.home", ".");
        if ("macos".equals(platform.operatingSystem())) {
            return Path.of(userHome, "Library", "Caches", "memory-service", "binaries");
        }
        String xdg = System.getenv("XDG_CACHE_HOME");
        if (xdg != null && !xdg.isBlank()) {
            return Path.of(xdg, "memory-service", "binaries");
        }
        return Path.of(userHome, ".cache", "memory-service", "binaries");
    }

    private static Optional<MemoryServiceBinary> resolvePackaged(
            Path cacheDirectory, MemoryServicePlatform platform, ClassLoader loader)
            throws IOException {
        List<MemoryServiceBinaryProvider> matches = new ArrayList<>();
        ServiceLoader.load(MemoryServiceBinaryProvider.class, loader).stream()
                .map(ServiceLoader.Provider::get)
                .filter(provider -> platform.equals(provider.platform()))
                .forEach(matches::add);
        if (matches.isEmpty()) {
            return Optional.empty();
        }
        if (matches.size() > 1) {
            throw new IOException(
                    "Multiple Memory Service binary providers found for " + platform.id());
        }
        MemoryServiceBinaryProvider provider = matches.get(0);
        return Optional.of(
                new MemoryServiceBinary(
                        extractProvider(provider, cacheDirectory),
                        platform,
                        "packaged provider " + provider.getClass().getName()));
    }

    static Path extractProvider(MemoryServiceBinaryProvider provider, Path cacheDirectory)
            throws IOException {
        String expected = readExpectedSha256(provider);
        MemoryServicePlatform platform = provider.platform();
        Path platformDirectory = cacheDirectory.resolve(platform.id());
        Path digestDirectory = platformDirectory.resolve(expected);
        secureDirectory(cacheDirectory);
        secureDirectory(platformDirectory);
        secureDirectory(digestDirectory);
        Path executable = digestDirectory.resolve("memory-service");
        Path lockPath = digestDirectory.resolve(".extract.lock");

        Object jvmLock =
                JVM_EXTRACTION_LOCKS.computeIfAbsent(
                        lockPath.toAbsolutePath(), ignored -> new Object());
        synchronized (jvmLock) {
            return extractUnderLock(
                    provider, platform, expected, executable, lockPath, digestDirectory);
        }
    }

    private static Path extractUnderLock(
            MemoryServiceBinaryProvider provider,
            MemoryServicePlatform platform,
            String expected,
            Path executable,
            Path lockPath,
            Path digestDirectory)
            throws IOException {
        try (FileChannel channel =
                        FileChannel.open(
                                lockPath, StandardOpenOption.CREATE, StandardOpenOption.WRITE);
                FileLock ignored = channel.lock()) {
            if (Files.isRegularFile(executable, LinkOption.NOFOLLOW_LINKS)) {
                if (expected.equals(sha256(executable))) {
                    secureExecutable(executable);
                    return executable.toAbsolutePath();
                }
                Files.delete(executable);
            } else if (Files.exists(executable, LinkOption.NOFOLLOW_LINKS)) {
                throw new IOException("Cached executable is not a regular file: " + executable);
            }

            Path temporary = Files.createTempFile(digestDirectory, ".memory-service-", ".tmp");
            try {
                try (InputStream input = provider.openBinary()) {
                    Files.copy(input, temporary, StandardCopyOption.REPLACE_EXISTING);
                }
                String actual = sha256(temporary);
                if (!expected.equals(actual)) {
                    throw new IOException(
                            "Packaged Memory Service checksum mismatch for "
                                    + platform.id()
                                    + ": expected "
                                    + expected
                                    + " but got "
                                    + actual);
                }
                secureExecutable(temporary);
                try {
                    Files.move(
                            temporary,
                            executable,
                            StandardCopyOption.ATOMIC_MOVE,
                            StandardCopyOption.REPLACE_EXISTING);
                } catch (AtomicMoveNotSupportedException ignoredMove) {
                    Files.move(temporary, executable, StandardCopyOption.REPLACE_EXISTING);
                }
                secureExecutable(executable);
                return executable.toAbsolutePath();
            } finally {
                Files.deleteIfExists(temporary);
            }
        }
    }

    private static Optional<MemoryServiceBinary> resolveFile(
            Path candidate, MemoryServicePlatform platform, String source) throws IOException {
        Path absolute = candidate.toAbsolutePath().normalize();
        if (!Files.exists(absolute, LinkOption.NOFOLLOW_LINKS)) {
            return Optional.empty();
        }
        if (!Files.isRegularFile(absolute, LinkOption.NOFOLLOW_LINKS)) {
            throw new IOException("Memory Service executable is not a regular file: " + absolute);
        }
        if (!Files.isExecutable(absolute)) {
            throw new IOException("Memory Service executable is not executable: " + absolute);
        }
        return Optional.of(new MemoryServiceBinary(absolute, platform, source + " " + absolute));
    }

    private static Optional<MemoryServiceBinary> resolveOnPath(
            String command, MemoryServicePlatform platform, Map<String, String> environment)
            throws IOException {
        String path = environment.get("PATH");
        if (path == null || path.isBlank()) {
            return Optional.empty();
        }
        for (String directory : path.split(java.util.regex.Pattern.quote(File.pathSeparator))) {
            if (directory.isBlank()) {
                continue;
            }
            Optional<MemoryServiceBinary> candidate =
                    resolveFile(Path.of(directory).resolve(command), platform, "PATH");
            if (candidate.isPresent()) {
                return candidate;
            }
        }
        return Optional.empty();
    }

    private static String readExpectedSha256(MemoryServiceBinaryProvider provider)
            throws IOException {
        String value;
        try (InputStream input = provider.openSha256()) {
            value = new String(input.readAllBytes(), java.nio.charset.StandardCharsets.US_ASCII);
        }
        String digest = value.trim().split("\\s+", 2)[0].toLowerCase(Locale.ROOT);
        if (!digest.matches("[0-9a-f]{64}")) {
            throw new IOException("Invalid SHA-256 metadata from " + provider.getClass().getName());
        }
        return digest;
    }

    private static String sha256(Path file) throws IOException {
        MessageDigest digest;
        try {
            digest = MessageDigest.getInstance("SHA-256");
        } catch (NoSuchAlgorithmException e) {
            throw new IllegalStateException("SHA-256 is unavailable", e);
        }
        try (InputStream input = Files.newInputStream(file)) {
            byte[] buffer = new byte[64 * 1024];
            int count;
            while ((count = input.read(buffer)) != -1) {
                digest.update(buffer, 0, count);
            }
        }
        return HexFormat.of().formatHex(digest.digest());
    }

    private static void secureDirectory(Path directory) throws IOException {
        Files.createDirectories(directory);
        try {
            Files.setPosixFilePermissions(directory, PosixFilePermissions.fromString("rwx------"));
        } catch (UnsupportedOperationException ignored) {
            File file = directory.toFile();
            boolean secured =
                    file.setReadable(false, false)
                            && file.setWritable(false, false)
                            && file.setExecutable(false, false)
                            && file.setReadable(true, true)
                            && file.setWritable(true, true)
                            && file.setExecutable(true, true);
            if (!secured) {
                throw new IOException("Could not make directory owner-only: " + directory);
            }
        }
    }

    private static void secureExecutable(Path executable) throws IOException {
        try {
            Files.setPosixFilePermissions(executable, PosixFilePermissions.fromString("rwx------"));
        } catch (UnsupportedOperationException ignored) {
            File file = executable.toFile();
            if (!file.setReadable(true, true)
                    || !file.setWritable(true, true)
                    || !file.setExecutable(true, true)) {
                throw new IOException("Could not make executable owner-only: " + executable);
            }
        }
    }
}
