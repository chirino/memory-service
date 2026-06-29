Feature: Admin Memory gRPC API
  As a service principal (cognition processor)
  I want to write memories on behalf of users via AdminMemoriesService
  So that I can store derived cognition memories without user JWT authentication

  Background:
    Given I am authenticated with API key as admin client "cognition_processor"

  Scenario: Admin can write memory on behalf of a user
    When I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "facts"
    key: "fact-001"
    value {
      fields {
        key: "content"
        value { string_value: "User prefers dark mode" }
      }
      fields {
        key: "confidence"
        value { number_value: 0.95 }
      }
    }
    actor {
      on_behalf_of_user_id: "alice"
    }
    """
    Then the gRPC response should not have an error
    And set "memoryId" to the gRPC response field "id"
    And the gRPC response field "namespace[0]" should be "user"
    And the gRPC response field "namespace[1]" should be "alice"
    And the gRPC response field "namespace[2]" should be "cognition.v1"
    And the gRPC response field "namespace[3]" should be "facts"
    And the gRPC response field "key" should be "fact-001"

  Scenario: Admin can search memories on behalf of a user
    Given I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "bob"
    namespace: "cognition.v1"
    namespace: "preferences"
    key: "pref-001"
    value {
      fields {
        key: "category"
        value { string_value: "ui" }
      }
    }
    actor {
      on_behalf_of_user_id: "bob"
    }
    """
    When I send gRPC request "AdminMemoriesService/SearchMemories" with body:
    """
    namespace_prefix: "user"
    namespace_prefix: "bob"
    namespace_prefix: "cognition.v1"
    as_user_id: "bob"
    limit: 10
    """
    Then the gRPC response should not have an error
    And the gRPC response field "items[0].namespace[0]" should be "user"
    And the gRPC response field "items[0].namespace[1]" should be "bob"
    And the gRPC response field "items[0].value.category" should be "ui"

  Scenario: Admin can update (archive) memory on behalf of a user
    Given I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "charlie"
    namespace: "cognition.v1"
    namespace: "facts"
    key: "fact-temp"
    value {
      fields {
        key: "content"
        value { string_value: "temporary fact" }
      }
    }
    actor {
      on_behalf_of_user_id: "charlie"
    }
    """
    When I send gRPC request "AdminMemoriesService/UpdateMemory" with body:
    """
    namespace: "user"
    namespace: "charlie"
    namespace: "cognition.v1"
    namespace: "facts"
    key: "fact-temp"
    archived: true
    actor {
      on_behalf_of_user_id: "charlie"
    }
    """
    Then the gRPC response should not have an error

  Scenario: Admin write without on_behalf_of_user_id should work for admin-owned namespaces
    When I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "system"
    namespace: "metrics"
    key: "count"
    value {
      fields {
        key: "value"
        value { number_value: 42 }
      }
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "namespace[0]" should be "system"
    And the gRPC response field "key" should be "count"

  Scenario: Admin write with TTL via AdminMemoriesService
    When I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "dave"
    namespace: "cognition.v1"
    namespace: "facts"
    key: "temp-fact"
    value {
      fields {
        key: "content"
        value { string_value: "expires soon" }
      }
    }
    ttl_seconds: 3600
    actor {
      on_behalf_of_user_id: "dave"
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "expires_at" should not be empty

  Scenario: Admin write with index fields via AdminMemoriesService
    When I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "eve"
    namespace: "cognition.v1"
    namespace: "facts"
    key: "indexed-fact"
    value {
      fields {
        key: "content"
        value { string_value: "searchable content" }
      }
    }
    index {
      key: "category"
      value: "ui"
    }
    index {
      key: "priority"
      value: "high"
    }
    actor {
      on_behalf_of_user_id: "eve"
    }
    """
    Then the gRPC response should not have an error
