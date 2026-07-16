@api-keys @auth-modes @trusted-user-id @grpc
Feature: Trusted client user identity assertion over gRPC
  As a memory-service operator
  I want gRPC metadata to use the same trusted user assertion model as REST
  So Java and other gRPC agent clients can select the effective user per request

  Scenario: Trusted API-key client creates a conversation for the asserted user
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" is trusted to assert user IDs
    And memory-service is running with API keys and no OIDC
    And I authenticate with only API key header "agent-api-key-1"
    And I assert isolated user "alice" with X-User-ID
    When I send gRPC request "ConversationsService/CreateConversation" with body:
      """
      title: "asserted gRPC user"
      """
    Then the gRPC response should not have an error
    And the gRPC response field "ownerUserId" should be "alice"

  Scenario: Trusted API-key client stores and retrieves memory for the asserted user
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" is trusted to assert user IDs
    And memory-service is running with API keys and no OIDC
    And I authenticate with only API key header "agent-api-key-1"
    And I assert isolated user "alice" with X-User-ID
    When I send gRPC request "MemoriesService/PutMemory" with body:
      """
      namespace: "user"
      namespace: "alice"
      namespace: "prefs"
      key: "delegated-theme"
      value {
        fields {
          key: "color"
          value { string_value: "dark" }
        }
      }
      """
    Then the gRPC response should not have an error
    When I send gRPC request "MemoriesService/GetMemory" with body:
      """
      namespace: "user"
      namespace: "alice"
      namespace: "prefs"
      key: "delegated-theme"
      """
    Then the gRPC response should not have an error
    And the gRPC response field "value.color" should be "dark"

  Scenario: Authorized event stream uses the asserted user
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" is trusted to assert user IDs
    And memory-service is running with API keys and no OIDC
    And I authenticate with only API key header "agent-api-key-1"
    And I set the "X-User-ID" header to "alice"
    And the current client is connected to the gRPC event stream as "asserted-user"
    When I call POST "/v1/conversations" with body:
      """
      {"title": "asserted event stream user"}
      """
    Then the response status should be 201
    And "asserted-user" should receive a gRPC event with kind "conversation" and event "created"

  Scenario: Untrusted API-key client ignores x-user-id
    Given API key "agent-api-key-1" maps to client "agent"
    And memory-service is running with API keys and no OIDC
    And I use gRPC metadata:
      | x-api-key | agent-api-key-1 |
      | x-user-id | alice           |
    When I send gRPC request "ConversationsService/ListConversations" with body:
      """
      """
    Then the gRPC response should have status "UNAUTHENTICATED"

  Scenario: Trusted client rejects multiple asserted users
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" is trusted to assert user IDs
    And memory-service is running with API keys and no OIDC
    And I use gRPC metadata:
      | authorization | Bearer agent-api-key-1 |
      | x-user-id     | alice                  |
      | x-user-id     | bob                    |
    When I send gRPC request "ConversationsService/ListConversations" with body:
      """
      """
    Then the gRPC response should have status "INVALID_ARGUMENT"

  Scenario: Invalid credentials are rejected before a user assertion is considered
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" is trusted to assert user IDs
    And memory-service is running with API keys and no OIDC
    And I use gRPC metadata:
      | x-api-key | not-a-configured-api-key |
      | x-user-id | alice                    |
    When I send gRPC request "ConversationsService/ListConversations" with body:
      """
      """
    Then the gRPC response should have status "UNAUTHENTICATED"

  Scenario: Admin and system gRPC methods ignore duplicate asserted users
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And client "agent" is trusted to assert user IDs
    And memory-service is running with API keys and no OIDC
    And I use gRPC metadata:
      | x-api-key | agent-api-key-1 |
      | x-user-id | alice           |
      | x-user-id | bob             |
    When I send gRPC request "SystemService/GetCapabilities" with body:
      """
      {}
      """
    Then the gRPC response should not have an error
    When I send gRPC request "AdminConversationsService/ListConversations" with body:
      """
      """
    Then the gRPC response should not have an error

  Scenario: gRPC capabilities report trusted user assertion as enabled
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" is trusted to assert user IDs
    And memory-service is running with API keys and no OIDC
    And I authenticate with only API key header "agent-api-key-1"
    When I send gRPC request "SystemService/GetCapabilities" with body:
      """
      {}
      """
    Then the gRPC response should not have an error
    And the gRPC response field "auth.userIdAssertionEnabled" should be true
