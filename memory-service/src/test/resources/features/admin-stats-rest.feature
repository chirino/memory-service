@admin-stats
Feature: Admin Stats REST API
  As an administrator or auditor
  I want to view operational statistics via REST API
  So that I can monitor service health through admin dashboards

  Background:
    Given I am authenticated as admin user "alice"

  Scenario: Get request rate returns time series data
    When I call GET "/v1/admin/stats/request-rate"
    Then the response status should be 200
    And the response should be a time series with metric "request_rate"
    And the response should be a time series with unit "requests/sec"
    And the response time series data should have at least 1 points

  Scenario: Get error rate returns percentage data
    When I call GET "/v1/admin/stats/error-rate"
    Then the response status should be 200
    And the response should be a time series with metric "error_rate"
    And the response should be a time series with unit "percent"

  Scenario: Get P95 latency returns seconds data
    When I call GET "/v1/admin/stats/latency-p95"
    Then the response status should be 200
    And the response should be a time series with metric "latency_p95"
    And the response should be a time series with unit "seconds"

  Scenario: Get cache hit rate returns percentage data
    When I call GET "/v1/admin/stats/cache-hit-rate"
    Then the response status should be 200
    And the response should be a time series with metric "cache_hit_rate"
    And the response should be a time series with unit "percent"

  Scenario: Auditor can access stats endpoints
    Given I am authenticated as auditor user "charlie"
    When I call GET "/v1/admin/stats/request-rate"
    Then the response status should be 200
    And the response should be a time series with metric "request_rate"

  Scenario: Non-admin user receives 403 Forbidden
    Given I am authenticated as user "bob"
    When I call GET "/v1/admin/stats/request-rate"
    Then the response status should be 403

  Scenario: Stats return 503 when Prometheus is unavailable
    Given Prometheus is unavailable
    When I call GET "/v1/admin/stats/request-rate"
    Then the response status should be 503
    And the response body "code" should be "prometheus_unavailable"

  Scenario: Custom time range parameters work
    When I call GET "/v1/admin/stats/request-rate?start=2024-01-01T00:00:00Z&end=2024-01-01T01:00:00Z&step=5m"
    Then the response status should be 200
    And the response should be a time series with metric "request_rate"

  Scenario: Stats endpoints work after Prometheus becomes available again
    Given Prometheus is unavailable
    When I call GET "/v1/admin/stats/request-rate"
    Then the response status should be 503
    Given Prometheus is available
    When I call GET "/v1/admin/stats/request-rate"
    Then the response status should be 200
