# Metrics Design

This document describes the metrics strategy for the memory-service, covering Prometheus metrics for operational monitoring and admin dashboard metrics.

## Goals

1. **Operational Monitoring**: Provide Prometheus metrics for service operators to monitor system health, performance, and reliability.
2. **Admin Dashboard Metrics**: Expose basic metrics for admin UIs without impacting normal operations.
3. **Minimal Overhead**: Avoid table scans, new indexes, or expensive queries for metrics collection.
4. **Multi-Replica Friendly**: Design for stateless, horizontally-scaled deployments where metrics are aggregated across replicas.

## Architecture

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│ memory-service  │     │ memory-service  │     │ memory-service  │
│   (replica 1)   │     │   (replica 2)   │     │   (replica N)   │
│                 │     │                 │     │                 │
│  /q/metrics     │     │  /q/metrics     │     │  /q/metrics     │
└────────┬────────┘     └────────┬────────┘     └────────┬────────┘
         │                       │                       │
         └───────────────────────┼───────────────────────┘
                                 │ scrape
                                 ▼
                       ┌─────────────────┐
                       │   Prometheus    │
                       │                 │
                       │  - aggregation  │
                       │  - time-series  │
                       │  - alerting     │
                       └────────┬────────┘
                                │ PromQL queries
                                ▼
                       ┌─────────────────┐
                       │ memory-service  │
                       │ /v1/admin/stats │◄──── Admin Dashboard
                       └─────────────────┘
```

The memory-service delegates metric aggregation to Prometheus:

1. **Each replica** exposes operational metrics at `/q/metrics`
2. **Prometheus** scrapes all replicas and handles aggregation
3. **Admin API** queries Prometheus via PromQL for dashboard stats
4. **Service stays stateless** - no in-memory aggregation or background polling

## Design Principles

### Why Delegate to Prometheus?

| Approach | Pros | Cons |
|----------|------|------|
| **In-service aggregation** | No external dependency | Requires coordination across replicas, adds state |
| **Database queries** | Accurate counts | Table scans, performance impact |
| **Prometheus delegation** | Truly stateless, already aggregated, time-series for free | Requires Prometheus availability |

Since operators already need Prometheus for alerting and monitoring, leveraging it for admin dashboard metrics is the natural choice.

### Metric Categories

| Category | Purpose | Collection Method |
|----------|---------|-------------------|
| RED Metrics | Request rate, errors, duration | In-request instrumentation |
| Cache Metrics | Cache effectiveness | Counter increment on cache ops |
| Data Store Metrics | Database operation health | Timed operation wrappers |
| Admin Dashboard | Aggregated stats | Query Prometheus via PromQL |

## Prometheus Metrics (Operational)

These metrics follow the RED method (Rate, Errors, Duration) and are essential for service operators.

### HTTP/REST Metrics

Quarkus Micrometer automatically provides these via `quarkus-micrometer-registry-prometheus`:

```
# Request rate and duration
http_server_requests_seconds_count{method="GET",uri="/v1/conversations",status="200"}
http_server_requests_seconds_sum{method="GET",uri="/v1/conversations",status="200"}
http_server_requests_seconds_max{method="GET",uri="/v1/conversations",status="200"}

# Error rates visible via status label
http_server_requests_seconds_count{method="POST",uri="/v1/conversations/{id}/entries",status="500"}
```

### gRPC Metrics

Quarkus provides gRPC metrics automatically:

```
grpc_server_processing_duration_seconds{method="ListConversations",service="Conversations"}
grpc_server_requests_total{method="ListConversations",service="Conversations",status="OK"}
```

### Cache Metrics (Existing)

Already implemented in [RedisMemoryEntriesCache.java](../memory-service/src/main/java/io/github/chirino/memory/cache/RedisMemoryEntriesCache.java) and [InfinispanMemoryEntriesCache.java](../memory-service/src/main/java/io/github/chirino/memory/cache/InfinispanMemoryEntriesCache.java):

```
memory_entries_cache_hits_total{backend="redis"}
memory_entries_cache_misses_total{backend="infinispan"}
memory_entries_cache_errors_total{backend="redis"}
```

### Data Store Metrics (New)

Add timing and counting metrics for data store operations:

```java
@ApplicationScoped
public class MeteredMemoryStore implements MemoryStore {

