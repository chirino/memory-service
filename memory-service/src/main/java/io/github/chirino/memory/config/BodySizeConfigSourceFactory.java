package io.github.chirino.memory.config;

import io.smallrye.config.ConfigSourceContext;
import io.smallrye.config.ConfigSourceFactory;
import io.smallrye.config.ConfigValue;
import io.smallrye.config.PropertiesConfigSource;
import java.util.List;
import java.util.Map;
import org.eclipse.microprofile.config.spi.ConfigSource;

/**
 * Automatically sets {@code quarkus.http.limits.max-body-size} to 2x the value of
 * {@code memory-service.attachments.max-size} so users only need to configure one property.
 *
 * <p>The 2x multiplier accounts for multipart encoding overhead (boundaries, headers, etc.).
 *
 * <p>Users can still override {@code quarkus.http.limits.max-body-size} explicitly via system
 * properties or environment variables if needed.
 */
public class BodySizeConfigSourceFactory implements ConfigSourceFactory {

    private static final String ATTACHMENT_MAX_SIZE = "memory-service.attachments.max-size";
    private static final String BODY_SIZE_PROPERTY = "quarkus.http.limits.max-body-size";
    private static final long DEFAULT_MAX_SIZE = 10 * 1024 * 1024; // 10M
    private static final int MULTIPLIER = 2;

    // Ordinal 275: higher than application.properties (250), but lower than
    // system properties (300) and environment variables (400), so explicit overrides still win.
    private static final int ORDINAL = 275;

    @Override
    public Iterable<ConfigSource> getConfigSources(ConfigSourceContext context) {
        ConfigValue maxSizeValue = context.getValue(ATTACHMENT_MAX_SIZE);
        long maxSizeBytes = DEFAULT_MAX_SIZE;

        if (maxSizeValue != null && maxSizeValue.getValue() != null) {
            maxSizeBytes = parseMemorySize(maxSizeValue.getValue());
        }

        long bodySizeBytes = maxSizeBytes * MULTIPLIER;

        return List.of(
                new PropertiesConfigSource(
                        Map.of(BODY_SIZE_PROPERTY, bodySizeBytes + ""),
                        "body-size-auto-config",
                        ORDINAL));
    }

    /** Parses a memory size string like "10M", "512K", "1G", or plain bytes "10485760". */
    static long parseMemorySize(String value) {
        value = value.trim();
        if (value.isEmpty()) {
            return DEFAULT_MAX_SIZE;
        }

        char last = Character.toUpperCase(value.charAt(value.length() - 1));
        long multiplier = 1;

        if (last == 'K') {
            multiplier = 1024;
            value = value.substring(0, value.length() - 1).trim();
        } else if (last == 'M') {
            multiplier = 1024 * 1024;
            value = value.substring(0, value.length() - 1).trim();
        } else if (last == 'G') {
            multiplier = 1024L * 1024 * 1024;
            value = value.substring(0, value.length() - 1).trim();
        }

        return Long.parseLong(value) * multiplier;
    }
}
