@admin-stats
Feature: Admin Stats REST API
  As an administrator or auditor
  I want to view operational statistics via REST API
  So that I can monitor service health through admin dashboards

  Background:
    Given I am authenticated as admin user "alice"

  # Serial required: this scenario reads through the shared Prometheus mock on the shared stats server, so a concurrent scenario can flip availability underneath it.
  Scenario: Get request rate returns time series data
    When I call GET "/v1/admin/stats/request-rate"
    Then the response status should be 200
    And the response should be a time series with metric "request_rate"
    And the response should be a time series with unit "requests/sec"
    And the response time series data should have at least 1 points

  # Serial required: this scenario reads through the shared Prometheus mock on the shared stats server, so a concurrent scenario can flip availability underneath it.
  Scenario: Get error rate returns percentage data
    When I call GET "/v1/admin/stats/error-rate"
    Then the response status should be 200
    And the response should be a time series with metric "error_rate"
    And the response should be a time series with unit "percent"

  # Serial required: this scenario reads through the shared Prometheus mock on the shared stats server, so a concurrent scenario can flip availability underneath it.
  Scenario: Get P95 latency returns seconds data
    When I call GET "/v1/admin/stats/latency-p95"
    Then the response status should be 200
    And the response should be a time series with metric "latency_p95"
    And the response should be a time series with unit "seconds"

  # Serial required: this scenario reads through the shared Prometheus mock on the shared stats server, so a concurrent scenario can flip availability underneath it.
  Scenario: Get cache hit rate returns percentage data
    When I call GET "/v1/admin/stats/cache-hit-rate"
    Then the response status should be 200
    And the response should be a time series with metric "cache_hit_rate"
    And the response should be a time series with unit "percent"

  # Serial required: this scenario reads through the shared Prometheus mock on the shared stats server, so a concurrent scenario can flip availability underneath it.
  Scenario: Auditor can access stats endpoints
    Given I am authenticated as auditor user "charlie"
    When I call GET "/v1/admin/stats/request-rate"
    Then the response status should be 200
    And the response should be a time series with metric "request_rate"

  # Serial today only because this feature shares the serial stats runner; this scenario is a pure authorization check and appears parallel-safe.
  Scenario: Non-admin user receives 403 Forbidden
    Given I am authenticated as user "bob"
    When I call GET "/v1/admin/stats/request-rate"
    Then the response status should be 403

  # Serial today only because this feature shares the serial stats runner; this scenario only checks that summary fields are present and appears parallel-safe.
  Scenario: Admin can fetch datastore-backed summary stats
    And there is a conversation owned by "bob" with title "Summary archive probe"
    And set "summaryConversationId" to "${conversationId}"
    Given I am authenticated as user "alice"
    And I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "summary"],
      "key": "to-delete",
      "value": { "x": 1 }
    }
    """
    And I call PATCH "/v1/memories?ns=user&ns=alice&ns=summary&key=to-delete" with body:
    """
    {
      "archived": true
    }
    """
    And I am authenticated as admin user "alice"
    When I call PATCH "/v1/admin/conversations/${summaryConversationId}" with body:
    """
    {
      "archived": true,
      "justification": "Create deleted data for summary stats"
    }
    """
    Then the response status should be 200
    When I call GET "/v1/admin/stats/summary"
    Then the response status should be 200
    And the response body field "conversationGroups.total" should not be null
    And the response body field "conversationGroups.archived" should not be null
    And the response body field "conversationGroups.oldestArchivedAt" should not be null
    And the response body field "conversations.total" should not be null
    And the response body field "entries.total" should not be null
    And the response body field "memories.total" should not be null
    And the response body field "memories.archived" should not be null
    And the response body field "memories.oldestArchivedAt" should not be null
    And the response body field "outboxEvents" should be null

  # Serial today only because this feature shares the serial stats runner; this scenario only checks that summary fields are present and appears parallel-safe.
  Scenario: Auditor can fetch datastore-backed summary stats
    Given I am authenticated as auditor user "charlie"
    When I call GET "/v1/admin/stats/summary"
    Then the response status should be 200

  # Serial today only because this feature shares the serial stats runner; this scenario is a pure authorization check and appears parallel-safe.
  Scenario: Non-admin user cannot fetch datastore-backed summary stats
    Given I am authenticated as user "bob"
    When I call GET "/v1/admin/stats/summary"
    Then the response status should be 403

  # Serial required: this scenario intentionally flips the shared Prometheus mock to unavailable, which would break concurrent stats scenarios on the same server.
  Scenario: Stats return 503 when Prometheus is unavailable
    Given Prometheus is unavailable
    When I call GET "/v1/admin/stats/request-rate"
    Then the response status should be 503
    And the response body "code" should be "prometheus_unavailable"

  # Serial required: this scenario reads through the shared Prometheus mock on the shared stats server, so a concurrent scenario can flip availability underneath it.
  Scenario: Custom time range parameters work
    When I call GET "/v1/admin/stats/request-rate?start=2024-01-01T00:00:00Z&end=2024-01-01T01:00:00Z&step=5m"
    Then the response status should be 200
    And the response should be a time series with metric "request_rate"

  # Serial required: this scenario intentionally flips the shared Prometheus mock unavailable and back again, which would race any concurrent stats scenario on the same server.
  Scenario: Stats endpoints work after Prometheus becomes available again
    Given Prometheus is unavailable
    When I call GET "/v1/admin/stats/request-rate"
    Then the response status should be 503
    Given Prometheus is available
    When I call GET "/v1/admin/stats/request-rate"
    Then the response status should be 200

  # Datastore metrics (requires MeteredMemoryStore)

  # Serial today only because this feature shares the serial stats runner; this scenario reads datastore-backed metrics and appears parallel-safe.
  Scenario: Get database pool utilization returns percentage data
    When I call GET "/v1/admin/stats/db-pool-utilization"
    Then the response status should be 200
    And the response should be a time series with metric "db_pool_utilization"
    And the response should be a time series with unit "percent"

  # Serial today only because this feature shares the serial stats runner; this scenario reads datastore-backed metrics and appears parallel-safe.
  Scenario: Get store latency P95 returns multi-series data
    When I call GET "/v1/admin/stats/store-latency-p95"
    Then the response status should be 200
    And the response should be a multi-series with metric "store_latency_p95"
    And the response should be a multi-series with unit "seconds"

  # Serial today only because this feature shares the serial stats runner; this scenario reads datastore-backed metrics and appears parallel-safe.
  Scenario: Get store throughput returns multi-series data
    When I call GET "/v1/admin/stats/store-throughput"
    Then the response status should be 200
    And the response should be a multi-series with metric "store_throughput"
    And the response should be a multi-series with unit "operations/sec"
