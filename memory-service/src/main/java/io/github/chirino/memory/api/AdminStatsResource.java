package io.github.chirino.memory.api;

import io.github.chirino.memory.admin.client.model.ErrorResponse;
import io.github.chirino.memory.admin.client.model.TimeSeriesResponse;
import io.github.chirino.memory.admin.client.model.TimeSeriesResponseDataInner;
import io.github.chirino.memory.prometheus.PrometheusClient;
import io.github.chirino.memory.prometheus.PrometheusNotConfiguredException;
import io.github.chirino.memory.prometheus.PrometheusUnavailableException;
import io.github.chirino.memory.security.AdminRoleResolver;
import io.github.chirino.memory.security.ApiKeyContext;
import io.github.chirino.memory.store.AccessDeniedException;
import io.quarkus.security.Authenticated;
import io.quarkus.security.identity.SecurityIdentity;
import jakarta.inject.Inject;
import jakarta.json.JsonArray;
import jakarta.json.JsonObject;
import jakarta.ws.rs.DefaultValue;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.QueryParam;
import jakarta.ws.rs.WebApplicationException;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;
import java.time.Instant;
import java.time.ZoneOffset;
import java.time.temporal.ChronoUnit;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.eclipse.microprofile.rest.client.inject.RestClient;
import org.jboss.logging.Logger;

/**
 * Admin stats endpoints for dashboard metrics.
 *
 * <p>These endpoints query Prometheus for aggregated metrics and return typed time series
 * responses. Prometheus must be configured via the memory-service.prometheus.url property.
 *
 * <p>All endpoints require auditor or admin role.
 */
@Path("/v1/admin/stats")
@Authenticated
@Produces(MediaType.APPLICATION_JSON)
public class AdminStatsResource {

    private static final Logger LOG = Logger.getLogger(AdminStatsResource.class);

    // Predefined PromQL queries
    private static final String REQUEST_RATE_QUERY =
            "sum(rate(http_server_requests_seconds_count[5m]))";
    private static final String ERROR_RATE_QUERY =
            "sum(rate(http_server_requests_seconds_count{status=~\"5..\"}[5m])) / "
                    + "sum(rate(http_server_requests_seconds_count[5m])) * 100";
    private static final String LATENCY_P95_QUERY =
            "histogram_quantile(0.95, sum(rate(http_server_requests_seconds_bucket[5m])) by (le))";
    private static final String CACHE_HIT_RATE_QUERY =
            "sum(rate(memory_entries_cache_hits_total[5m])) / "
                    + "(sum(rate(memory_entries_cache_hits_total[5m])) + "
                    + "sum(rate(memory_entries_cache_misses_total[5m]))) * 100";

    @ConfigProperty(name = "memory-service.prometheus.url")
    Optional<String> prometheusUrl;

    @Inject @RestClient PrometheusClient prometheusClient;

    @Inject AdminRoleResolver roleResolver;

    @Inject SecurityIdentity identity;

    @Inject ApiKeyContext apiKeyContext;

    @GET
    @Path("/request-rate")
    public Response getRequestRate(
            @QueryParam("start") String start,
            @QueryParam("end") String end,
            @QueryParam("step") @DefaultValue("60s") String step) {
        return executeRangeQuery(
                REQUEST_RATE_QUERY, "request_rate", "requests/sec", start, end, step);
    }

    @GET
    @Path("/error-rate")
    public Response getErrorRate(
            @QueryParam("start") String start,
            @QueryParam("end") String end,
            @QueryParam("step") @DefaultValue("60s") String step) {
        return executeRangeQuery(ERROR_RATE_QUERY, "error_rate", "percent", start, end, step);
    }

    @GET
    @Path("/latency-p95")
    public Response getLatencyP95(
            @QueryParam("start") String start,
            @QueryParam("end") String end,
            @QueryParam("step") @DefaultValue("60s") String step) {
        return executeRangeQuery(LATENCY_P95_QUERY, "latency_p95", "seconds", start, end, step);
    }

    @GET
    @Path("/cache-hit-rate")
    public Response getCacheHitRate(
            @QueryParam("start") String start,
            @QueryParam("end") String end,
            @QueryParam("step") @DefaultValue("60s") String step) {
        return executeRangeQuery(
                CACHE_HIT_RATE_QUERY, "cache_hit_rate", "percent", start, end, step);
    }

