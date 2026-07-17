@api-keys @auth-modes
Feature: No-OIDC API key authentication
  As a memory-service operator
  I want configured API keys to authenticate admin clients without user context
  So processors and small no-OIDC deployments can use one credential without weakening user-scoped APIs

  Scenario: No-OIDC mode accepts API-key-only admin client access
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And memory-service is running with API keys and no OIDC
    And I authenticate with only API key header "agent-api-key-1"
    When I call GET "/v1/admin/conversations"
    Then the response status should be 200

  Scenario: No-OIDC mode rejects API-key-only access to user-scoped APIs
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And memory-service is running with API keys and no OIDC
    And I authenticate with only API key header "agent-api-key-1"
    When I call GET "/v1/conversations"
    Then the response status should be 401

  Scenario: No-OIDC production mode rejects raw bearer user plus API-key client
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And memory-service is running with API keys and no OIDC
    And I authenticate as bearer user "alice" with API key "agent-api-key-1"
    When I call GET "/v1/admin/conversations"
    Then the response status should be 401

  Scenario: No-OIDC mode rejects an invalid API key
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And memory-service is running with API keys and no OIDC
    And I authenticate with only API key header "not-a-configured-api-key"
    When I call GET "/v1/admin/conversations"
    Then the response status should be 401
    And the response body should contain "invalid API key"

  Scenario: Remote HTTP cannot authenticate with embedded MCP marker header
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And memory-service is running with API keys and embedded MCP identity configured
    And I set the "X-Embedded-MCP-Transport" header to "embedded-mcp"
    When I call GET "/v1/conversations"
    Then the response status should be 401

  Scenario: Remote HTTP marker does not bypass admin authentication
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And memory-service is running with API keys and embedded MCP identity configured
    And I set the "X-Embedded-MCP-Transport" header to "embedded-mcp"
    When I call GET "/v1/admin/conversations"
    Then the response status should be 401

  Scenario: Remote HTTP embedded MCP marker is ignored when normal API key auth is present
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And memory-service is running with API keys and embedded MCP identity configured
    And I authenticate with only API key header "agent-api-key-1"
    And I set the "X-Embedded-MCP-Transport" header to "embedded-mcp"
    When I call GET "/v1/admin/conversations"
    Then the response status should be 200
