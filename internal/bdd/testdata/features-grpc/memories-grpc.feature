Feature: Episodic Memory gRPC API
  As an agent or user
  I want to store and retrieve namespaced memories via gRPC
  So that I can persist state across sessions

  Background:
    Given I am authenticated as user "alice"

  Scenario: Put and get a memory via gRPC
    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "prefs"
    key: "theme"
    value {
      fields {
        key: "color"
        value { string_value: "dark" }
      }
    }
    """
    Then the gRPC response should not have an error
    And set "memoryId" to the gRPC response field "id"
    And the gRPC response field "namespace[0]" should be "user"
    And the gRPC response field "namespace[1]" should be "alice"
    And the gRPC response field "key" should be "theme"
    When I send gRPC request "MemoriesService/GetMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "prefs"
    key: "theme"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "${memoryId}"
    And the gRPC response field "value.color" should be "dark"

  Scenario: Put replaces an existing value
    Given I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "prefs"
    key: "lang"
    value {
      fields {
        key: "locale"
        value { string_value: "en" }
      }
    }
    """
    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "prefs"
    key: "lang"
    value {
      fields {
        key: "locale"
        value { string_value: "fr" }
      }
    }
    """
    Then the gRPC response should not have an error
    When I send gRPC request "MemoriesService/GetMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "prefs"
    key: "lang"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "value.locale" should be "fr"

  Scenario: Get non-existent memory returns NOT_FOUND
    When I send gRPC request "MemoriesService/GetMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    key: "missing"
    """
    Then the gRPC response should have status "NOT_FOUND"

  Scenario: Delete memory removes it
    Given I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "tmp"
    key: "to-delete"
    value {
      fields {
        key: "x"
        value { number_value: 1 }
      }
    }
    """
    When I send gRPC request "MemoriesService/DeleteMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "tmp"
    key: "to-delete"
    """
    Then the gRPC response should not have an error
    When I send gRPC request "MemoriesService/GetMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "tmp"
    key: "to-delete"
    """
    Then the gRPC response should have status "NOT_FOUND"

  Scenario: Search memories under prefix
    Given I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "notes"
    key: "note-1"
    value {
      fields {
        key: "text"
        value { string_value: "first note" }
      }
    }
    """
    And I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "notes"
    key: "note-2"
    value {
      fields {
        key: "text"
        value { string_value: "second note" }
      }
    }
    """
    When I send gRPC request "MemoriesService/SearchMemories" with body:
    """
    namespace_prefix: "user"
    namespace_prefix: "alice"
    namespace_prefix: "notes"
    limit: 10
    """
    Then the gRPC response should not have an error
    And the gRPC response field "items" should have size 2

  Scenario: List namespaces under prefix
    Given I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "ns-test"
    namespace: "a"
    key: "k1"
    value {
      fields {
        key: "v"
        value { number_value: 1 }
      }
    }
    """
    And I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "ns-test"
    namespace: "b"
    key: "k2"
    value {
      fields {
        key: "v"
        value { number_value: 2 }
      }
    }
    """
    When I send gRPC request "MemoriesService/ListMemoryNamespaces" with body:
    """
    prefix: "user"
    prefix: "alice"
    prefix: "ns-test"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "namespaces" should have size 2

  Scenario: User cannot write to another user's namespace
    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "bob"
    namespace: "prefs"
    key: "theme"
    value {
      fields {
        key: "color"
        value { string_value: "light" }
      }
    }
    """
    Then the gRPC response should have status "PERMISSION_DENIED"

  Scenario: Admin can query memory index status
    Given I am authenticated as admin user "alice"
    When I send gRPC request "MemoriesService/GetMemoryIndexStatus" with body:
    """
    {}
    """
    Then the gRPC response should not have an error
    And the gRPC response field "pending" should not be null
