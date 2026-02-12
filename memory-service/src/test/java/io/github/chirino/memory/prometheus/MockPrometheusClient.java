package io.github.chirino.memory.prometheus;

import io.quarkiverse.cucumber.ScenarioScope;
import jakarta.annotation.Priority;
import jakarta.enterprise.inject.Alternative;
import jakarta.json.Json;
import jakarta.json.JsonObject;
import jakarta.ws.rs.WebApplicationException;
import java.io.StringReader;
import org.eclipse.microprofile.rest.client.inject.RestClient;

/**
 * Mock implementation of PrometheusClient for testing admin stats endpoints.
 *
 * <p>This mock is activated during tests via CDI @Alternative and can be controlled from step
 * definitions to simulate various Prometheus states (available, unavailable, etc.).
 */
@Alternative
@Priority(1)
@RestClient
@ScenarioScope
public class MockPrometheusClient implements PrometheusClient {

    // Canned Prometheus response for single-series queries
    private static final String MOCK_SINGLE_SERIES_RESPONSE =
            """
            {
              "status": "success",
              "data": {
                "resultType": "matrix",
                "result": [{
                  "metric": {},
                  "values": [
                    [1704067200, "42.5"],
                    [1704067260, "45.2"],
                    [1704067320, "47.8"]
                  ]
                }]
              }
            }
            """;

    // Canned Prometheus response for multi-series queries (with operation labels)
    private static final String MOCK_MULTI_SERIES_RESPONSE =
            """
            {
              "status": "success",
              "data": {
                "resultType": "matrix",
                "result": [
                  {
                    "metric": {"operation": "createConversation"},
                    "values": [
                      [1704067200, "0.025"],
                      [1704067260, "0.028"],
                      [1704067320, "0.022"]
                    ]
                  },
                  {
                    "metric": {"operation": "appendMemoryEntries"},
                    "values": [
                      [1704067200, "0.045"],
                      [1704067260, "0.052"],
                      [1704067320, "0.048"]
                    ]
                  }
                ]
              }
            }
            """;

    private boolean available = true;
    private String customResponse = null;

    @Override
    public JsonObject queryRange(String query, String start, String end, String step) {
        if (!available) {
            throw new WebApplicationException("Prometheus unavailable", 503);
        }
        String responseJson;
        if (customResponse != null) {
            responseJson = customResponse;
        } else if (isMultiSeriesQuery(query)) {
            responseJson = MOCK_MULTI_SERIES_RESPONSE;
        } else {
            responseJson = MOCK_SINGLE_SERIES_RESPONSE;
        }
        return Json.createReader(new StringReader(responseJson)).readObject();
    }

    /**
     * Detect if the query is a multi-series query based on the "by" clause.
     *
     * @param query the PromQL query
     * @return true if the query groups by labels
     */
    private boolean isMultiSeriesQuery(String query) {
        return query != null && query.contains("by (");
    }

    /**
     * Set whether Prometheus should be available.
     *
     * @param available true to return mock responses, false to throw errors
     */
    public void setAvailable(boolean available) {
        this.available = available;
    }

    /**
     * Check if Prometheus is set as available.
     *
     * @return true if available
     */
    public boolean isAvailable() {
        return available;
    }

    /**
     * Set a custom response JSON to return.
     *
     * @param responseJson the JSON response to return, or null to use the default
     */
    public void setCustomResponse(String responseJson) {
        this.customResponse = responseJson;
    }

    /** Reset to default state (available, default response). */
    public void reset() {
        this.available = true;
        this.customResponse = null;
    }
}