    @Inject
    MeterRegistry registry;

    @Inject
    @Named("delegate")
    MemoryStore delegate;

    @Override
    public Conversation createConversation(String userId, CreateConversationRequest request) {
        return registry.timer("memory.store.operation", "operation", "createConversation")
            .record(() -> delegate.createConversation(userId, request));
    }

    @Override
    public List<Conversation> listConversations(String userId, ListConversationsRequest request) {
        return registry.timer("memory.store.operation", "operation", "listConversations")
            .record(() -> delegate.listConversations(userId, request));
    }

    // ... wrap all operations
}
```

Resulting metrics:

```
memory_store_operation_seconds_count{operation="createConversation"}
memory_store_operation_seconds_sum{operation="createConversation"}
memory_store_operation_seconds_max{operation="createConversation"}
memory_store_operation_seconds_count{operation="appendAgentEntries"}
```

### Task Processing Metrics (New)

Track background task processing:

```
memory_tasks_processed_total{task_type="embedding",status="success"}
memory_tasks_processed_total{task_type="embedding",status="failure"}
memory_tasks_processing_duration_seconds{task_type="embedding"}
memory_tasks_queue_depth{task_type="embedding"}  # gauge, sampled periodically
```

### Connection Pool Metrics

Quarkus/Agroal provides database connection pool metrics automatically:

```
agroal_active_count{datasource="default"}
agroal_available_count{datasource="default"}
agroal_awaiting_count{datasource="default"}
```

## Admin Dashboard Metrics

The admin API provides **typed endpoints** with predefined PromQL queries for specific use cases. This approach provides:
- **Type safety**: Well-defined response DTOs instead of raw Prometheus JSON
- **Security**: No arbitrary query execution - only known, audited queries
- **Simplicity**: Frontend doesn't need PromQL knowledge

### Optional Feature

Prometheus integration is **optional**. When `memory-service.prometheus.url` is not configured:
- Admin stats endpoints return **501 Not Implemented**
- All other memory-service functionality works normally
- Operators can still scrape `/q/metrics` directly with their own Prometheus instance

### Admin Stats Endpoints

Each endpoint executes a predefined PromQL query and returns typed results:

| Endpoint | Description | Query Type |
|----------|-------------|------------|
| `GET /v1/admin/stats/request-rate` | HTTP request rate per second | Range (time-series) |
| `GET /v1/admin/stats/error-rate` | 5xx error percentage | Range (time-series) |
| `GET /v1/admin/stats/latency-p95` | P95 response latency in seconds | Range (time-series) |
| `GET /v1/admin/stats/cache-hit-rate` | Cache hit percentage | Range (time-series) |

**Common Query Parameters** (for range queries):

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `start` | ISO 8601 timestamp | 1 hour ago | Start of time range |
| `end` | ISO 8601 timestamp | now | End of time range |
| `step` | duration (e.g., `60s`, `5m`) | `60s` | Resolution step |

### Response DTOs

```java
// OpenAPI-generated DTO for time-series data
public class TimeSeriesResponse {
    private List<TimeSeriesDataPoint> data;
    private String metric;  // e.g., "request_rate", "error_rate"
    private String unit;    // e.g., "requests/sec", "percent", "seconds"
}

public class TimeSeriesDataPoint {
    private OffsetDateTime timestamp;
    private Double value;
}
```

Example response for `/v1/admin/stats/request-rate`:

```json
{
  "metric": "request_rate",
  "unit": "requests/sec",
  "data": [
    {"timestamp": "2024-01-01T10:00:00Z", "value": 42.5},
    {"timestamp": "2024-01-01T10:01:00Z", "value": 45.2},
    {"timestamp": "2024-01-01T10:02:00Z", "value": 47.8}
  ]
}
```

### Implementation

```java
@Path("/v1/admin/stats")
@Authenticated
@Produces(MediaType.APPLICATION_JSON)
public class AdminStatsResource {

