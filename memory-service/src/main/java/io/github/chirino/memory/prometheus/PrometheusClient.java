package io.github.chirino.memory.prometheus;

import jakarta.json.JsonObject;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.QueryParam;
import jakarta.ws.rs.core.MediaType;
import org.eclipse.microprofile.rest.client.inject.RegisterRestClient;

/**
 * REST client for querying Prometheus.
 *
 * <p>This client is used by AdminStatsResource to execute predefined PromQL queries for admin
 * dashboard metrics.
 */
@RegisterRestClient(configKey = "prometheus")
@Path("/api/v1")
public interface PrometheusClient {

    /**
     * Executes a PromQL range query against Prometheus.
     *
     * @param query The PromQL query to execute
     * @param start Start timestamp (RFC3339 or Unix timestamp)
     * @param end End timestamp (RFC3339 or Unix timestamp)
     * @param step Query resolution step (e.g., "60s", "5m")
     * @return Prometheus query result as JSON
     */
    @GET
    @Path("/query_range")
    @Produces(MediaType.APPLICATION_JSON)
    JsonObject queryRange(
            @QueryParam("query") String query,
            @QueryParam("start") String start,
            @QueryParam("end") String end,
            @QueryParam("step") String step);
}
