package io.github.chirino.memory.config;

import io.smallrye.config.ConfigSourceContext;
import io.smallrye.config.ConfigSourceFactory;
import io.smallrye.config.ConfigValue;
import io.smallrye.config.PropertiesConfigSource;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import org.eclipse.microprofile.config.spi.ConfigSource;

/**
 * Derives Quarkus subsystem configuration from memory-service properties.
 *
 * <p><b>Datastore type routing:</b> When {@code memory-service.datastore.type=mongo}, provides a
 * dummy JDBC datasource URL and disables PostgreSQL Liquibase. The dummy URL keeps Hibernate ORM
 * and the datasource bean active (avoiding Quarkus synthetic CDI observer failures) while ensuring
 * no actual PostgreSQL connection is made — the {@code Instance<>} lazy injection in all selector
 * classes ensures PostgreSQL beans are never resolved.
 *
 * <p><b>Migration routing:</b> {@code memory-service.datastore.migrate-at-start} (default: {@code
 * true}) drives the correct Liquibase migration property based on the datastore type:
 *
 * <ul>
 *   <li>{@code postgres} → {@code quarkus.liquibase.migrate-at-start}
 *   <li>{@code mongo} → {@code quarkus.liquibase-mongodb.migrate-at-start}
 * </ul>
 */
public class DatastoreConfigSourceFactory implements ConfigSourceFactory {

    private static final String DATASTORE_TYPE = "memory-service.datastore.type";
    private static final String MIGRATE_AT_START = "memory-service.datastore.migrate-at-start";

    // Ordinal 275: higher than application.properties (250), but lower than
    // system properties (300) and environment variables (400), so explicit overrides still win.
    private static final int ORDINAL = 275;

    @Override
    public Iterable<ConfigSource> getConfigSources(ConfigSourceContext context) {
        String type = getStringValue(context, DATASTORE_TYPE, "postgres");
        String migrateAtStart = getStringValue(context, MIGRATE_AT_START, "true");

        Map<String, String> properties = new HashMap<>();

        if ("mongo".equals(type) || "mongodb".equals(type)) {
            // When dev services are disabled (e.g., production), provide a dummy JDBC URL
            // to keep Hibernate ORM and the datasource bean active. Quarkus registers
            // synthetic CDI lifecycle observers for the Hibernate Session at build time —
            // setting quarkus.hibernate-orm.active=false causes InactiveBeanException from
            // these observers. The dummy URL prevents auto-deactivation while Instance<>
            // lazy injection in selectors ensures no PostgreSQL connection is ever made.
            // When dev services ARE enabled (dev/test), skip the dummy URL so dev services
            // can provide a real PostgreSQL instance normally.
            String devServicesEnabled =
                    getStringValue(context, "quarkus.datasource.devservices.enabled", "false");
            if ("true".equals(devServicesEnabled)) {
                // Dev/test mode: dev services provide a real PostgreSQL instance.
                // Run PG Liquibase so the schema exists (needed by test cleanup code).
                properties.put("quarkus.liquibase.migrate-at-start", migrateAtStart);
            } else {
                // Production: no dev services. Provide a dummy JDBC URL to keep Hibernate
                // ORM and the datasource bean active, with no actual connection pool.
                properties.put(
                        "quarkus.datasource.jdbc.url", "jdbc:postgresql://unused:5432/unused");
                properties.put("quarkus.datasource.jdbc.initial-size", "0");
                properties.put("quarkus.datasource.jdbc.min-size", "0");
                properties.put("quarkus.datasource.jdbc.max-size", "1");
                properties.put("quarkus.liquibase.migrate-at-start", "false");
            }

            // Route migrations to MongoDB Liquibase
            properties.put("quarkus.liquibase-mongodb.migrate-at-start", migrateAtStart);
        } else {
            // Route migrations to PostgreSQL Liquibase
            properties.put("quarkus.liquibase.migrate-at-start", migrateAtStart);
        }

        return List.of(new PropertiesConfigSource(properties, "datastore-auto-config", ORDINAL));
    }

    private static String getStringValue(
            ConfigSourceContext context, String key, String defaultValue) {
        ConfigValue value = context.getValue(key);
        return (value != null && value.getValue() != null)
                ? value.getValue().trim().toLowerCase()
                : defaultValue;
    }
}
