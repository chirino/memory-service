Feature: Admin Memory gRPC API
  As an administrator
  I want to write memories across namespaces via AdminMemoriesService
  So that I can store namespace-scoped cognition memories through admin authorization

  Background:
    Given I am authenticated as admin user "alice"

  Scenario: Admin can write memory in a user namespace
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
    justification: "BDD admin memory write"
    """
    Then the gRPC response should not have an error
    And set "memoryId" to the gRPC response field "id"
    And the gRPC response field "namespace[0]" should be "user"
    And the gRPC response field "namespace[1]" should be "alice"
    And the gRPC response field "namespace[2]" should be "cognition.v1"
    And the gRPC response field "namespace[3]" should be "facts"
    And the gRPC response field "key" should be "fact-001"
    And the gRPC response field "revision" should not be null

  Scenario: Admin can search memories with user-scoped policy filtering
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
    justification: "BDD admin memory setup"
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

  Scenario: Admin can update (archive) memory in a user namespace
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
    justification: "BDD admin memory setup"
    """
    When I send gRPC request "AdminMemoriesService/UpdateMemory" with body:
    """
    namespace: "user"
    namespace: "charlie"
    namespace: "cognition.v1"
    namespace: "facts"
    key: "fact-temp"
    archived: true
    justification: "BDD admin memory archive"
    """
    Then the gRPC response should not have an error

  Scenario: Admin gRPC put detects stale expected revision
    Given I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "facts"
    key: "cas-grpc"
    value {
      fields {
        key: "content"
        value { string_value: "first value" }
      }
    }
    justification: "BDD admin memory CAS setup"
    """
    And the gRPC response should not have an error
    And set "grpcAdminRevision" to the gRPC response field "revision"
    And I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "facts"
    key: "cas-grpc"
    value {
      fields {
        key: "content"
        value { string_value: "second value" }
      }
    }
    expected_revision: ${grpcAdminRevision}
    justification: "BDD admin memory CAS update"
    """
    And the gRPC response should not have an error
    When I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "facts"
    key: "cas-grpc"
    value {
      fields {
        key: "content"
        value { string_value: "stale value" }
      }
    }
    expected_revision: ${grpcAdminRevision}
    justification: "BDD admin memory stale CAS"
    """
    Then the gRPC response should have status "ABORTED"

  Scenario: Admin write should work for admin-owned namespaces
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
    justification: "BDD admin system memory"
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
    justification: "BDD admin memory TTL"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "expiresAt" should not be null

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
    justification: "BDD admin memory index"
    """
    Then the gRPC response should not have an error

  Scenario: Admin gRPC put runs attribute extraction with neutral admin context
    Given I call PUT "/admin/v1/memory-policies" with body:
    """
    {
      "authz": "package memories.authz\nimport future.keywords.if\ndefault decision = {\"allow\": true}",
      "attributes": "package memories.attributes\nimport future.keywords.if\ndefault attributes = {}\nattributes = {\"admin_user_id\": input.context.user_id, \"admin_role\": input.context.jwt_claims.roles[0]} if { count(input.context.jwt_claims.roles) > 0 }",
      "filter": "package memories.filter\nimport future.keywords.if\nnamespace_prefix := input.namespace_prefix\nattribute_filter := {}"
    }
    """
    And the response status should be 204
    When I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "frank"
    namespace: "cognition.v1"
    namespace: "facts"
    key: "neutral-context-grpc"
    value {
      fields {
        key: "content"
        value { string_value: "uses neutral admin context" }
      }
    }
    justification: "BDD admin memory neutral context"
    """
    Then the gRPC response should not have an error
    When I send gRPC request "AdminMemoriesService/SearchMemories" with body:
    """
    namespace_prefix: "user"
    namespace_prefix: "frank"
    namespace_prefix: "cognition.v1"
    namespace_prefix: "facts"
    filter {
      fields {
        key: "admin_user_id"
        value {
          struct_value {
            fields {
              key: "\u0024eq"
              value { string_value: "" }
            }
          }
        }
      }
      fields {
        key: "admin_role"
        value {
          struct_value {
            fields {
              key: "\u0024eq"
              value { string_value: "admin" }
            }
          }
        }
      }
    }
    limit: 10
    justification: "BDD admin memory neutral search"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "items[0].key" should be "neutral-context-grpc"
    And the gRPC response field "items[0].attributes.admin_user_id" should be ""
    And the gRPC response field "items[0].attributes.admin_role" should be "admin"