    @ConfigProperty(name = "memory-service.prometheus.url")
    Optional<String> prometheusUrl;

    @Inject
    @RestClient
    PrometheusClient prometheusClient;

    @Inject
    AdminRoleResolver roleResolver;

    @Inject
    SecurityIdentity identity;

    @Inject
    ApiKeyContext apiKeyContext;

    // Predefined PromQL queries
    private static final String REQUEST_RATE_QUERY =
        "sum(rate(http_server_requests_seconds_count[5m]))";
    private static final String ERROR_RATE_QUERY =
        "sum(rate(http_server_requests_seconds_count{status=~\"5..\"}[5m])) / " +
        "sum(rate(http_server_requests_seconds_count[5m])) * 100";
    private static final String LATENCY_P95_QUERY =
        "histogram_quantile(0.95, sum(rate(http_server_requests_seconds_bucket[5m])) by (le))";
    private static final String CACHE_HIT_RATE_QUERY =
        "sum(rate(memory_entries_cache_hits_total[5m])) / " +
        "(sum(rate(memory_entries_cache_hits_total[5m])) + " +
        "sum(rate(memory_entries_cache_misses_total[5m]))) * 100";

    @GET
    @Path("/request-rate")
    public Response getRequestRate(
            @QueryParam("start") String start,
            @QueryParam("end") String end,
            @QueryParam("step") @DefaultValue("60s") String step) {
        return executeRangeQuery(REQUEST_RATE_QUERY, "request_rate", "requests/sec", start, end, step);
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
        return executeRangeQuery(CACHE_HIT_RATE_QUERY, "cache_hit_rate", "percent", start, end, step);
    }

    private Response executeRangeQuery(
            String query, String metric, String unit,
            String start, String end, String step) {
        try {
            roleResolver.requireAuditor(identity, apiKeyContext);
            checkPrometheusConfigured();

            // Default time range: last hour
            String resolvedStart = start != null ? start : Instant.now().minus(1, ChronoUnit.HOURS).toString();
            String resolvedEnd = end != null ? end : Instant.now().toString();

            JsonObject prometheusResponse = prometheusClient.queryRange(query, resolvedStart, resolvedEnd, step);
            TimeSeriesResponse response = convertToTimeSeries(prometheusResponse, metric, unit);
            return Response.ok(response).build();
        } catch (AccessDeniedException e) {
            return forbidden(e);
        } catch (PrometheusUnavailableException e) {
            return serviceUnavailable(e);
        }
    }

    private TimeSeriesResponse convertToTimeSeries(JsonObject prometheus, String metric, String unit) {
        // Convert Prometheus matrix result to typed response
        TimeSeriesResponse response = new TimeSeriesResponse();
        response.setMetric(metric);
        response.setUnit(unit);

        List<TimeSeriesDataPoint> data = new ArrayList<>();
        JsonArray results = prometheus.getJsonObject("data").getJsonArray("result");
        if (!results.isEmpty()) {
            JsonArray values = results.getJsonObject(0).getJsonArray("values");
            for (int i = 0; i < values.size(); i++) {
                JsonArray point = values.getJsonArray(i);
                TimeSeriesDataPoint dp = new TimeSeriesDataPoint();
                dp.setTimestamp(Instant.ofEpochSecond(point.getJsonNumber(0).longValue()).atOffset(ZoneOffset.UTC));
                dp.setValue(Double.parseDouble(point.getString(1)));
                data.add(dp);
            }
        }
        response.setData(data);
        return response;
    }

    private void checkPrometheusConfigured() {
        if (prometheusUrl.isEmpty()) {
            throw new PrometheusNotConfiguredException();
        }
    }
}
```

### Prometheus Client

```java
@RegisterRestClient(configKey = "prometheus")
@Path("/api/v1")
public interface PrometheusClient {

