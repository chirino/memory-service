package io.github.chirino.memoryservice.process;

import java.io.IOException;
import java.io.InputStream;
import java.net.ProxySelector;
import java.net.URI;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.GeneralSecurityException;
import java.security.KeyStore;
import java.security.cert.Certificate;
import java.security.cert.CertificateFactory;
import java.security.cert.X509Certificate;
import java.time.Duration;
import java.util.ArrayList;
import java.util.Collection;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Objects;
import java.util.Optional;
import java.util.function.Function;
import java.util.regex.Pattern;
import javax.net.ssl.SSLContext;
import javax.net.ssl.TrustManager;
import javax.net.ssl.TrustManagerFactory;
import javax.net.ssl.X509TrustManager;

/**
 * Immutable settings for downloading packaged Memory Service executables from OCI registries:
 * mirror rules, per-host credentials, extra trusted CAs, the proxy, and timeouts.
 *
 * <p>These settings are configured only in code. The library reads no system property or
 * environment variable that can add a mirror, change where credentials go, or trust a CA. The only
 * environment lookup is the optional {@code GH_TOKEN}/{@code GITHUB_TOKEN} pickup for {@code
 * ghcr.io}, which {@link Builder#githubTokenFromEnvironment(boolean)} turns off.
 *
 * <pre>{@code
 * MemoryServiceBinaryRegistries registries = MemoryServiceBinaryRegistries.builder()
 *         .mirror("ghcr.io", "harbor.corp.example/ghcr-proxy")
 *         .credentials("harbor.corp.example", "robot$memory-service", harborToken)
 *         .githubTokenFromEnvironment(false)
 *         .build();
 * MemoryServiceProcess.builder(stateDirectory)
 *         .binaryResolvers(MemoryServiceBinary.packaged(registries))
 *         .start();
 * }</pre>
 */
public final class MemoryServiceBinaryRegistries {

    static final String GITHUB_REGISTRY = "ghcr.io";
    static final List<String> GITHUB_TOKEN_VARIABLES = List.of("GH_TOKEN", "GITHUB_TOKEN");

    private static final Duration DEFAULT_DOWNLOAD_TIMEOUT = Duration.ofMinutes(5);
    private static final Duration DEFAULT_CONNECT_TIMEOUT = Duration.ofSeconds(30);
    private static final Duration DEFAULT_RETRY_BACKOFF = Duration.ofSeconds(1);

    private static final Pattern AUTHORITY =
            Pattern.compile(
                    "(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)*|\\[[0-9a-f:.]+\\])(?::[0-9]{1,5})?");
    private static final Pattern PATH_COMPONENT =
            Pattern.compile("[a-z0-9]+(?:(?:\\.|_|__|-+)[a-z0-9]+)*");

    private static final MemoryServiceBinaryRegistries DEFAULTS = builder().build();

    private final List<MirrorRule> mirrors;
    private final boolean upstreamFallback;
    private final Map<String, Credentials> credentials;
    private final boolean githubTokenFromEnvironment;
    private final Path trustedCertificates;
    private final SSLContext sslContext;
    private final ProxySelector proxy;
    private final Duration downloadTimeout;
    private final Duration connectTimeout;
    private final Duration retryBackoff;
    private final Function<String, String> environment;
    private final String githubRegistry;

    private MemoryServiceBinaryRegistries(Builder builder, SSLContext sslContext) {
        this.mirrors = List.copyOf(builder.mirrors);
        this.upstreamFallback =
                builder.upstreamFallback == null ? mirrors.isEmpty() : builder.upstreamFallback;
        this.credentials = Map.copyOf(builder.credentials);
        this.githubTokenFromEnvironment = builder.githubTokenFromEnvironment;
        this.trustedCertificates = builder.trustedCertificates;
        this.sslContext = sslContext;
        this.proxy = builder.proxy;
        this.downloadTimeout = builder.downloadTimeout;
        this.connectTimeout = builder.connectTimeout;
        this.retryBackoff = builder.retryBackoff;
        this.environment = builder.environment;
        this.githubRegistry = builder.githubRegistry;
    }

    /**
     * Default settings: download from the upstream registry named by the provider, and use {@code
     * GH_TOKEN} or {@code GITHUB_TOKEN} for {@code ghcr.io} when one is set.
     */
    public static MemoryServiceBinaryRegistries defaults() {
        return DEFAULTS;
    }

    /** Starts a new configuration with the same defaults as {@link #defaults()}. */
    public static Builder builder() {
        return new Builder();
    }

