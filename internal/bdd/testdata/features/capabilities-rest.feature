Feature: Client capabilities REST API
  As an authenticated agent client
  I want to discover configured server capabilities
  So that I can adapt client behavior without probing endpoints

  Scenario: Authenticated client with resolved client context can fetch capabilities
    Given I am authenticated as user "alice"
    And I am authenticated as agent with API key "test-agent-key"
    When I call GET "/v1/capabilities"
    Then the response status should be 200
    And the response body field "version" should not be null
    And the response body field "tech.store" should not be null
    And the response body field "tech.attachments" should not be null
    And the response body field "tech.cache" should not be null
    And the response body field "tech.vector" should not be null
    And the response body field "tech.event_bus" should not be null
    And the response body field "tech.embedder" should not be null
    And the response body field "features.outbox_enabled" should not be null
    And the response body field "features.semantic_search_enabled" should not be null
    And the response body field "features.fulltext_search_enabled" should not be null
    And the response body field "auth.oidc_enabled" should not be null
    And the response body field "auth.api_key_enabled" should not be null
    And the response body field "security.encryption_enabled" should not be null

  Scenario: Authenticated admin without client context can fetch capabilities
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/capabilities"
    Then the response status should be 200
    And the response body field "version" should not be null
    And the response body field "tech.store" should not be null

  Scenario: Authenticated auditor without client context can fetch capabilities
    Given I am authenticated as auditor user "alice"
    When I call GET "/v1/capabilities"
    Then the response status should be 200
    And the response body field "version" should not be null
    And the response body field "tech.store" should not be null

  Scenario: User-only auth without client context is rejected
    Given I am authenticated as user "bob"
    When I call GET "/v1/capabilities"
    Then the response status should be 403
    And the response body field "error" should contain "client context or admin/auditor role"

  Scenario: Missing authentication is rejected
    Given I am not authenticated
    When I call GET "/v1/capabilities"
    Then the response status should be 401