    @GET
    @Path("/query_range")
    @Produces(MediaType.APPLICATION_JSON)
    JsonObject queryRange(
        @QueryParam("query") String query,
        @QueryParam("start") String start,
        @QueryParam("end") String end,
        @QueryParam("step") String step);
}
```

### Configuration

```properties
# Optional: Prometheus server URL for admin stats queries
# If not set, admin stats endpoints return 501 Not Implemented
memory-service.prometheus.url=http://prometheus:9090

# REST client configuration (only used when prometheus.url is set)
quarkus.rest-client.prometheus.url=${memory-service.prometheus.url:http://localhost:9090}
quarkus.rest-client.prometheus.scope=jakarta.inject.Singleton
```

### Error Handling

| Scenario | HTTP Status | Response |
|----------|-------------|----------|
| Prometheus not configured | 501 Not Implemented | `{"error": "prometheus_not_configured", "message": "Set memory-service.prometheus.url to enable admin stats."}` |
| Prometheus unavailable | 503 Service Unavailable | `{"error": "prometheus_unavailable", "message": "Could not connect to Prometheus server."}` |
| Access denied | 403 Forbidden | `{"error": "forbidden", "message": "Auditor role required."}` |

## Metric Naming Conventions

Follow Prometheus naming conventions:

- **Prefix**: `memory_` for all custom metrics
- **Suffix**: `_total` for counters, `_seconds` for durations, `_bytes` for sizes
- **Labels**: Use lowercase with underscores (`operation`, `backend`, `status`)

Examples:
```
memory_store_operation_seconds_count{operation="create_conversation"}
memory_entries_cache_hits_total{backend="redis"}
memory_tasks_processed_total{task_type="embedding",status="success"}
memory_tasks_queue_depth{task_type="embedding"}
```

## Configuration

```properties
# Enable/disable metrics endpoint (default: enabled via quarkus-micrometer-registry-prometheus)
quarkus.micrometer.export.prometheus.enabled=true

# Metrics endpoint path
quarkus.micrometer.export.prometheus.path=/q/metrics

# Histogram buckets for latency metrics
quarkus.micrometer.binder.http-server.percentiles=0.5,0.95,0.99

# Optional: Enable admin stats by configuring Prometheus URL
# memory-service.prometheus.url=http://prometheus:9090
```

## Deployment Considerations

### Multi-Replica Aggregation

Since the service is stateless with multiple replicas:

1. **Prometheus scrapes all replicas** individually
2. **Counters are additive**: Use `sum()` in PromQL to aggregate across replicas
3. **Histograms work correctly**: Prometheus properly aggregates histogram buckets
4. **Gauges (connection pools, etc.)**: Use `sum()` for totals across replicas

Example Prometheus queries:

```promql
# Total request rate across all replicas
sum(rate(http_server_requests_seconds_count{uri="/v1/conversations"}[5m]))

# P95 latency (aggregated histogram)
histogram_quantile(0.95, sum(rate(http_server_requests_seconds_bucket{uri="/v1/conversations"}[5m])) by (le))

# Error rate
sum(rate(http_server_requests_seconds_count{status=~"5.."}[5m])) / sum(rate(http_server_requests_seconds_count[5m]))

# Cache hit rate
sum(rate(memory_entries_cache_hits_total[5m])) / (sum(rate(memory_entries_cache_hits_total[5m])) + sum(rate(memory_entries_cache_misses_total[5m])))
```

### Grafana Dashboard

Recommended panels for a service operator dashboard:

1. **Request Rate**: `sum(rate(http_server_requests_seconds_count[5m])) by (uri)`
2. **Error Rate**: `sum(rate(http_server_requests_seconds_count{status=~"5.."}[5m])) by (uri)`
3. **P95 Latency**: Histogram quantile by endpoint
4. **Cache Hit Rate**: `sum(rate(memory_entries_cache_hits_total[5m])) / (sum(rate(memory_entries_cache_hits_total[5m])) + sum(rate(memory_entries_cache_misses_total[5m])))`
5. **Database Operation Latency**: `histogram_quantile(0.95, sum(rate(memory_store_operation_seconds_bucket[5m])) by (le, operation))`
6. **Active Connections**: `sum(agroal_active_count)`
7. **Task Queue Depth**: `sum(memory_tasks_queue_depth) by (task_type)`

### Alerts

Recommended alerting rules:

```yaml
groups:
  - name: memory-service
    rules:
      - alert: HighErrorRate
        expr: sum(rate(http_server_requests_seconds_count{status=~"5.."}[5m])) / sum(rate(http_server_requests_seconds_count[5m])) > 0.05
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "High error rate in memory-service"

      - alert: HighLatency
        expr: histogram_quantile(0.95, sum(rate(http_server_requests_seconds_bucket[5m])) by (le)) > 2
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "P95 latency exceeds 2 seconds"

      - alert: CacheHitRateLow
        expr: sum(rate(memory_entries_cache_hits_total[5m])) / (sum(rate(memory_entries_cache_hits_total[5m])) + sum(rate(memory_entries_cache_misses_total[5m]))) < 0.5
        for: 15m
        labels:
          severity: warning
        annotations:
          summary: "Cache hit rate below 50%"

      - alert: TaskQueueBacklog
        expr: sum(memory_tasks_queue_depth) > 1000
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: "Task queue backlog growing"
```

## Implementation Plan

### Phase 1: OpenAPI Contract & DTOs

**Files to create/modify:**
- `memory-service-contracts/openapi/admin-api.yaml` - Add stats endpoints

**OpenAPI additions:**

```yaml
paths:
  /v1/admin/stats/request-rate:
    get:
      operationId: getRequestRate
      summary: Get HTTP request rate over time
      tags: [Admin Stats]
      security:
        - oauth2: [auditor]
        - apiKey: []
      parameters:
        - $ref: '#/components/parameters/StatsStart'
        - $ref: '#/components/parameters/StatsEnd'
        - $ref: '#/components/parameters/StatsStep'
      responses:
        '200':
          description: Time series data
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/TimeSeriesResponse'
        '501':
          description: Prometheus not configured
        '503':
          description: Prometheus unavailable

  /v1/admin/stats/error-rate:
    get:
      operationId: getErrorRate
      summary: Get 5xx error rate percentage over time
      # ... similar structure

  /v1/admin/stats/latency-p95:
    get:
      operationId: getLatencyP95
      summary: Get P95 response latency over time
      # ... similar structure

  /v1/admin/stats/cache-hit-rate:
    get:
      operationId: getCacheHitRate
      summary: Get cache hit rate percentage over time
      # ... similar structure

components:
  parameters:
    StatsStart:
      name: start
      in: query
      schema:
        type: string
        format: date-time
      description: Start of time range (default: 1 hour ago)
    StatsEnd:
      name: end
      in: query
      schema:
        type: string
        format: date-time
      description: End of time range (default: now)
    StatsStep:
      name: step
      in: query
      schema:
        type: string
        default: "60s"
      description: Resolution step (e.g., 60s, 5m)

  schemas:
    TimeSeriesResponse:
      type: object
      required: [metric, unit, data]
      properties:
        metric:
          type: string
          description: Metric identifier (e.g., "request_rate")
        unit:
          type: string
          description: Unit of measurement (e.g., "requests/sec", "percent")
        data:
          type: array
          items:
            $ref: '#/components/schemas/TimeSeriesDataPoint'

    TimeSeriesDataPoint:
      type: object
      required: [timestamp, value]
      properties:
        timestamp:
          type: string
          format: date-time
        value:
          type: number
          format: double
```

**Tasks:**
- [ ] Add endpoints to `admin-api.yaml`
- [ ] Add `TimeSeriesResponse` and `TimeSeriesDataPoint` schemas
- [ ] Add query parameters for time range
- [ ] Run OpenAPI code generation: `./mvnw generate-sources`

### Phase 2: Prometheus Client

**Files to create:**
- `memory-service/src/main/java/io/github/chirino/memory/prometheus/PrometheusClient.java`
- `memory-service/src/main/java/io/github/chirino/memory/prometheus/PrometheusNotConfiguredException.java`
- `memory-service/src/main/java/io/github/chirino/memory/prometheus/PrometheusUnavailableException.java`

**Tasks:**
- [ ] Add `quarkus-rest-client-jackson` dependency to `pom.xml`
- [ ] Create `PrometheusClient` REST client interface
- [ ] Create exception classes for error handling
- [ ] Add configuration properties to `application.properties`

### Phase 3: Admin Stats Resource

**Files to create:**
- `memory-service/src/main/java/io/github/chirino/memory/api/AdminStatsResource.java`

**Tasks:**
- [ ] Create `AdminStatsResource` with typed endpoints
- [ ] Implement Prometheus response to DTO conversion
- [ ] Add role-based access control (auditor role)
- [ ] Handle Prometheus not configured (501)
- [ ] Handle Prometheus unavailable (503)

### Phase 4: Cucumber Tests

**Testing Strategy:** Mock the `PrometheusClient` - no Prometheus devservice needed.

The existing test infrastructure already supports mocking via CDI alternatives. We mock `PrometheusClient` to return canned Prometheus responses.

**Files to create:**
- `memory-service/src/test/java/io/github/chirino/memory/prometheus/MockPrometheusClient.java`
- `memory-service/src/test/resources/features/admin-stats-rest.feature`

**Mock Implementation:**

```java
@Alternative
@Priority(1)
@ApplicationScoped
public class MockPrometheusClient implements PrometheusClient {

    // Canned Prometheus response for testing
    private static final String MOCK_RESPONSE = """
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

    private boolean available = true;

    @Override
    public JsonObject queryRange(String query, String start, String end, String step) {
        if (!available) {
            throw new WebApplicationException("Prometheus unavailable", 503);
        }
        return Json.createReader(new StringReader(MOCK_RESPONSE)).readObject();
    }

    // Methods for test control
    public void setAvailable(boolean available) {
        this.available = available;
    }
}
```

**Cucumber Feature:**

```gherkin
@admin-stats
Feature: Admin Stats API

  Background:
    Given the authenticated user is "alice"
    And alice has the "auditor" role

  Scenario: Get request rate returns time series data
    When I GET "/v1/admin/stats/request-rate"
    Then the response status should be 200
    And the response should contain "metric" with value "request_rate"
    And the response should contain "unit" with value "requests/sec"
    And the response "data" array should have at least 1 items

  Scenario: Get error rate returns percentage data
    When I GET "/v1/admin/stats/error-rate"
    Then the response status should be 200
    And the response should contain "metric" with value "error_rate"
    And the response should contain "unit" with value "percent"

  Scenario: Get cache hit rate returns percentage data
    When I GET "/v1/admin/stats/cache-hit-rate"
    Then the response status should be 200
    And the response should contain "metric" with value "cache_hit_rate"
    And the response should contain "unit" with value "percent"

  Scenario: Get P95 latency returns seconds data
    When I GET "/v1/admin/stats/latency-p95"
    Then the response status should be 200
    And the response should contain "metric" with value "latency_p95"
    And the response should contain "unit" with value "seconds"

  Scenario: Stats endpoints require auditor role
    Given the authenticated user is "bob"
    And bob does not have the "auditor" role
    When I GET "/v1/admin/stats/request-rate"
    Then the response status should be 403

  Scenario: Stats return 501 when Prometheus not configured
    Given Prometheus is not configured
    When I GET "/v1/admin/stats/request-rate"
    Then the response status should be 501
    And the response should contain "error" with value "prometheus_not_configured"

  Scenario: Stats return 503 when Prometheus unavailable
    Given Prometheus is unavailable
    When I GET "/v1/admin/stats/request-rate"
    Then the response status should be 503
    And the response should contain "error" with value "prometheus_unavailable"

  Scenario: Custom time range parameters work
    When I GET "/v1/admin/stats/request-rate?start=2024-01-01T00:00:00Z&end=2024-01-01T01:00:00Z&step=5m"
    Then the response status should be 200
```

**Step Definitions to add:**

```java
@Given("Prometheus is not configured")
public void prometheusIsNotConfigured() {
    // Set mock to indicate no Prometheus URL configured
    mockPrometheusClient.setConfigured(false);
}

@Given("Prometheus is unavailable")
public void prometheusIsUnavailable() {
    mockPrometheusClient.setAvailable(false);
}
```

**Tasks:**
- [ ] Create `MockPrometheusClient` as CDI alternative
- [ ] Register mock in test profile
- [ ] Create `admin-stats-rest.feature`
- [ ] Add step definitions for Prometheus state control
- [ ] Add step definitions for time series response validation

### Phase 5: Verify Integration

**Manual verification steps:**
- [ ] Start memory-service in dev mode
- [ ] Verify `/q/metrics` endpoint returns Prometheus format
- [ ] Verify admin stats return 501 without Prometheus URL
- [ ] Configure Prometheus URL and verify stats work

### Future Enhancements

#### Datastore Metrics (Add after MeteredMemoryStore)

These endpoints should be added as soon as `MeteredMemoryStore` is implemented. They provide visibility into database operations and connection pool health.

| Endpoint | PromQL Query | Description |
|----------|--------------|-------------|
| `/v1/admin/stats/db-pool-utilization` | `agroal_active_count / (agroal_active_count + agroal_available_count) * 100` | Database connection pool utilization % |
| `/v1/admin/stats/store-latency-p95` | `histogram_quantile(0.95, sum(rate(memory_store_operation_seconds_bucket[5m])) by (le, operation))` | Store operation P95 latency by type |
| `/v1/admin/stats/store-throughput` | `sum(rate(memory_store_operation_seconds_count[5m])) by (operation)` | Store operations/sec by type |

**Multi-series response schema** (for endpoints returning data per operation):

```yaml
MultiSeriesResponse:
  type: object
  required: [metric, unit, series]
  properties:
    metric:
      type: string
    unit:
      type: string
    series:
      type: array
      items:
        type: object
        required: [label, data]
        properties:
          label:
            type: string
            description: Series label (e.g., operation name)
          data:
            type: array
            items:
              $ref: '#/components/schemas/TimeSeriesDataPoint'
```

Example response for `/v1/admin/stats/store-throughput`:

```json
{
  "metric": "store_throughput",
  "unit": "operations/sec",
  "series": [
    {
      "label": "createConversation",
      "data": [
        {"timestamp": "2024-01-01T10:00:00Z", "value": 5.2},
        {"timestamp": "2024-01-01T10:01:00Z", "value": 4.8}
      ]
    },
    {
      "label": "appendUserEntries",
      "data": [
        {"timestamp": "2024-01-01T10:00:00Z", "value": 42.5},
        {"timestamp": "2024-01-01T10:01:00Z", "value": 45.2}
      ]
    }
  ]
}
```

#### Other Future Endpoints

| Endpoint | PromQL Query | Description |
|----------|--------------|-------------|
| `/v1/admin/stats/conversations-created` | `increase(memory_store_operation_seconds_count{operation="createConversation"}[24h])` | Conversations created in time range |
| `/v1/admin/stats/messages-stored` | `increase(memory_store_operation_seconds_count{operation=~"append.*"}[24h])` | Messages stored in time range |
| `/v1/admin/stats/grpc-request-rate` | `sum(rate(grpc_server_requests_total[5m]))` | gRPC request rate |

## Summary

| Metric Type | Collection Method | Overhead |
|-------------|-------------------|----------|
| HTTP/gRPC metrics | Automatic (Quarkus) | Minimal |
| Cache metrics | Counter increment | Minimal |
| Store operation timing | Timer wrapper | Minimal |
| Task processing | Counter increment | Minimal |
| Admin dashboard stats | Query Prometheus | None on memory-service |

### Key Design Decisions

1. **Typed endpoints over generic query proxy**: Security (no arbitrary PromQL), type safety, simpler frontend
2. **Mock PrometheusClient for tests**: No Prometheus devservice needed, fast tests, controllable scenarios
3. **Optional Prometheus**: Service fully functional without it; admin stats gracefully return 501
4. **Start small**: 4 core metrics initially, easy to add more endpoints later

This design ensures comprehensive observability while keeping the memory-service stateless and avoiding database overhead for metrics collection.