    /** Ordered registry locations to try for an upstream repository such as {@code ghcr.io/o/n}. */
    List<Location> locations(String repository) {
        Location upstream = Location.upstream(repository);
        int longest = -1;
        for (MirrorRule rule : mirrors) {
            if (rule.matches(repository)) {
                longest = Math.max(longest, rule.prefix().length());
            }
        }
        List<Location> result = new ArrayList<>();
        if (longest < 0) {
            result.add(upstream);
            return result;
        }
        for (MirrorRule rule : mirrors) {
            if (rule.prefix().length() == longest && rule.matches(repository)) {
                result.add(rule.resolve(repository));
            }
        }
        if (upstreamFallback) {
            result.add(upstream);
        }
        return result;
    }

    /**
     * Credentials for one location: explicit credentials for its host, then the GitHub token for
     * {@code ghcr.io}. Plain-HTTP locations never receive credentials.
     */
    Optional<Credentials> credentialsFor(Location location) {
        if (!location.https()) {
            return Optional.empty();
        }
        Credentials explicit = credentials.get(location.authority());
        if (explicit == null) {
            explicit = credentials.get(location.host());
        }
        if (explicit != null) {
            return Optional.of(explicit);
        }
        if (githubTokenFromEnvironment && githubRegistry.equals(location.authority())) {
            for (String variable : GITHUB_TOKEN_VARIABLES) {
                String value = environment.apply(variable);
                if (value != null && !value.isBlank()) {
                    return Optional.of(new Credentials("token", value.trim(), variable, true));
                }
            }
        }
        return Optional.empty();
    }

    SSLContext sslContext() {
        return sslContext;
    }

    ProxySelector proxy() {
        return proxy;
    }

    Duration downloadTimeout() {
        return downloadTimeout;
    }

    Duration connectTimeout() {
        return connectTimeout;
    }

    String githubRegistry() {
        return githubRegistry;
    }

    Duration retryBackoff() {
        return retryBackoff;
    }

    @Override
    public String toString() {
        return "MemoryServiceBinaryRegistries[mirrors="
                + mirrors
                + ", upstreamFallback="
                + upstreamFallback
                + ", credentialHosts="
                + credentials.keySet()
                + ", githubTokenFromEnvironment="
                + githubTokenFromEnvironment
                + ", trustedCertificates="
                + trustedCertificates
                + ", downloadTimeout="
                + downloadTimeout
                + "]";
    }

    /** Builder for {@link MemoryServiceBinaryRegistries}. */
    public static final class Builder {
        private final List<MirrorRule> mirrors = new ArrayList<>();
        private final Map<String, Credentials> credentials = new LinkedHashMap<>();
        private Boolean upstreamFallback;
        private boolean githubTokenFromEnvironment = true;
        private Path trustedCertificates;
        private ProxySelector proxy;
        private Duration downloadTimeout = DEFAULT_DOWNLOAD_TIMEOUT;
        private Duration connectTimeout = DEFAULT_CONNECT_TIMEOUT;
        private Duration retryBackoff = DEFAULT_RETRY_BACKOFF;
        private Function<String, String> environment = System::getenv;
        private String githubRegistry = GITHUB_REGISTRY;

        private Builder() {}

        /**
         * Adds a mirror rule that maps an upstream repository prefix, such as {@code ghcr.io} or
         * {@code ghcr.io/chirino/memory-service-binary}, to a mirror location such as {@code
         * harbor.corp.example/ghcr-proxy}. The longest matching prefix wins, and the rest of the
         * repository path is appended to the location. Rules for the same prefix are tried in the
         * order they were added. Locations use HTTPS unless they start with {@code http://}.
         */
        public Builder mirror(String prefix, String location) {
            mirrors.add(MirrorRule.parse(prefix, location));
            return this;
        }

        /**
         * Whether to try the upstream registry after every mirror has failed. Defaults to {@code
         * false} once a mirror rule matches, so air-gapped hosts never contact the upstream
         * registry.
         */
        public Builder upstreamFallback(boolean upstreamFallback) {
            this.upstreamFallback = upstreamFallback;
            return this;
        }

        /**
         * Credentials for one registry host ({@code host} or {@code host:port}). They are sent only
         * to that host over HTTPS and to the token realm it names. Registries that take a bare
         * token, such as GHCR, accept any username.
         */
        public Builder credentials(String host, String username, String secret) {
            String normalized = normalizeHost(host);
            Objects.requireNonNull(username, "username");
            Objects.requireNonNull(secret, "secret");
            if (secret.isEmpty()) {
                throw new IllegalArgumentException(
                        "secret for " + normalized + " must not be empty");
            }
            credentials.put(
                    normalized,
                    new Credentials(username, secret, "credentials(" + normalized + ")", false));
            return this;
        }

