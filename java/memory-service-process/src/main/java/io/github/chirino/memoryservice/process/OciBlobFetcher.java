package io.github.chirino.memoryservice.process;

import io.github.chirino.memoryservice.process.MemoryServiceBinaryRegistries.Credentials;
import io.github.chirino.memoryservice.process.MemoryServiceBinaryRegistries.Location;
import java.io.FilterInputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.InterruptedIOException;
import java.io.OutputStream;
import java.net.URI;
import java.net.URLEncoder;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.DigestInputStream;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.time.Duration;
import java.time.Instant;
import java.time.ZonedDateTime;
import java.time.format.DateTimeFormatter;
import java.time.format.DateTimeParseException;
import java.util.ArrayList;
import java.util.Base64;
import java.util.HexFormat;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Properties;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.ScheduledFuture;
import java.util.concurrent.ScheduledThreadPoolExecutor;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.logging.Logger;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import java.util.zip.GZIPInputStream;

/**
 * Downloads one gzip-compressed executable layer by digest over the OCI distribution API.
 *
 * <p>Handles the bearer-token and Basic challenges, manual redirects that never forward the {@code
 * Authorization} header to another origin, pull-through-cache warming on a mirror {@code 404},
 * retries, a per-location deadline, and size and digest bounds from the signed descriptor.
 */
final class OciBlobFetcher {

    static final String MANIFEST_MEDIA_TYPE = "application/vnd.oci.image.manifest.v1+json";

    private static final Logger LOG = Logger.getLogger(OciBlobFetcher.class.getName());
    private static final int MAX_ATTEMPTS = 3;
    private static final int MAX_REDIRECTS = 5;
    private static final Duration MAX_RETRY_AFTER = Duration.ofMinutes(1);
    private static final Pattern CHALLENGE_PARAMETER =
            Pattern.compile("([A-Za-z_]+)\\s*=\\s*(?:\"((?:[^\"\\\\]|\\\\.)*)\"|([^,\\s]*))");
    private static final Pattern TOKEN_FIELD =
            Pattern.compile("\"(token|access_token)\"\\s*:\\s*\"((?:[^\"\\\\]|\\\\.)*)\"");
    private static final ScheduledExecutorService WATCHDOG = watchdog();

    private final MemoryServiceBinaryRegistries registries;

    OciBlobFetcher(MemoryServiceBinaryRegistries registries) {
        this.registries = registries;
    }

    /** Descriptor read from a provider's {@code memory-service.oci.properties} resource. */
    record Descriptor(
            String repository,
            String reference,
            String layerDigest,
            long layerSize,
            long binarySize) {

        private static final Pattern REPOSITORY =
                Pattern.compile(
                        "[a-z0-9.-]+(?::[0-9]{1,5})?(?:/[a-z0-9]+(?:(?:\\.|_|__|-+)[a-z0-9]+)*)+");
        private static final Pattern REFERENCE =
                Pattern.compile("[A-Za-z0-9_][A-Za-z0-9._-]{0,127}");
        private static final Pattern DIGEST = Pattern.compile("sha256:[0-9a-f]{64}");

        static Descriptor parse(InputStream input, String source) throws IOException {
            Properties properties = new Properties();
            properties.load(new java.io.InputStreamReader(input, StandardCharsets.UTF_8));
            String repository = required(properties, "repository", REPOSITORY, source);
            String reference = required(properties, "reference", REFERENCE, source);
            String layerDigest = required(properties, "layerDigest", DIGEST, source);
            long layerSize = size(properties, "layerSize", source);
            long binarySize = size(properties, "binarySize", source);
            return new Descriptor(repository, reference, layerDigest, layerSize, binarySize);
        }

        private static String required(
                Properties properties, String key, Pattern pattern, String source)
                throws IOException {
            String value = properties.getProperty(key);
            if (value == null || value.isBlank()) {
                throw new IOException("Invalid OCI descriptor from " + source + ": missing " + key);
            }
            value = value.trim();
            if (!pattern.matcher(value).matches()) {
                throw new IOException(
                        "Invalid OCI descriptor from " + source + ": malformed " + key);
            }
            return value;
        }

        private static long size(Properties properties, String key, String source)
                throws IOException {
            String value = required(properties, key, Pattern.compile("[0-9]{1,18}"), source);
            long parsed = Long.parseLong(value);
            if (parsed <= 0) {
                throw new IOException(
                        "Invalid OCI descriptor from " + source + ": " + key + " must be positive");
            }
            return parsed;
        }
    }

