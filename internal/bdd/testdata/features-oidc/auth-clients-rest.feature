@oidc @auth-modes
Feature: OIDC client and API key authentication
  As a memory-service operator
  I want signed token claims and configured API keys to define accepted clients
  So Keycloak and paired API-key deployments behave predictably without extra auth modes

  # ─── OIDC Optional Client/Audience Filters ───────────────────────────────────
  # The test Keycloak realm maps tokens from the "memory-service-client"
  # confidential client to the custom "memory-service" audience. Audience-only
  # configuration (OIDCAllowedAudiences, no OIDCAllowedClients) must accept
  # tokens whose aud matches and reject tokens whose aud does not match.
  #
  # OIDCAllowedClients and OIDCAllowedAudiences are both optional. When both are
  # unset, any valid token from the configured issuer is accepted by the resolver,
  # and endpoint authorization still applies normally.
  #
  # When both OIDCAllowedClients and OIDCAllowedAudiences are configured, both checks
  # must pass independently.

  Scenario: OIDC mode accepts a valid token without a client filter
    Given memory-service is running with OIDC and no allowed client filter
    And I login via OIDC as user "bob" with password "bob"
    When I call GET "/v1/conversations"
    Then the response status should be 200

  Scenario: Audience-only mode accepts a token whose audience matches
    Given memory-service is running with OIDC allowed audience "memory-service"
    And I login via OIDC as user "bob" with password "bob"
    When I call GET "/v1/conversations"
    Then the response status should be 200

  Scenario: Audience-only mode rejects a token whose audience does not match
    Given memory-service is running with OIDC allowed audience "other-resource-server"
    And I login via OIDC as user "bob" with password "bob"
    When I call GET "/v1/conversations"
    Then the response status should be 403

  Scenario: Both client and audience configured — matching both is accepted
    Given memory-service is running with OIDC allowed client "memory-service-client" and allowed audience "memory-service"
    And I login via OIDC as user "bob" with password "bob"
    When I call GET "/v1/conversations"
    Then the response status should be 200

  Scenario: Both client and audience configured — wrong audience is rejected even when client matches
    Given memory-service is running with OIDC allowed client "memory-service-client" and allowed audience "other-resource-server"
    And I login via OIDC as user "bob" with password "bob"
    When I call GET "/v1/conversations"
    Then the response status should be 403

  # ─── OIDC Resource/API Scope Gates ───────────────────────────────────────────
  # Resource scopes are additional gates for OIDC-bearing requests only. They do
  # not grant roles or membership by themselves; normal user, admin, auditor,
  # indexer, API-key, and ownership checks still run.

  Scenario: OIDC admin role without admin conversation read scope is denied
    Given memory-service is running with OIDC allowed client "memory-service-client" and resource scopes:
      | permission                 | scopes                                     |
      | admin_conversations_read   | memory-service:admin-conversations-read    |
    And I login via OIDC as user "alice" with scopes "openid roles"
    When I call GET "/v1/admin/conversations"
    Then the response status should be 403

  Scenario: Coarse admin scope allows admin read and write
    Given memory-service is running with OIDC allowed client "memory-service-client" and resource scopes:
      | permission | scopes |
      | admin      | openid |
    And I login via OIDC as user "bob" with scopes "openid roles"
    And I have a conversation with title "Admin aggregate target"
    And I login via OIDC as user "alice" with scopes "openid roles"
    When I call GET "/v1/admin/conversations"
    Then the response status should be 200
    When I call PATCH "/v1/admin/conversations/${conversationId}" with body:
      """
      {"archived": true}
      """
    Then the response status should be 200

  Scenario: OIDC user read scope can read conversations but cannot write without write scope
    Given memory-service is running with OIDC allowed client "memory-service-client" and resource scopes:
      | permission | scopes                    |
      | user_read  | openid                    |
      | user_write | memory-service:user-write |
    And I login via OIDC as user "bob" with scopes "openid roles"
    When I call GET "/v1/conversations"
    Then the response status should be 200
    When I call POST "/v1/conversations" with body:
      """
      {"title": "blocked write"}
      """
    Then the response status should be 403

  Scenario: API-key-only admin client ignores OIDC resource scope gates
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And memory-service is running with OIDC allowed client "memory-service-client" and API keys and resource scopes:
      | permission               | scopes                                  |
      | admin_conversations_read | memory-service:admin-conversations-read |
    And I authenticate with only API key header "agent-api-key-1"
    When I call GET "/v1/admin/conversations"
    Then the response status should be 200

  Scenario: gRPC user resource scope gate denies a missing read scope
    Given memory-service is running with OIDC allowed client "memory-service-client" and resource scopes:
      | permission         | scopes                            |
      | conversations_read | memory-service:conversations-read |
    And I login via OIDC as user "bob" with scopes "openid roles"
    When I send gRPC request "ConversationsService/ListConversations" with body:
      """
      """
    Then the gRPC response should have status "PERMISSION_DENIED"
    And the gRPC error message should contain "conversations_read"

  Scenario: gRPC aggregate admin conversation scope allows admin reads
    Given memory-service is running with OIDC allowed client "memory-service-client" and resource scopes:
      | permission          | scopes |
      | admin_conversations | openid |
    And I login via OIDC as user "alice" with scopes "openid roles"
    When I send gRPC request "AdminConversationsService/ListConversations" with body:
      """
      """
    Then the gRPC response should not have an error

  Scenario: OIDC-only mode accepts a Keycloak user token
    Given memory-service is running with OIDC allowed client "memory-service-client"
    And I login via OIDC as user "bob" with password "bob"
    When I call GET "/v1/conversations"
    Then the response status should be 200

  Scenario: OIDC-only mode rejects raw bearer values
    Given memory-service is running with OIDC allowed client "memory-service-client"
    And I set the "Authorization" header to "Bearer bob"
    When I call GET "/v1/conversations"
    Then the response status should be 401
    And the response body should contain "invalid"

  Scenario: OIDC-only mode rejects a token from a disallowed client
    Given memory-service is running with OIDC allowed client "frontend"
    And I login via OIDC as user "bob" with password "bob"
    When I call GET "/v1/conversations"
    Then the response status should be 403

  Scenario: Mixed mode accepts a Keycloak admin token
    Given API key "agent-api-key-1" maps to client "agent"
    And memory-service is running with OIDC allowed client "memory-service-client" and API keys
    And I login via OIDC as user "alice" with password "alice"
    When I call GET "/v1/admin/conversations"
    Then the response status should be 200

  Scenario: OIDC mixed mode rejects raw bearer user plus API-key client
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And memory-service is running with OIDC allowed client "memory-service-client" and API keys
    And I authenticate as bearer user "alice" with API key "agent-api-key-1"
    When I call GET "/v1/admin/conversations"
    Then the response status should be 401

  Scenario: Mixed mode combines Keycloak user identity with API-key client identity
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And memory-service is running with OIDC allowed client "memory-service-client" and API keys
    And I authenticate with both OIDC user "bob" and API key "agent-api-key-1"
    When I call GET "/v1/admin/conversations"
    Then the response status should be 200

  Scenario: OIDC mixed mode accepts API-key-only admin client access
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And memory-service is running with OIDC allowed client "memory-service-client" and API keys
    And I authenticate with only API key header "agent-api-key-1"
    When I call GET "/v1/admin/conversations"
    Then the response status should be 200

  Scenario: OIDC mixed mode rejects API-key-only access to user-scoped APIs
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And memory-service is running with OIDC allowed client "memory-service-client" and API keys
    And I authenticate with only API key header "agent-api-key-1"
    When I call GET "/v1/conversations"
    Then the response status should be 401

  Scenario: OIDC mixed mode rejects bearer API-key compatibility
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And memory-service is running with OIDC allowed client "memory-service-client" and API keys
    And I authenticate with bearer API key "agent-api-key-1"
    When I call GET "/v1/admin/conversations"
    Then the response status should be 401

  Scenario: Mixed mode rejects invalid JWT-shaped bearer tokens
    Given API key "agent-api-key-1" maps to client "agent"
    And memory-service is running with OIDC allowed client "memory-service-client" and API keys
    And I set the "Authorization" header to "Bearer not.a.valid.jwt"
    When I call GET "/v1/conversations"
    Then the response status should be 401
    And the response body should contain "invalid JWT"

  Scenario: OIDC request with invalid paired API key is rejected
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And memory-service is running with OIDC allowed client "memory-service-client" and API keys
    And I login via OIDC as user "bob" with password "bob"
    And I set the "X-API-Key" header to "not-a-configured-api-key"
    When I call GET "/v1/conversations"
    Then the response status should be 401
    And the response body should contain "invalid API key"

  Scenario: Raw X-Client-ID does not override resolver-derived API-key client identity
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And memory-service is running with OIDC allowed client "memory-service-client" and API keys
    And I authenticate with only API key header "agent-api-key-1"
    And I set the "X-Client-ID" header to "forged-client"
    When I call GET "/v1/admin/conversations"
    Then the response status should be 200