        /**
         * Whether to use the {@code GH_TOKEN} or {@code GITHUB_TOKEN} environment variable, in that
         * order, for {@code ghcr.io}. Defaults to {@code true}. The token is never sent to any other
         * host, and a rejected token falls back to anonymous pulls with a warning.
         */
        public Builder githubTokenFromEnvironment(boolean enabled) {
            this.githubTokenFromEnvironment = enabled;
            return this;
        }

        /**
         * Trusts the CA certificates in a PEM file in addition to the JVM's default trust store.
         */
        public Builder trustedCertificates(Path pemFile) {
            this.trustedCertificates = Objects.requireNonNull(pemFile, "pemFile");
            return this;
        }

        /** Proxy selection for registry requests. Defaults to {@link ProxySelector#getDefault()}. */
        public Builder proxy(ProxySelector proxy) {
            this.proxy = Objects.requireNonNull(proxy, "proxy");
            return this;
        }

        /**
         * Deadline for downloading from one registry location, including the body and retries.
         * Defaults to five minutes.
         */
        public Builder downloadTimeout(Duration timeout) {
            this.downloadTimeout = positive(timeout, "downloadTimeout");
            return this;
        }

        Builder connectTimeout(Duration timeout) {
            this.connectTimeout = positive(timeout, "connectTimeout");
            return this;
        }

        Builder retryBackoff(Duration backoff) {
            Objects.requireNonNull(backoff, "retryBackoff");
            if (backoff.isNegative()) {
                throw new IllegalArgumentException("retryBackoff must not be negative");
            }
            this.retryBackoff = backoff;
            return this;
        }

        /** Test hook: treat another registry authority as GitHub's for the token rules. */
        Builder githubRegistry(String authority) {
            this.githubRegistry = normalizeHost(authority);
            return this;
        }

        Builder environment(Map<String, String> environment) {
            Map<String, String> copy = Map.copyOf(environment);
            this.environment = copy::get;
            return this;
        }

        /**
         * Validates and builds the settings.
         *
         * @throws IllegalArgumentException when the CA file can't be read, or credentials are set
         *     for a host that is reached only over {@code http://}
         */
        public MemoryServiceBinaryRegistries build() {
            for (String host : credentials.keySet()) {
                boolean http = false;
                boolean https = false;
                for (MirrorRule rule : mirrors) {
                    if (rule.host().equals(host) || rule.authority().equals(host)) {
                        if (rule.https()) {
                            https = true;
                        } else {
                            http = true;
                        }
                    }
                }
                if (http && !https) {
                    throw new IllegalArgumentException(
                            "credentials for "
                                    + host
                                    + " would never be used: that host is reached only over"
                                    + " http://, and credentials are sent only over HTTPS");
                }
            }
            SSLContext context = null;
            if (trustedCertificates != null) {
                context = sslContextWith(trustedCertificates);
            }
            return new MemoryServiceBinaryRegistries(this, context);
        }
    }

    private static Duration positive(Duration value, String name) {
        Objects.requireNonNull(value, name);
        if (value.isZero() || value.isNegative()) {
            throw new IllegalArgumentException(name + " must be positive");
        }
        return value;
    }

    private static String normalizeHost(String host) {
        Objects.requireNonNull(host, "host");
        String value = host.trim().toLowerCase(Locale.ROOT);
        if (value.isEmpty()) {
            throw new IllegalArgumentException("registry host must not be blank");
        }
        if (!AUTHORITY.matcher(value).matches()) {
            throw new IllegalArgumentException(
                    "registry host must be host or host:port without a scheme or path: " + host);
        }
        return value;
    }

