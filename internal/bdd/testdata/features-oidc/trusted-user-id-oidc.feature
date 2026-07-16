@oidc @auth-modes @trusted-user-id
Feature: Trusted OIDC client user identity assertion
  As a memory-service operator
  I want trusted OIDC clients to select an effective user without carrying user-derived privileges across identities
  So delegated requests remain scoped to the asserted user

  Scenario: Trusted OIDC client overrides the token user on user-scoped APIs
    Given client "memory-service-client" is trusted to assert user IDs
    And memory-service is running with OIDC allowed client "memory-service-client"
    And I login via OIDC as user "alice" with password "alice"
    And I set the "X-User-ID" header to "bob"
    When I call POST "/v1/conversations" with body:
      """
      {"title": "asserted OIDC user"}
      """
    Then the response status should be 201
    And the response body field "ownerUserId" should be "bob"

  Scenario: Untrusted OIDC client ignores the asserted user
    Given memory-service is running with OIDC allowed client "memory-service-client"
    And I login via OIDC as user "alice" with password "alice"
    And I set the "X-User-ID" header to "bob"
    When I call POST "/v1/conversations" with body:
      """
      {"title": "ignored OIDC assertion"}
      """
    Then the response status should be 201
    And the response body field "ownerUserId" should be "alice"

  Scenario: Paired OIDC and API-key credentials use the trusted resolved client
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" is trusted to assert user IDs
    And memory-service is running with OIDC allowed client "memory-service-client" and API keys
    And I authenticate with both OIDC user "bob" and API key "agent-api-key-1"
    And I set the "X-User-ID" header to "alice"
    When I call POST "/v1/conversations" with body:
      """
      {"title": "paired credentials asserted user"}
      """
    Then the response status should be 201
    And the response body field "ownerUserId" should be "alice"

  Scenario: User-derived admin role does not cross an asserted identity
    Given client "memory-service-client" is trusted to assert user IDs
    And memory-service is running with OIDC allowed client "memory-service-client"
    And I login via OIDC as user "alice" with password "alice"
    And I set the "X-User-ID" header to "bob"
    When I call POST "/v1/conversations/index" with body:
      """
      {}
      """
    Then the response status should be 403
    And the response body should contain "indexer or admin role required"

  Scenario: Configured client role survives mixed-credential delegation
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "indexer" role
    And client "agent" is trusted to assert user IDs
    And memory-service is running with OIDC allowed client "memory-service-client" and API keys
    And I authenticate with both OIDC user "bob" and API key "agent-api-key-1"
    And I set the "X-User-ID" header to "alice"
    When I call GET "/v1/conversations/unindexed?limit=10"
    Then the response status should be 200

  Scenario: Admin APIs ignore X-User-ID and retain the authenticated principal
    Given client "memory-service-client" is trusted to assert user IDs
    And memory-service is running with OIDC allowed client "memory-service-client"
    And I login via OIDC as user "alice" with password "alice"
    And I set the "X-User-ID" header to "bob"
    When I call GET "/v1/admin/conversations"
    Then the response status should be 200
