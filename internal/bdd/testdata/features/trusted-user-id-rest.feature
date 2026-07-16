@api-keys @auth-modes @trusted-user-id
Feature: Trusted client user identity assertion over REST
  As a memory-service operator
  I want selected authenticated clients to assert an effective user
  So remote agent applications can use user-scoped APIs without fabricating user credentials

  Scenario: Trusted API-key client creates a conversation for the asserted user
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" is trusted to assert user IDs
    And memory-service is running with API keys and no OIDC
    And I authenticate with only API key header "agent-api-key-1"
    And I set the "X-User-ID" header to "alice"
    When I call POST "/v1/conversations" with body:
      """
      {"title": "asserted REST user"}
      """
    Then the response status should be 201
    And the response body field "ownerUserId" should be "alice"

  Scenario: Untrusted API-key client ignores X-User-ID
    Given API key "agent-api-key-1" maps to client "agent"
    And memory-service is running with API keys and no OIDC
    And I authenticate with only API key header "agent-api-key-1"
    And I set the "X-User-ID" header to "alice"
    When I call GET "/v1/conversations"
    Then the response status should be 401

  Scenario: Invalid credentials are rejected before a user assertion is considered
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" is trusted to assert user IDs
    And memory-service is running with API keys and no OIDC
    And I authenticate with only API key header "not-a-configured-api-key"
    And I set the "X-User-ID" header to "alice"
    When I call GET "/v1/conversations"
    Then the response status should be 401

  Scenario: Trusted client rejects multiple asserted REST users
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" is trusted to assert user IDs
    And memory-service is running with API keys and no OIDC
    And I authenticate with only API key header "agent-api-key-1"
    And I set the "X-User-ID" header to "alice"
    And I add the "X-User-ID" header value "bob"
    When I call GET "/v1/conversations"
    Then the response status should be 400

  Scenario: Capabilities report trusted user assertion as enabled
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" is trusted to assert user IDs
    And memory-service is running with API keys and no OIDC
    And I authenticate with only API key header "agent-api-key-1"
    When I call GET "/v1/capabilities"
    Then the response status should be 200
    And the response body field "auth.user_id_assertion_enabled" should be "true"

  Scenario: Capabilities report trusted user assertion as disabled by default
    Given API key "agent-api-key-1" maps to client "agent"
    And memory-service is running with API keys and no OIDC
    And I authenticate with only API key header "agent-api-key-1"
    When I call GET "/v1/capabilities"
    Then the response status should be 200
    And the response body field "auth.user_id_assertion_enabled" should be "false"
