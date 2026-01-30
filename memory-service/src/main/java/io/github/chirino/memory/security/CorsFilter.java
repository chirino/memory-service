package io.github.chirino.memory.security;

import io.quarkus.vertx.web.RouteFilter;
import io.vertx.core.http.HttpMethod;
import io.vertx.core.http.HttpServerRequest;
import io.vertx.core.http.HttpServerResponse;
import io.vertx.ext.web.RoutingContext;
import jakarta.inject.Singleton;
import java.util.Optional;
import java.util.Set;
import java.util.regex.Pattern;
import org.eclipse.microprofile.config.inject.ConfigProperty;

/**
 * Vert.x route filter that handles CORS preflight and response headers.
 * Runs before authentication (priority 10) to allow OPTIONS requests without credentials.
 * Enabled when quarkus.http.cors is true and origins are configured.
 */
@Singleton
public class CorsFilter {

    @ConfigProperty(name = "quarkus.http.cors", defaultValue = "false")
    boolean corsEnabled;

    @ConfigProperty(name = "quarkus.http.cors.origins")
    Optional<Set<String>> corsOrigins;

    @ConfigProperty(
            name = "quarkus.http.cors.methods",
            defaultValue = "GET,POST,PUT,PATCH,DELETE,OPTIONS")
    String corsMethods;

    @ConfigProperty(
            name = "quarkus.http.cors.headers",
            defaultValue = "accept,authorization,content-type,x-requested-with")
    String corsHeaders;

    @ConfigProperty(name = "quarkus.http.cors.exposed-headers", defaultValue = "")
    String corsExposedHeaders;

    @ConfigProperty(
            name = "quarkus.http.cors.access-control-allow-credentials",
            defaultValue = "true")
    boolean corsAllowCredentials;

    @ConfigProperty(name = "quarkus.http.cors.access-control-max-age", defaultValue = "86400")
    String corsMaxAge;

    @RouteFilter(10000)
    void handleCors(RoutingContext ctx) {
        if (!corsEnabled) {
            ctx.next();
            return;
        }

        HttpServerRequest request = ctx.request();
        HttpServerResponse response = ctx.response();
        String origin = request.getHeader("Origin");

        if (origin == null || !isOriginAllowed(origin)) {
            ctx.next();
            return;
        }

        // Add CORS headers to all responses
        response.putHeader("Access-Control-Allow-Origin", origin);
        response.putHeader(
                "Access-Control-Allow-Credentials", String.valueOf(corsAllowCredentials));
        if (!corsExposedHeaders.isEmpty()) {
            response.putHeader("Access-Control-Expose-Headers", corsExposedHeaders);
        }

        // Handle preflight OPTIONS requests
        if (request.method() == HttpMethod.OPTIONS) {
            response.putHeader("Access-Control-Allow-Methods", corsMethods);
            response.putHeader("Access-Control-Allow-Headers", corsHeaders);
            response.putHeader("Access-Control-Max-Age", corsMaxAge);
            response.setStatusCode(200).end();
            return;
        }

        ctx.next();
    }

    private boolean isOriginAllowed(String origin) {
        if (corsOrigins.isEmpty() || corsOrigins.get().isEmpty()) {
            return false;
        }

        for (String allowed : corsOrigins.get()) {
            // Handle regex patterns (surrounded by /)
            if (allowed.startsWith("/") && allowed.endsWith("/") && allowed.length() > 2) {
                String regex = allowed.substring(1, allowed.length() - 1);
                if (Pattern.matches(regex, origin)) {
                    return true;
                }
            } else if (allowed.equals(origin) || allowed.equals("*")) {
                return true;
            }
        }
        return false;
    }
}