    /**
     * Downloads the descriptor's layer from the first location that succeeds and writes the
     * decompressed executable to {@code target}.
     */
    void fetch(Descriptor descriptor, Path target, String label) throws IOException {
        List<Location> locations = registries.locations(descriptor.repository());
        List<String> failures = new ArrayList<>();
        HttpClient.Builder builder =
                HttpClient.newBuilder()
                        .version(HttpClient.Version.HTTP_1_1)
                        .followRedirects(HttpClient.Redirect.NEVER)
                        .connectTimeout(registries.connectTimeout());
        if (registries.sslContext() != null) {
            builder.sslContext(registries.sslContext());
        }
        if (registries.proxy() != null) {
            builder.proxy(registries.proxy());
        }
        try (HttpClient client = builder.build()) {
            for (Location location : locations) {
                long started = System.nanoTime();
                LOG.info(
                        () ->
                                "Downloading Memory Service executable for "
                                        + label
                                        + " from "
                                        + location
                                        + " ("
                                        + descriptor.layerSize()
                                        + " bytes)");
                try {
                    new Attempt(client, location, descriptor, target).run();
                    long millis = TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - started);
                    LOG.info(
                            () ->
                                    "Downloaded Memory Service executable for "
                                            + label
                                            + " from "
                                            + location
                                            + " in "
                                            + millis
                                            + " ms");
                    return;
                } catch (InterruptedIOException e) {
                    throw e;
                } catch (IOException e) {
                    LOG.warning(
                            "Could not download Memory Service executable for "
                                    + label
                                    + " from "
                                    + location
                                    + ": "
                                    + e.getMessage());
                    failures.add(location + ": " + e.getMessage());
                }
            }
        }
        throw new IOException(
                "Could not download the Memory Service executable "
                        + descriptor.repository()
                        + "@"
                        + descriptor.layerDigest()
                        + " for "
                        + label
                        + "; tried "
                        + String.join("; ", failures));
    }

    /** Download from one location, with its own deadline, auth state, and retries. */
    private final class Attempt {
        private final HttpClient client;
        private final Location location;
        private final Descriptor descriptor;
        private final Path target;
        private final Instant deadline;
        private Credentials credentials;
        private String authorization;
        private boolean warmed;

        Attempt(HttpClient client, Location location, Descriptor descriptor, Path target) {
            this.client = client;
            this.location = location;
            this.descriptor = descriptor;
            this.target = target;
            this.deadline = Instant.now().plus(registries.downloadTimeout());
            this.credentials = registries.credentialsFor(location).orElse(null);
        }

        void run() throws IOException {
            for (int attempt = 1; ; attempt++) {
                try {
                    downloadOnce();
                    return;
                } catch (RetryableException e) {
                    if (attempt >= MAX_ATTEMPTS) {
                        throw new IOException(
                                e.getMessage() + " (after " + attempt + " attempts)", e);
                    }
                    Duration delay =
                            e.retryAfter != null
                                    ? e.retryAfter
                                    : registries.retryBackoff().multipliedBy(1L << (attempt - 1));
                    if (Instant.now().plus(delay).isAfter(deadline)) {
                        throw new IOException(
                                e.getMessage() + "; download deadline reached before a retry", e);
                    }
                    sleep(delay);
                }
            }
        }

        private void downloadOnce() throws IOException {
            URI blob = location.uri("blobs/" + descriptor.layerDigest());
            HttpResponse<InputStream> response = open(blob, null);
            if (response.statusCode() == 404 && location.mirror() && !warmed) {
                discard(response);
                // Pull-through caches often populate only when a manifest is pulled.
                HttpResponse<InputStream> manifest =
                        open(
                                location.uri("manifests/" + descriptor.reference()),
                                MANIFEST_MEDIA_TYPE);
                discard(manifest);
                int manifestStatus = manifest.statusCode();
                if (manifestStatus == 429 || manifestStatus >= 500) {
                    // Not warmed yet: the next attempt pulls the manifest again.
                    throwForStatus(manifestStatus, manifest, "manifest request");
                }
                warmed = true;
                response = open(blob, null);
            }
            int status = response.statusCode();
            if (status != 200) {
                discard(response);
                throwForStatus(status, response, "blob request");
            }
            readLayer(response);
        }

        private HttpResponse<InputStream> open(URI uri, String accept) throws IOException {
            URI current = uri;
            int redirects = 0;
            int authRounds = 0;
            while (true) {
                boolean sameOrigin = location.sameOrigin(current);
                HttpRequest.Builder request =
                        HttpRequest.newBuilder(current).GET().timeout(remaining());
                if (accept != null) {
                    request.header("Accept", accept);
                }
                if (sameOrigin && authorization != null) {
                    request.header("Authorization", authorization);
                }
                HttpResponse<InputStream> response = send(request.build());
                int status = response.statusCode();
                if (status == 401 && sameOrigin && authRounds < 2) {
                    String challenge =
                            response.headers().firstValue("WWW-Authenticate").orElse(null);
                    discard(response);
                    if (authRounds == 1) {
                        if (credentials == null || !credentials.implicit()) {
                            throw new IOException("registry rejected " + describeAuth());
                        }
                        rejectImplicit("the registry rejected its bearer token");
                    }
                    authenticate(challenge);
                    authRounds++;
                    continue;
                }
                if (status == 301
                        || status == 302
                        || status == 303
                        || status == 307
                        || status == 308) {
                    String target = response.headers().firstValue("Location").orElse(null);
                    discard(response);
                    if (target == null) {
                        throw new IOException("redirect without a Location header");
                    }
                    if (++redirects > MAX_REDIRECTS) {
                        throw new IOException("too many redirects");
                    }
                    URI next;
                    try {
                        next = current.resolve(target);
                    } catch (IllegalArgumentException e) {
                        throw new IOException("redirect to a malformed Location", e);
                    }
                    if (next.getHost() == null) {
                        throw new IOException("redirect to a Location without a host");
                    }
                    String scheme = next.getScheme() == null ? "" : next.getScheme();
                    if (!scheme.equalsIgnoreCase("https") && !scheme.equalsIgnoreCase("http")) {
                        throw new IOException("refused redirect to unsupported scheme " + scheme);
                    }
                    if ("https".equalsIgnoreCase(current.getScheme())
                            && !"https".equalsIgnoreCase(scheme)) {
                        throw new IOException("refused redirect from HTTPS to " + scheme);
                    }
                    current = next;
                    continue;
                }
                return response;
            }
        }

        private void authenticate(String challenge) throws IOException {
            if (challenge == null || challenge.isBlank()) {
                throw new IOException("registry returned 401 without a WWW-Authenticate challenge");
            }
            int space = challenge.indexOf(' ');
            String scheme = (space < 0 ? challenge : challenge.substring(0, space)).trim();
            Map<String, String> parameters = new LinkedHashMap<>();
            if (space >= 0) {
                Matcher matcher = CHALLENGE_PARAMETER.matcher(challenge.substring(space + 1));
                while (matcher.find()) {
                    String value = matcher.group(2) != null ? matcher.group(2) : matcher.group(3);
                    parameters.put(matcher.group(1).toLowerCase(Locale.ROOT), value);
                }
            }
            if (scheme.equalsIgnoreCase("Basic")) {
                if (credentials == null) {
                    throw new IOException("registry requires credentials (Basic challenge)");
                }
                authorization = basic(credentials);
                return;
            }
            if (!scheme.equalsIgnoreCase("Bearer")) {
                throw new IOException("unsupported authentication challenge " + scheme);
            }
            String realm = parameters.get("realm");
            if (realm == null || realm.isBlank()) {
                throw new IOException("Bearer challenge without a realm");
            }
            URI realmUri;
            try {
                realmUri = URI.create(realm);
            } catch (IllegalArgumentException e) {
                throw new IOException("Bearer challenge with an invalid realm", e);
            }
            String realmScheme = realmUri.getScheme() == null ? "" : realmUri.getScheme();
            if (!realmScheme.equalsIgnoreCase("https") && !realmScheme.equalsIgnoreCase("http")) {
                throw new IOException("Bearer challenge realm must be HTTP or HTTPS");
            }
            if (location.https() && !realmScheme.equalsIgnoreCase("https")) {
                throw new IOException("refused a token realm that downgrades HTTPS to HTTP");
            }
            String scope =
                    parameters.getOrDefault("scope", "repository:" + location.name() + ":pull");
            StringBuilder query = new StringBuilder();
            if (parameters.containsKey("service")) {
                query.append("service=").append(encode(parameters.get("service")));
            }
            if (!query.isEmpty()) {
                query.append('&');
            }
            query.append("scope=").append(encode(scope));
            URI tokenUri = URI.create(realm + (realm.contains("?") ? "&" : "?") + query);

            Credentials presented = credentials;
            if (presented != null && !realmScheme.equalsIgnoreCase("https")) {
                if (!presented.implicit()) {
                    throw new IOException(
                            "refused to send "
                                    + presented
                                    + " to non-HTTPS token realm "
                                    + realmUri.getHost());
                }
                presented = null;
            }
            // An ambient GitHub token only ever goes to a token realm on ghcr.io itself.
            if (presented != null
                    && presented.implicit()
                    && !registries.githubRegistry().equalsIgnoreCase(realmUri.getRawAuthority())) {
                presented = null;
            }
            authorization = "Bearer " + requestToken(tokenUri, presented);
        }

        private String requestToken(URI tokenUri, Credentials presented) throws IOException {
            HttpRequest.Builder request =
                    HttpRequest.newBuilder(tokenUri).GET().timeout(remaining());
            if (presented != null) {
                request.header("Authorization", basic(presented));
            }
            HttpResponse<InputStream> response = send(request.build());
            int status = response.statusCode();
            if ((status == 401 || status == 403) && presented != null) {
                discard(response);
                if (presented.implicit()) {
                    rejectImplicit("the token exchange returned HTTP " + status);
                    return requestToken(tokenUri, null);
                }
                throw new IOException(
                        "token exchange rejected " + presented + " with HTTP " + status);
            }
            if (status != 200) {
                discard(response);
                throwForStatus(status, response, "token request");
            }
            String body;
            AtomicBoolean timedOut = new AtomicBoolean();
            InputStream input = response.body();
            ScheduledFuture<?> watchdog = closeAtDeadline(input, timedOut);
            try (input) {
                byte[] bytes = input.readNBytes(1024 * 1024);
                if (timedOut.get()) {
                    throw new IOException("download deadline reached while reading the token");
                }
                body = new String(bytes, StandardCharsets.UTF_8);
            } catch (IOException e) {
                if (timedOut.get()) {
                    throw new IOException("download deadline reached while reading the token", e);
                }
                throw new RetryableException("token response failed: " + describe(e), null);
            } finally {
                watchdog.cancel(false);
            }
            Matcher matcher = TOKEN_FIELD.matcher(body);
            String token = null;
            while (matcher.find()) {
                if (token == null || "token".equals(matcher.group(1))) {
                    token = unescape(matcher.group(2));
                }
            }
            if (token == null || token.isBlank()) {
                throw new IOException("token response had no token");
            }
            return token;
        }

        private void rejectImplicit(String reason) {
            LOG.warning(
                    "Ignoring "
                            + credentials
                            + " for "
                            + location.authority()
                            + " because "
                            + reason
                            + "; retrying anonymously");
            credentials = null;
            authorization = null;
        }

        private String describeAuth() {
            return credentials == null ? "anonymous access" : String.valueOf(credentials);
        }

        private void readLayer(HttpResponse<InputStream> response) throws IOException {
            AtomicBoolean timedOut = new AtomicBoolean();
            InputStream body = response.body();
            ScheduledFuture<?> watchdog = closeAtDeadline(body, timedOut);
            MessageDigest digest = sha256();
            NetworkInputStream network = new NetworkInputStream(body, descriptor.layerSize());
            try (InputStream counted = network;
                    DigestInputStream digested = new DigestInputStream(counted, digest);
                    GZIPInputStream gzip = new GZIPInputStream(digested, 64 * 1024);
                    OutputStream output = Files.newOutputStream(target)) {
                byte[] buffer = new byte[64 * 1024];
                long written = 0;
                int count;
                while ((count = gzip.read(buffer)) != -1) {
                    written += count;
                    if (written > descriptor.binarySize()) {
                        throw new IntegrityException(
                                "layer expands past binarySize " + descriptor.binarySize());
                    }
                    output.write(buffer, 0, count);
                }
                // Hash any bytes after the gzip trailer so the digest covers the whole blob.
                digested.transferTo(OutputStream.nullOutputStream());
                if (network.count() != descriptor.layerSize()) {
                    throw new IntegrityException(
                            "layer size mismatch: expected "
                                    + descriptor.layerSize()
                                    + " bytes but got "
                                    + network.count());
                }
                String actual = "sha256:" + HexFormat.of().formatHex(digest.digest());
                if (!actual.equals(descriptor.layerDigest())) {
                    throw new IntegrityException(
                            "layer digest mismatch: expected "
                                    + descriptor.layerDigest()
                                    + " but got "
                                    + actual);
                }
                if (written != descriptor.binarySize()) {
                    throw new IntegrityException(
                            "executable size mismatch: expected "
                                    + descriptor.binarySize()
                                    + " bytes but got "
                                    + written);
                }
            } catch (IntegrityException e) {
                throw e;
            } catch (IOException e) {
                if (timedOut.get()) {
                    throw new IOException("download deadline reached while reading the layer", e);
                }
                if (e instanceof NetworkException) {
                    throw new RetryableException(
                            "connection failed while reading the layer: " + e.getMessage(), null);
                }
                throw new IntegrityException("corrupt layer: " + e.getMessage());
            } finally {
                watchdog.cancel(false);
            }
        }

        /**
         * Closes a response body at the deadline. The request timeout covers only the headers, so
         * without this a server that stalls mid-body would hold the extraction lock forever.
         */
        private ScheduledFuture<?> closeAtDeadline(InputStream body, AtomicBoolean timedOut)
                throws IOException {
            long delay = Math.max(1, remaining().toMillis());
            try {
                return WATCHDOG.schedule(
                        () -> {
                            timedOut.set(true);
                            try {
                                body.close();
                            } catch (IOException ignored) {
                                // The reader reports the deadline.
                            }
                        },
                        delay,
                        TimeUnit.MILLISECONDS);
            } catch (RuntimeException e) {
                body.close();
                throw e;
            }
        }

        private HttpResponse<InputStream> send(HttpRequest request) throws IOException {
            try {
                return client.send(request, HttpResponse.BodyHandlers.ofInputStream());
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                throw new InterruptedIOException("interrupted while downloading");
            } catch (IOException e) {
                throw new RetryableException(
                        "connection to "
                                + request.uri().getRawAuthority()
                                + " failed: "
                                + describe(e),
                        null);
            }
        }

        private Duration remaining() throws IOException {
            Duration left = Duration.between(Instant.now(), deadline);
            if (left.isZero() || left.isNegative()) {
                throw new IOException("download deadline reached");
            }
            return left;
        }

        private void throwForStatus(int status, HttpResponse<?> response, String what)
                throws IOException {
            if (status == 429 || status >= 500) {
                throw new RetryableException(
                        what + " returned HTTP " + status, retryAfter(response));
            }
            throw new IOException(what + " returned HTTP " + status);
        }
    }

    private static Duration retryAfter(HttpResponse<?> response) {
        String value = response.headers().firstValue("Retry-After").orElse(null);
        if (value == null) {
            return null;
        }
        value = value.trim();
        Duration delay;
        try {
            delay = Duration.ofSeconds(Long.parseLong(value));
        } catch (NumberFormatException e) {
            try {
                ZonedDateTime when =
                        ZonedDateTime.parse(value, DateTimeFormatter.RFC_1123_DATE_TIME);
                delay = Duration.between(Instant.now(), when.toInstant());
            } catch (DateTimeParseException ignored) {
                return null;
            }
        }
        if (delay.isNegative()) {
            return Duration.ZERO;
        }
        return delay.compareTo(MAX_RETRY_AFTER) > 0 ? MAX_RETRY_AFTER : delay;
    }

    private static void discard(HttpResponse<InputStream> response) {
        try {
            response.body().close();
        } catch (IOException ignored) {
            // The body is not needed.
        }
    }

    private static void sleep(Duration delay) throws InterruptedIOException {
        try {
            Thread.sleep(delay.toMillis());
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new InterruptedIOException("interrupted while waiting to retry a download");
        }
    }

    private static String basic(Credentials credentials) {
        String pair = credentials.username() + ":" + credentials.secret();
        return "Basic " + Base64.getEncoder().encodeToString(pair.getBytes(StandardCharsets.UTF_8));
    }

    private static String encode(String value) {
        return URLEncoder.encode(value, StandardCharsets.UTF_8);
    }

    private static String describe(IOException e) {
        String message = e.getMessage();
        return message == null || message.isBlank() ? e.getClass().getSimpleName() : message;
    }

    private static String unescape(String value) {
        if (value.indexOf('\\') < 0) {
            return value;
        }
        StringBuilder result = new StringBuilder(value.length());
        for (int i = 0; i < value.length(); i++) {
            char c = value.charAt(i);
            if (c != '\\' || i + 1 >= value.length()) {
                result.append(c);
                continue;
            }
            char next = value.charAt(++i);
            switch (next) {
                case 'n' -> result.append('\n');
                case 't' -> result.append('\t');
                case 'r' -> result.append('\r');
                case 'b' -> result.append('\b');
                case 'f' -> result.append('\f');
                case 'u' -> {
                    if (i + 4 < value.length()) {
                        result.append((char) Integer.parseInt(value.substring(i + 1, i + 5), 16));
                        i += 4;
                    }
                }
                default -> result.append(next);
            }
        }
        return result.toString();
    }

    private static MessageDigest sha256() {
        try {
            return MessageDigest.getInstance("SHA-256");
        } catch (NoSuchAlgorithmException e) {
            throw new IllegalStateException("SHA-256 is unavailable", e);
        }
    }

    private static ScheduledExecutorService watchdog() {
        ScheduledThreadPoolExecutor executor =
                new ScheduledThreadPoolExecutor(
                        1,
                        runnable -> {
                            Thread thread =
                                    new Thread(runnable, "memory-service-download-watchdog");
                            thread.setDaemon(true);
                            return thread;
                        });
        executor.setRemoveOnCancelPolicy(true);
        return executor;
    }

    /** A failure worth retrying at the same location. */
    private static final class RetryableException extends IOException {
        private final transient Duration retryAfter;

        RetryableException(String message, Duration retryAfter) {
            super(message);
            this.retryAfter = retryAfter;
        }
    }

    /** Content that fails a size or digest check. Never retried at the same location. */
    private static final class IntegrityException extends IOException {
        IntegrityException(String message) {
            super(message);
        }
    }

    /** An I/O failure from the network stream rather than from decoding. */
    private static final class NetworkException extends IOException {
        NetworkException(IOException cause) {
            super(describe(cause), cause);
        }
    }

    /** Counts body bytes, stops past the expected size, and marks network read failures. */
    private static final class NetworkInputStream extends FilterInputStream {
        private final long limit;
        private long count;

        NetworkInputStream(InputStream input, long limit) {
            super(input);
            this.limit = limit;
        }

        long count() {
            return count;
        }

        @Override
        public int read() throws IOException {
            byte[] one = new byte[1];
            int n = read(one, 0, 1);
            return n == -1 ? -1 : one[0] & 0xff;
        }

        @Override
        public int read(byte[] buffer, int offset, int length) throws IOException {
            int n;
            try {
                n = super.read(buffer, offset, length);
            } catch (IOException e) {
                throw new NetworkException(e);
            }
            if (n > 0) {
                count += n;
                if (count > limit) {
                    throw new IntegrityException("layer is larger than layerSize " + limit);
                }
            }
            return n;
        }

        @Override
        public long skip(long n) throws IOException {
            byte[] buffer = new byte[(int) Math.min(8192, Math.max(n, 1))];
            int read = read(buffer, 0, (int) Math.min(buffer.length, n));
            return Math.max(read, 0);
        }

        @Override
        public int available() {
            return 0;
        }

        @Override
        public boolean markSupported() {
            return false;
        }
    }
}
