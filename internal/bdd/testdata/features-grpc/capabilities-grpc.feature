Feature: Client capabilities gRPC API
  As an authenticated agent/app client
  I want to discover server capabilities over gRPC
  So that I can adapt to configured backend features

  Scenario: Authenticated client with resolved client context can fetch capabilities
    Given I am authenticated as user "alice"
    And I am authenticated as agent with API key "test-agent-key"
    When I send gRPC request "SystemService/GetCapabilities" with body:
    """
    {}
    """
    Then the gRPC response should not have an error
    And the gRPC response field "version" should not be null
    And the gRPC response field "tech.store" should not be null
    And the gRPC response field "tech.eventBus" should not be null
    And the gRPC response field "features.outboxEnabled" should not be null
    And the gRPC response field "auth.apiKeyEnabled" should not be null
    And the gRPC response field "security.encryptionEnabled" should not be null

  Scenario: Authenticated admin without client context can fetch capabilities
    Given I am authenticated as admin user "alice"
    When I send gRPC request "SystemService/GetCapabilities" with body:
    """
    {}
    """
    Then the gRPC response should not have an error
    And the gRPC response field "version" should not be null
    And the gRPC response field "tech.store" should not be null

  Scenario: Authenticated auditor without client context can fetch capabilities
    Given I am authenticated as auditor user "alice"
    When I send gRPC request "SystemService/GetCapabilities" with body:
    """
    {}
    """
    Then the gRPC response should not have an error
    And the gRPC response field "version" should not be null
    And the gRPC response field "tech.store" should not be null

  Scenario: User-only auth without client context is rejected
    Given I am authenticated as user "bob"
    When I send gRPC request "SystemService/GetCapabilities" with body:
    """
    {}
    """
    Then the gRPC response should have status "PERMISSION_DENIED"
    And the gRPC error message should contain "client context or admin/auditor role required"

  Scenario: Missing authentication is rejected
    Given I am not authenticated
    When I send gRPC request "SystemService/GetCapabilities" with body:
    """
    {}
    """
    Then the gRPC response should have status "UNAUTHENTICATED"
    And the gRPC error message should contain "missing authorization"