    private Response executeRangeQuery(
            String query, String metric, String unit, String start, String end, String step) {
        try {
            roleResolver.requireAuditor(identity, apiKeyContext);
            checkPrometheusConfigured();

            // Default time range: last hour
            String resolvedStart =
                    start != null ? start : Instant.now().minus(1, ChronoUnit.HOURS).toString();
            String resolvedEnd = end != null ? end : Instant.now().toString();

            JsonObject prometheusResponse =
                    prometheusClient.queryRange(query, resolvedStart, resolvedEnd, step);
            TimeSeriesResponse response = convertToTimeSeries(prometheusResponse, metric, unit);
            return Response.ok(response).build();
        } catch (AccessDeniedException e) {
            return forbidden(e);
        } catch (PrometheusNotConfiguredException e) {
            return prometheusNotConfigured(e);
        } catch (PrometheusUnavailableException e) {
            return prometheusUnavailable(e);
        } catch (WebApplicationException e) {
            // REST client errors (connection refused, timeout, etc.)
            LOG.warnf("Prometheus query failed: %s", e.getMessage());
            return prometheusUnavailable(
                    new PrometheusUnavailableException("Prometheus query failed", e));
        } catch (Exception e) {
            // Catch-all for unexpected errors during Prometheus communication
            LOG.warnf("Unexpected error querying Prometheus: %s", e.getMessage());
            return prometheusUnavailable(
                    new PrometheusUnavailableException(
                            "Prometheus query failed: " + e.getMessage(), e));
        }
    }

    private TimeSeriesResponse convertToTimeSeries(
            JsonObject prometheus, String metric, String unit) {
        TimeSeriesResponse response = new TimeSeriesResponse();
        response.setMetric(metric);
        response.setUnit(unit);

        List<TimeSeriesResponseDataInner> data = new ArrayList<>();
        JsonObject dataObj = prometheus.getJsonObject("data");
        if (dataObj != null) {
            JsonArray results = dataObj.getJsonArray("result");
            if (results != null && !results.isEmpty()) {
                JsonArray values = results.getJsonObject(0).getJsonArray("values");
                if (values != null) {
                    for (int i = 0; i < values.size(); i++) {
                        JsonArray point = values.getJsonArray(i);
                        TimeSeriesResponseDataInner dp = new TimeSeriesResponseDataInner();
                        // Prometheus returns [timestamp, "value"] pairs
                        long epochSeconds = point.getJsonNumber(0).longValue();
                        dp.setTimestamp(
                                Instant.ofEpochSecond(epochSeconds).atOffset(ZoneOffset.UTC));
                        String valueStr = point.getString(1);
                        // Handle NaN and Inf values from Prometheus
                        if ("NaN".equals(valueStr)
                                || "+Inf".equals(valueStr)
                                || "-Inf".equals(valueStr)) {
                            dp.setValue(null);
                        } else {
                            dp.setValue(Double.parseDouble(valueStr));
                        }
                        data.add(dp);
                    }
                }
            }
        }
        response.setData(data);
        return response;
    }

    private void checkPrometheusConfigured() {
        if (prometheusUrl.isEmpty() || prometheusUrl.get().isBlank()) {
            throw new PrometheusNotConfiguredException();
        }
    }

    private Response forbidden(AccessDeniedException e) {
        LOG.infof("Access denied for admin stats operation: %s", e.getMessage());
        ErrorResponse error = new ErrorResponse();
        error.setError("Forbidden");
        error.setCode("forbidden");
        error.setDetails(Map.of("message", e.getMessage()));
        return Response.status(Response.Status.FORBIDDEN).entity(error).build();
    }

    private Response prometheusNotConfigured(PrometheusNotConfiguredException e) {
        ErrorResponse error = new ErrorResponse();
        error.setError("Prometheus not configured");
        error.setCode("prometheus_not_configured");
        error.setDetails(Map.of("message", e.getMessage()));
        return Response.status(501).entity(error).build();
    }

    private Response prometheusUnavailable(PrometheusUnavailableException e) {
        ErrorResponse error = new ErrorResponse();
        error.setError("Prometheus unavailable");
        error.setCode("prometheus_unavailable");
        error.setDetails(Map.of("message", e.getMessage()));
        return Response.status(503).entity(error).build();
    }
}