    private static SSLContext sslContextWith(Path pemFile) {
        if (!Files.isRegularFile(pemFile)) {
            throw new IllegalArgumentException("trusted certificate file not found: " + pemFile);
        }
        try {
            Collection<? extends Certificate> extra;
            try (InputStream input = Files.newInputStream(pemFile)) {
                extra = CertificateFactory.getInstance("X.509").generateCertificates(input);
            }
            if (extra.isEmpty()) {
                throw new IllegalArgumentException("no certificates found in " + pemFile);
            }
            KeyStore store = KeyStore.getInstance(KeyStore.getDefaultType());
            store.load(null, null);
            int index = 0;
            for (X509Certificate certificate : defaultTrustManager().getAcceptedIssuers()) {
                store.setCertificateEntry("default-" + index++, certificate);
            }
            index = 0;
            for (Certificate certificate : extra) {
                store.setCertificateEntry("trusted-" + index++, certificate);
            }
            TrustManagerFactory factory =
                    TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm());
            factory.init(store);
            SSLContext context = SSLContext.getInstance("TLS");
            context.init(null, factory.getTrustManagers(), null);
            return context;
        } catch (IOException | GeneralSecurityException e) {
            throw new IllegalArgumentException(
                    "could not load trusted certificates from " + pemFile + ": " + e.getMessage(),
                    e);
        }
    }

    private static X509TrustManager defaultTrustManager() throws GeneralSecurityException {
        TrustManagerFactory factory =
                TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm());
        factory.init((KeyStore) null);
        for (TrustManager manager : factory.getTrustManagers()) {
            if (manager instanceof X509TrustManager x509) {
                return x509;
            }
        }
        throw new GeneralSecurityException("no default X.509 trust manager");
    }

    /** Registry credentials. {@link #toString()} never includes the secret. */
    record Credentials(String username, String secret, String source, boolean implicit) {
        @Override
        public String toString() {
            return source;
        }
    }

    /** One place to download a repository from. */
    record Location(String scheme, String authority, String name, boolean mirror) {

        static Location upstream(String repository) {
            int slash = repository.indexOf('/');
            return new Location(
                    "https",
                    repository.substring(0, slash),
                    repository.substring(slash + 1),
                    false);
        }

        boolean https() {
            return "https".equals(scheme);
        }

        String host() {
            if (authority.startsWith("[")) {
                return authority.substring(0, authority.indexOf(']') + 1);
            }
            int colon = authority.lastIndexOf(':');
            return colon < 0 ? authority : authority.substring(0, colon);
        }

        URI uri(String path) {
            return URI.create(scheme + "://" + authority + "/v2/" + name + "/" + path);
        }

        /** True when the URI is on this location's scheme, host, and port. */
        boolean sameOrigin(URI uri) {
            if (!scheme.equalsIgnoreCase(uri.getScheme())) {
                return false;
            }
            String other = uri.getRawAuthority();
            return other != null && authority.equalsIgnoreCase(other);
        }

        @Override
        public String toString() {
            return (https() ? "" : "http://") + authority + "/" + name;
        }
    }

    /** A {@code registries.conf}-style mirror rule. */
    record MirrorRule(String prefix, String scheme, String authority, String path) {

        static MirrorRule parse(String prefix, String location) {
            Objects.requireNonNull(prefix, "prefix");
            Objects.requireNonNull(location, "location");
            String normalizedPrefix = prefix.trim().toLowerCase(Locale.ROOT);
            if (normalizedPrefix.contains("://")) {
                throw new IllegalArgumentException(
                        "mirror prefix must not include a scheme: " + prefix);
            }
            validateReference(normalizedPrefix, "mirror prefix", prefix);

            String value = location.trim();
            String scheme = "https";
            String lower = value.toLowerCase(Locale.ROOT);
            if (lower.startsWith("http://")) {
                scheme = "http";
                value = value.substring("http://".length());
            } else if (lower.startsWith("https://")) {
                value = value.substring("https://".length());
            } else if (value.contains("://")) {
                throw new IllegalArgumentException(
                        "mirror location must use http:// or https://: " + location);
            }
            value = value.toLowerCase(Locale.ROOT);
            validateReference(value, "mirror location", location);
            int slash = value.indexOf('/');
            String authority = slash < 0 ? value : value.substring(0, slash);
            String path = slash < 0 ? "" : value.substring(slash + 1);
            if (path.isEmpty() && normalizedPrefix.contains("/")) {
                throw new IllegalArgumentException(
                        "mirror location "
                                + location
                                + " needs a repository path because prefix "
                                + prefix
                                + " is more than a registry host");
            }
            return new MirrorRule(normalizedPrefix, scheme, authority, path);
        }

        private static void validateReference(String value, String what, String original) {
            if (value.isEmpty()) {
                throw new IllegalArgumentException(what + " must not be blank");
            }
            String[] parts = value.split("/", -1);
            if (!AUTHORITY.matcher(parts[0]).matches()) {
                throw new IllegalArgumentException(
                        what + " must start with a registry host: " + original);
            }
            for (int i = 1; i < parts.length; i++) {
                if (!PATH_COMPONENT.matcher(parts[i]).matches()) {
                    throw new IllegalArgumentException(
                            what + " has an invalid repository path: " + original);
                }
            }
        }

        boolean matches(String repository) {
            return repository.equals(prefix) || repository.startsWith(prefix + "/");
        }

        Location resolve(String repository) {
            String rest = repository.substring(prefix.length());
            String name = path + rest;
            if (name.startsWith("/")) {
                name = name.substring(1);
            }
            return new Location(scheme, authority, name, true);
        }

        boolean https() {
            return "https".equals(scheme);
        }

        String host() {
            return Location.upstream(authority + "/x").host();
        }

        @Override
        public String toString() {
            return prefix
                    + " -> "
                    + (https() ? "" : "http://")
                    + authority
                    + (path.isEmpty() ? "" : "/" + path);
        }
    }
}
