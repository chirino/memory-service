@api-keys @auth-modes @grpc
Feature: No-OIDC API key authentication over gRPC
  As a memory-service operator
  I want API-key-only principals to access gRPC capabilities and be rejected by user-scoped gRPC APIs
  So that gRPC and REST auth behaviour are consistent

  Scenario: gRPC capabilities accepts API-key-only client identity
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And memory-service is running with API keys and no OIDC
    And I authenticate with only API key header "agent-api-key-1"
    When I send gRPC request "SystemService/GetCapabilities" with body:
    """
    {}
    """
    Then the gRPC response should not have an error
    And the gRPC response field "version" should not be null

  Scenario: gRPC user-scoped APIs reject API-key-only client identity
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And memory-service is running with API keys and no OIDC
    And I authenticate with only API key header "agent-api-key-1"
    When I send gRPC request "ConversationsService/ListConversations" with body:
    """
    """
    Then the gRPC response should have status "UNAUTHENTICATED"

  Scenario: gRPC rejects non-Bearer authorization even with a valid API key
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And memory-service is running with API keys and no OIDC
    And I use gRPC metadata:
      | authorization | Basic not-a-bearer     |
      | x-api-key     | agent-api-key-1        |
    When I send gRPC request "AdminConversationsService/ListConversations" with body:
    """
    """
    Then the gRPC response should have status "UNAUTHENTICATED"

  Scenario: gRPC memory writes reject API-key-only client identity
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And memory-service is running with API keys and no OIDC
    And I authenticate with only API key header "agent-api-key-1"
    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    key: "profile"
    value {
      fields {
        key: "name"
        value { string_value: "Alice" }
      }
    }
    """
    Then the gRPC response should have status "UNAUTHENTICATED"

  Scenario: gRPC memory reads reject API-key-only client identity
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And memory-service is running with API keys and no OIDC
    And I authenticate with only API key header "agent-api-key-1"
    When I send gRPC request "MemoriesService/GetMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    key: "profile"
    """
    Then the gRPC response should have status "UNAUTHENTICATED"

  Scenario: gRPC memory search rejects API-key-only client identity
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And memory-service is running with API keys and no OIDC
    And I authenticate with only API key header "agent-api-key-1"
    When I send gRPC request "MemoriesService/SearchMemories" with body:
    """
    namespace_prefix: "user"
    """
    Then the gRPC response should have status "UNAUTHENTICATED"

  Scenario: gRPC memory namespace listing rejects API-key-only client identity
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And memory-service is running with API keys and no OIDC
    And I authenticate with only API key header "agent-api-key-1"
    When I send gRPC request "MemoriesService/ListMemoryNamespaces" with body:
    """
    """
    Then the gRPC response should have status "UNAUTHENTICATED"

  Scenario: gRPC memory archive rejects API-key-only client identity
    Given API key "agent-api-key-1" maps to client "agent"
    And client "agent" has the "admin" role
    And memory-service is running with API keys and no OIDC
    And I authenticate with only API key header "agent-api-key-1"
    When I send gRPC request "MemoriesService/UpdateMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    key: "profile"
    archived: true
    """
    Then the gRPC response should have status "UNAUTHENTICATED"
