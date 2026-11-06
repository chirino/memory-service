Feature: Episodic Memory gRPC API
  As an agent or user
  I want to store and retrieve namespaced memories via gRPC
  So that I can persist state across sessions

  Background:
    Given I am authenticated as user "alice"

  @memory-kind-regression
  Scenario: Admin kind list omits Rego while get includes it via gRPC
    Given I am authenticated as admin user "alice"
    When I send gRPC request "AdminMemoryKindService/CreateMemoryKindVersion" with body:
    """
    name: "grpc-listing/v1"
    attributes { key: "tag" value: "string" }
    projection_rego: "package memories.attributes\n# grpc-listing-secret-marker\nattributes := {\"tag\": \"safe\"}"
    justification: "verify list redaction"
    """
    Then the gRPC response should not have an error
    When I send gRPC request "AdminMemoryKindService/ListMemoryKindVersions" with body:
    """
    family: "grpc-listing"
    justification: "verify list redaction"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "items" should have size 1
    And the gRPC response should not contain field "items[0].projectionRego"
    When I send gRPC request "AdminMemoryKindService/GetMemoryKindVersion" with body:
    """
    family: "grpc-listing"
    version: "v1"
    justification: "verify single get"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "projectionRego" should not be null

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
    And the gRPC response field "kind" should be "default/v1"
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
    And the gRPC response field "kind" should be "default/v1"

  Scenario: List memory lifecycle events via gRPC
    Given I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "events"
    key: "seen"
    value {
      fields {
        key: "enabled"
        value { bool_value: true }
      }
    }
    """
    When I send gRPC request "MemoriesService/ListMemoryEvents" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "events"
    kinds: "add"
    limit: 10
    """
    Then the gRPC response should not have an error
    And the gRPC response field "events" should have size 1
    And the gRPC response field "events[0].kind" should be "add"
    And the gRPC response field "events[0].key" should be "seen"
    And the gRPC response field "events[0].value.enabled" should be true
    And the gRPC response field "events[0].memoryKind" should be "default/v1"

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

  Scenario: Archive memory keeps it readable via archive filters
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
    When I send gRPC request "MemoriesService/UpdateMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "tmp"
    key: "to-delete"
    archived: true
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
    When I send gRPC request "MemoriesService/GetMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "tmp"
    key: "to-delete"
    archived: ARCHIVE_FILTER_INCLUDE
    """
    Then the gRPC response should not have an error
    And the gRPC response field "archived" should be true
    When I send gRPC request "MemoriesService/GetMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "tmp"
    key: "to-delete"
    archived: ARCHIVE_FILTER_ONLY
    """
    Then the gRPC response should not have an error
    And the gRPC response field "archived" should be true

  @memory-kind-regression
  Scenario: Archived-only and include select matching kind and content via gRPC
    Given I am authenticated as admin user "alice"
    And I call POST "/admin/v1/memory-kinds" with body:
    """
    {"name":"grpc-archive-select/v1","attributes":{},"projectionRego":"package memories.attributes\nattributes := {}"}
    """
    And the response status should be 200
    And I call POST "/admin/v1/memory-kinds" with body:
    """
    {"name":"grpc-archive-select/v2","attributes":{},"projectionRego":"package memories.attributes\nattributes := {}"}
    """
    And the response status should be 200
    And I am authenticated as user "alice"
    And I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-archive-select"
    key: "same-key"
    value { fields { key: "version" value { string_value: "a" } } }
    kind: "grpc-archive-select/v1"
    """
    And I send gRPC request "MemoriesService/UpdateMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-archive-select"
    key: "same-key"
    archived: true
    """
    And I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-archive-select"
    key: "same-key"
    value { fields { key: "version" value { string_value: "b" } } }
    kind: "grpc-archive-select/v2"
    """
    When I send gRPC request "MemoriesService/GetMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-archive-select"
    key: "same-key"
    archived: ARCHIVE_FILTER_ONLY
    """
    Then the gRPC response should not have an error
    And the gRPC response field "kind" should be "grpc-archive-select/v1"
    And the gRPC response field "value.version" should be "a"
    When I send gRPC request "MemoriesService/GetMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-archive-select"
    key: "same-key"
    archived: ARCHIVE_FILTER_INCLUDE
    """
    Then the gRPC response should not have an error
    And the gRPC response field "kind" should be "grpc-archive-select/v2"
    And the gRPC response field "value.version" should be "b"

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
    And the gRPC response field "items[0].kind" should be "default/v1"

  Scenario: SearchMemories supports enhanced attribute filters via gRPC
    Given I am authenticated as user "alice"
    And I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-filter-ops"
    key: "grpc-alpha"
    value {
      fields { key: "text" value { string_value: "alpha" } }
    }
    """
    And I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-filter-ops"
    key: "grpc-beta"
    value {
      fields { key: "text" value { string_value: "beta" } }
    }
    """
    When I send gRPC request "MemoriesService/SearchMemories" with body:
    """
    namespace_prefix: "user"
    namespace_prefix: "alice"
    namespace_prefix: "grpc-filter-ops"
    filter {
      fields {
        key: "sub"
        value {
          struct_value {
            fields {
              key: "\u0024eq"
              value { string_value: "alice" }
            }
          }
        }
      }
    }
    limit: 10
    """
    Then the gRPC response should not have an error
    And the gRPC response field "items" should have size 2
    When I send gRPC request "MemoriesService/SearchMemories" with body:
    """
    namespace_prefix: "user"
    namespace_prefix: "alice"
    namespace_prefix: "grpc-filter-ops"
    filter {
      fields {
        key: "sub"
        value {
          struct_value {
            fields {
              key: "\u0024eq"
              value { string_value: "bob" }
            }
          }
        }
      }
    }
    limit: 10
    """
    Then the gRPC response should not have an error
    And the gRPC response field "items" should have size 0
    When I send gRPC request "MemoriesService/SearchMemories" with body:
    """
    namespace_prefix: "user"
    namespace_prefix: "alice"
    namespace_prefix: "grpc-filter-ops"
    filter {
      fields {
        key: "sub"
        value {
          struct_value {
            fields {
              key: "\u0024in"
              value {
                list_value {
                  values { string_value: "alice" }
                }
              }
            }
          }
        }
      }
    }
    limit: 10
    """
    Then the gRPC response should not have an error
    And the gRPC response field "items" should have size 2
    When I send gRPC request "MemoriesService/SearchMemories" with body:
    """
    namespace_prefix: "user"
    namespace_prefix: "alice"
    namespace_prefix: "grpc-filter-ops"
    filter {
      fields {
        key: "sub"
        value {
          struct_value {
            fields {
              key: "\u0024exists"
              value { bool_value: true }
            }
          }
        }
      }
    }
    limit: 10
    """
    Then the gRPC response should not have an error
    And the gRPC response field "items" should have size 2
    When I send gRPC request "MemoriesService/SearchMemories" with body:
    """
    namespace_prefix: "user"
    namespace_prefix: "alice"
    namespace_prefix: "grpc-filter-ops"
    filter {
      fields {
        key: "sub"
        value {
          struct_value {
            fields {
              key: "\u0024unknown"
              value { string_value: "bob" }
            }
          }
        }
      }
    }
    limit: 10
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"

  Scenario: Search memories and list namespaces honor archive filters
    Given I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "archive-search"
    key: "active-note"
    value {
      fields {
        key: "text"
        value { string_value: "still active" }
      }
    }
    """
    And I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "archive-search"
    key: "archived-note"
    value {
      fields {
        key: "text"
        value { string_value: "only archived" }
      }
    }
    """
    And I send gRPC request "MemoriesService/UpdateMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "archive-search"
    key: "archived-note"
    archived: true
    """
    When I send gRPC request "MemoriesService/SearchMemories" with body:
    """
    namespace_prefix: "user"
    namespace_prefix: "alice"
    namespace_prefix: "archive-search"
    limit: 10
    """
    Then the gRPC response should not have an error
    And the gRPC response field "items" should have size 1
    And the gRPC response field "items[0].key" should be "active-note"
    When I send gRPC request "MemoriesService/SearchMemories" with body:
    """
    namespace_prefix: "user"
    namespace_prefix: "alice"
    namespace_prefix: "archive-search"
    archived: ARCHIVE_FILTER_INCLUDE
    limit: 10
    """
    Then the gRPC response should not have an error
    And the gRPC response field "items" should have size 2
    When I send gRPC request "MemoriesService/SearchMemories" with body:
    """
    namespace_prefix: "user"
    namespace_prefix: "alice"
    namespace_prefix: "archive-search"
    archived: ARCHIVE_FILTER_ONLY
    limit: 10
    """
    Then the gRPC response should not have an error
    And the gRPC response field "items" should have size 1
    And the gRPC response field "items[0].key" should be "archived-note"
    When I send gRPC request "MemoriesService/ListMemoryNamespaces" with body:
    """
    prefix: "user"
    prefix: "alice"
    prefix: "archive-search"
    archived: ARCHIVE_FILTER_ONLY
    """
    Then the gRPC response should not have an error
    And the gRPC response field "namespaces" should have size 1

  Scenario: GetMemory include_usage returns usage counters
    Given I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "usage-grpc-get"
    key: "gk1"
    value {
      fields {
        key: "text"
        value { string_value: "tracked" }
      }
    }
    """
    When I send gRPC request "MemoriesService/GetMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "usage-grpc-get"
    key: "gk1"
    include_usage: true
    """
    Then the gRPC response should not have an error
    And the gRPC response field "usage.fetchCount" should be "1"
    And the gRPC response field "usage.lastFetchedAt" should not be null

  Scenario: SearchMemories include_usage does not increment fetch counters
    Given I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "usage-grpc-search"
    key: "gk2"
    value {
      fields {
        key: "text"
        value { string_value: "search-test" }
      }
    }
    """
    And I send gRPC request "MemoriesService/GetMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "usage-grpc-search"
    key: "gk2"
    include_usage: true
    """
    And the gRPC response should not have an error
    When I send gRPC request "MemoriesService/SearchMemories" with body:
    """
    namespace_prefix: "user"
    namespace_prefix: "alice"
    namespace_prefix: "usage-grpc-search"
    include_usage: true
    limit: 10
    """
    Then the gRPC response should not have an error
    And the gRPC response field "items" should have size 1
    And the gRPC response field "items[0].usage.fetchCount" should be "1"
    When I am authenticated as admin user "alice"
    And I send gRPC request "AdminMemoriesService/GetMemoryUsage" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "usage-grpc-search"
    key: "gk2"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "fetchCount" should be "1"

  Scenario: ListTopMemoryUsage ranks by fetch_count
    Given I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "usage-top"
    key: "k-top"
    value {
      fields {
        key: "text"
        value { string_value: "top" }
      }
    }
    """
    And I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "usage-top"
    key: "k-low"
    value {
      fields {
        key: "text"
        value { string_value: "low" }
      }
    }
    """
    And I send gRPC request "MemoriesService/GetMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "usage-top"
    key: "k-top"
    """
    And the gRPC response should not have an error
    And I send gRPC request "MemoriesService/GetMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "usage-top"
    key: "k-top"
    """
    And the gRPC response should not have an error
    And I send gRPC request "MemoriesService/GetMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "usage-top"
    key: "k-low"
    """
    And the gRPC response should not have an error
    When I am authenticated as admin user "alice"
    And I send gRPC request "AdminMemoriesService/ListTopMemoryUsage" with body:
    """
    prefix: "user"
    prefix: "alice"
    prefix: "usage-top"
    sort: FETCH_COUNT
    limit: 1
    """
    Then the gRPC response should not have an error
    And the gRPC response field "items[0].key" should be "k-top"

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
    And the gRPC error message should contain "access denied"

  Scenario: Admin can query memory index status
    Given I am authenticated as admin user "alice"
    When I send gRPC request "AdminMemoriesService/GetMemoryIndexStatus" with body:
    """
    justification: "inspect index status"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "pending" should not be null

  Scenario: Auditor can list and get memories over gRPC
    Given I am authenticated as user "alice"
    And I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "admin-grpc"
    key: "grpc-theme"
    value {
      fields {
        key: "color"
        value { string_value: "dark" }
      }
    }
    """
    And the gRPC response should not have an error
    And set "adminGrpcMemoryId" to the gRPC response field "id"
    When I am authenticated as auditor user "charlie"
    And I send gRPC request "AdminMemoriesService/ListMemories" with body:
    """
    namespace_prefix: "user"
    namespace_prefix: "alice"
    namespace_prefix: "admin-grpc"
    limit: 10
    """
    Then the gRPC response should not have an error
    And the gRPC response field "items" should have size 1
    And the gRPC response field "items[0].key" should be "grpc-theme"
    When I send gRPC request "AdminMemoriesService/GetMemory" with body:
    """
    id: "${adminGrpcMemoryId | uuid_to_hex_string}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "key" should be "grpc-theme"

  Scenario: Auditor can search as target user over gRPC
    Given I am authenticated as user "alice"
    And I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "admin-grpc-as-user"
    key: "grpc-alice-note"
    value {
      fields {
        key: "owner"
        value { string_value: "alice" }
      }
    }
    """
    And the gRPC response should not have an error
    And I am authenticated as user "bob"
    And I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "bob"
    namespace: "admin-grpc-as-user"
    key: "grpc-bob-note"
    value {
      fields {
        key: "owner"
        value { string_value: "bob" }
      }
    }
    """
    And the gRPC response should not have an error
    When I am authenticated as auditor user "charlie"
    And I send gRPC request "AdminMemoriesService/SearchMemories" with body:
    """
    namespace_prefix: "user"
    as_user_id: "bob"
    limit: 10
    """
    Then the gRPC response should not have an error
    And the gRPC response field "items" should have size 1
    And the gRPC response field "items[0].key" should be "grpc-bob-note"

  Scenario: gRPC non-admin can search a custom kind without reserved projection attributes
    Given I am authenticated as admin user "alice"
    And I call POST "/admin/v1/memory-kinds?justification=custom-kind-search" with body:
    """
    {"name":"grpctagonly/v1","attributes":{"tag":"string"},"projectionRego":"package memories.attributes\nattributes := {\"tag\": input.value.tag}"}
    """
    And the response status should be 200
    And I am authenticated as user "bob"
    And I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "bob"
    namespace: "grpc-tag-only"
    key: "grpc-custom-visible"
    kind: "grpctagonly/v1"
    value { fields { key: "tag" value { string_value: "find-me" } } }
    """
    And the gRPC response should not have an error
    When I send gRPC request "MemoriesService/SearchMemories" with body:
    """
    namespace_prefix: "user"
    namespace_prefix: "bob"
    namespace_prefix: "grpc-tag-only"
    kind: "grpctagonly/v1"
    filter { fields { key: "tag" value { string_value: "find-me" } } }
    limit: 10
    """
    Then the gRPC response should not have an error
    And the gRPC response field "items" should have size 1
    And the gRPC response field "items[0].key" should be "grpc-custom-visible"

  Scenario: gRPC PutMemory with revision_id and revision: 1 creates memory idempotently
    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-idem-create"
    key: "gk-idem-create"
    value {
      fields {
        key: "v"
        value { number_value: 1 }
      }
    }
    revision_id: "9f1c1d0a-0000-4000-8000-000000000001"
    revision: 1
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "9f1c1d0a-0000-4000-8000-000000000001"
    And set "grpcFirstCreatedAt" to the gRPC response field "createdAt"

    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-idem-create"
    key: "gk-idem-create"
    value {
      fields {
        key: "v"
        value { number_value: 1 }
      }
    }
    revision_id: "9f1c1d0a-0000-4000-8000-000000000001"
    revision: 1
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "9f1c1d0a-0000-4000-8000-000000000001"
    And the gRPC response field "createdAt" should be "${grpcFirstCreatedAt}"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'gk-idem-create'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "gk-idem-create"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |


  Scenario: gRPC create-only on active memory returns ABORTED
    Given I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-idem-conflict"
    key: "gk-active"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    """
    Then the gRPC response should not have an error

    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-idem-conflict"
    key: "gk-active"
    value {
      fields { key: "v" value { number_value: 2 } }
    }
    revision_id: "9f1c1d0a-0000-4000-8000-000000000002"
    revision: 1
    """
    Then the gRPC response should have status "ABORTED"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'gk-active'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "gk-active"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE id = '9f1c1d0a-0000-4000-8000-000000000002'
    """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"_id": "9f1c1d0a-0000-4000-8000-000000000002"}}
    """
    Then the MongoDB result should match:
      | count |
      | 0     |


  Scenario: gRPC PutMemory with matching expected_revision_id updates memory idempotently
    Given I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-cas"
    key: "gk-cas"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "9f1c1d0a-0000-4000-8000-000000000010"
    """
    Then the gRPC response should not have an error

    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-cas"
    key: "gk-cas"
    value {
      fields { key: "v" value { number_value: 2 } }
    }
    revision_id: "9f1c1d0a-0000-4000-8000-000000000011"
    expected_revision_id: "9f1c1d0a-0000-4000-8000-000000000010"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "9f1c1d0a-0000-4000-8000-000000000011"

    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-cas"
    key: "gk-cas"
    value {
      fields { key: "v" value { number_value: 2 } }
    }
    revision_id: "9f1c1d0a-0000-4000-8000-000000000011"
    expected_revision_id: "9f1c1d0a-0000-4000-8000-000000000010"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "9f1c1d0a-0000-4000-8000-000000000011"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'gk-cas'
    """
    Then the SQL result should match:
      | count |
      | 2     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "gk-cas"}}
    """
    Then the MongoDB result should match:
      | count |
      | 2     |
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'gk-cas' AND archived_at IS NULL
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "gk-cas", "archived_at": {"$exists": false}}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |


  Scenario: gRPC PutMemory with mismatching expected_revision_id returns ABORTED
    Given I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-cas-mismatch"
    key: "gk-mismatch"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "9f1c1d0a-0000-4000-8000-000000000020"
    """
    Then the gRPC response should not have an error

    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-cas-mismatch"
    key: "gk-mismatch"
    value {
      fields { key: "v" value { number_value: 2 } }
    }
    revision_id: "9f1c1d0a-0000-4000-8000-000000000021"
    expected_revision_id: "9f1c1d0a-0000-4000-8000-000000000099"
    """
    Then the gRPC response should have status "ABORTED"
    And the gRPC error message should contain "expected_active_revision_id_mismatch"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'gk-mismatch'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "gk-mismatch"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE id = '9f1c1d0a-0000-4000-8000-000000000021'
    """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"_id": "9f1c1d0a-0000-4000-8000-000000000021"}}
    """
    Then the MongoDB result should match:
      | count |
      | 0     |


  Scenario: gRPC PutMemory retry with different value returns ABORTED
    Given I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-diff"
    key: "gk-diff"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "9f1c1d0a-0000-4000-8000-000000000030"
    """
    Then the gRPC response should not have an error

    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-diff"
    key: "gk-diff"
    value {
      fields { key: "v" value { number_value: 2 } }
    }
    revision_id: "9f1c1d0a-0000-4000-8000-000000000030"
    """
    Then the gRPC response should have status "ABORTED"
    And the gRPC error message should contain "memory_revision_already_exists"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'gk-diff'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "gk-diff"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |


  Scenario: gRPC PutMemory rejects revision value other than 1
    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-invalid"
    key: "gk-invalid-rev"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "9f1c1d0a-0000-4000-8000-000000000040"
    revision: 2
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"

  Scenario: gRPC PutMemory with expected_revision_id without revision_id returns INVALID_ARGUMENT
    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-invalid"
    key: "gk-no-rev-id"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    expected_revision_id: "9f1c1d0a-0000-4000-8000-000000000050"
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"

  Scenario: gRPC PutMemory retry of archived memory returns NOT_FOUND
    Given I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-arch"
    key: "gk-arch"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "9f1c1d0a-0000-4000-8000-000000000060"
    """
    Then the gRPC response should not have an error

    When I send gRPC request "MemoriesService/UpdateMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-arch"
    key: "gk-arch"
    archived: true
    """
    Then the gRPC response should not have an error

    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-arch"
    key: "gk-arch"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "9f1c1d0a-0000-4000-8000-000000000060"
    """
    Then the gRPC response should have status "NOT_FOUND"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'gk-arch' AND archived_at IS NOT NULL AND deleted_reason = 1
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "gk-arch", "archived_at": {"$exists": true}, "deleted_reason": 1}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'gk-arch'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "gk-arch"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |

  Scenario: gRPC PutMemory rejects empty and non-UUID revision_id
    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-invalid"
    key: "gk-empty-rev-id"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: ""
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"
    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-invalid"
    key: "gk-bad-rev-id"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "not-a-valid-uuid"
    revision: 1
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'gk-empty-rev-id' OR key = 'gk-bad-rev-id'
    """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": {"$in": ["gk-empty-rev-id", "gk-bad-rev-id"]}}}
    """
    Then the MongoDB result should match:
      | count |
      | 0     |

  Scenario: gRPC PutMemory with expected_revision_id when no active memory exists returns ABORTED
    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-cas-none"
    key: "gk-no-prior"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "9f1c1d0a-0000-4000-8000-000000000070"
    expected_revision_id: "9f1c1d0a-0000-4000-8000-000000000071"
    """
    Then the gRPC response should have status "ABORTED"
    And the gRPC error message should contain "expected_active_revision_id_mismatch"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'gk-no-prior'
    """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "gk-no-prior"}}
    """
    Then the MongoDB result should match:
      | count |
      | 0     |

  Scenario: gRPC cross-namespace revision_id collision returns ABORTED without disclosing data
    Given I am authenticated as user "alice"
    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-secret"
    key: "gk-secret"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "9f1c1d0a-0000-4000-8000-000000000080"
    """
    Then the gRPC response should not have an error
    When I am authenticated as user "bob"
    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "bob"
    namespace: "grpc-public"
    key: "gk-other"
    value {
      fields { key: "v" value { number_value: 2 } }
    }
    revision_id: "9f1c1d0a-0000-4000-8000-000000000080"
    """
    Then the gRPC response should have status "ABORTED"
    And the gRPC error message should contain "memory_revision_already_exists"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE id = '9f1c1d0a-0000-4000-8000-000000000080'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"_id": "9f1c1d0a-0000-4000-8000-000000000080"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'gk-other'
    """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "gk-other"}}
    """
    Then the MongoDB result should match:
      | count |
      | 0     |

  Scenario: gRPC PutMemory retry with different TTL returns ABORTED and same TTL succeeds
    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-ttl"
    key: "gk-ttl"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    ttl_seconds: 3600
    revision_id: "9f1c1d0a-0000-4000-8000-000000000090"
    """
    Then the gRPC response should not have an error
    And set "grpcTtlExpiresAt" to the gRPC response field "expiresAt"
    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-ttl"
    key: "gk-ttl"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    ttl_seconds: 3600
    revision_id: "9f1c1d0a-0000-4000-8000-000000000090"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "expiresAt" should be "${grpcTtlExpiresAt}"
    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-ttl"
    key: "gk-ttl"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    ttl_seconds: 7200
    revision_id: "9f1c1d0a-0000-4000-8000-000000000090"
    """
    Then the gRPC response should have status "ABORTED"
    And the gRPC error message should contain "memory_revision_already_exists"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'gk-ttl'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "gk-ttl"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |

  Scenario: gRPC PutMemory index nil and empty map match exactly on retry
    Given I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-index"
    key: "gk-index"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "9f1c1d0a-0000-4000-8000-0000000000a0"
    """
    Then the gRPC response should not have an error
    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-index"
    key: "gk-index"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "9f1c1d0a-0000-4000-8000-0000000000a0"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "9f1c1d0a-0000-4000-8000-0000000000a0"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'gk-index'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "gk-index"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |

  Scenario: gRPC exact retry suppresses side effects
    Given I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-side-effects"
    key: "gk-side"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "9f1c1d0a-0000-4000-8000-0000000000b0"
    """
    Then the gRPC response should not have an error
    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-side-effects"
    key: "gk-side"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "9f1c1d0a-0000-4000-8000-0000000000b0"
    """
    Then the gRPC response should not have an error
    When I call GET "/v1/memories/events?ns=user&ns=alice&ns=grpc-side-effects&kinds=add"
    Then the response status should be 200
    And the response body "events" should have 1 item
    When I call GET "/v1/memories/events?ns=user&ns=alice&ns=grpc-side-effects&kinds=update"
    Then the response status should be 200
    And the response body "events" should have 0 items
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'gk-side'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "gk-side"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'gk-side' AND archived_at IS NOT NULL
    """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "gk-side", "archived_at": {"$exists": true}}}
    """
    Then the MongoDB result should match:
      | count |
      | 0     |

  Scenario: gRPC retry by a caller without write access to the namespace returns PERMISSION_DENIED
    Given I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-authz"
    key: "gk-authz"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "9f1c1d0a-0000-4000-8000-0000000000c0"
    """
    Then the gRPC response should not have an error
    When I am authenticated as user "bob"
    And I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-authz"
    key: "gk-authz"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "9f1c1d0a-0000-4000-8000-0000000000c0"
    """
    Then the gRPC response should have status "PERMISSION_DENIED"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'gk-authz'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "gk-authz"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |

  Scenario: gRPC PutMemory rejects non-UUID expected_revision_id and writes nothing
    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-invalid"
    key: "gk-bad-expected-id"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "9f1c1d0a-0000-4000-8000-0000000002b0"
    expected_revision_id: "not-a-valid-uuid"
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'gk-bad-expected-id'
    """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "gk-bad-expected-id"}}
    """
    Then the MongoDB result should match:
      | count |
      | 0     |

  Scenario: gRPC evicted row retry with create-only mode returns ABORTED when active row exists
    Given I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-evict-idem"
    key: "gk-evict-k1"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "9f1c1d0a-0000-4000-8000-0000000000e1"
    revision: 1
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "9f1c1d0a-0000-4000-8000-0000000000e1"

    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-evict-idem"
    key: "gk-evict-k1"
    value {
      fields { key: "v" value { number_value: 2 } }
    }
    revision_id: "9f1c1d0a-0000-4000-8000-0000000000e2"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "9f1c1d0a-0000-4000-8000-0000000000e2"

    When I execute SQL query:
    """
    DELETE FROM memories WHERE id = '9f1c1d0a-0000-4000-8000-0000000000e1'
    """
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "delete", "filter": {"_id": "9f1c1d0a-0000-4000-8000-0000000000e1"}}
    """

    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-evict-idem"
    key: "gk-evict-k1"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "9f1c1d0a-0000-4000-8000-0000000000e1"
    revision: 1
    """
    Then the gRPC response should have status "ABORTED"

    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE id = '9f1c1d0a-0000-4000-8000-0000000000e1'
    """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"_id": "9f1c1d0a-0000-4000-8000-0000000000e1"}}
    """
    Then the MongoDB result should match:
      | count |
      | 0     |

  Scenario: gRPC PUT with revision_id after HardDeleteEvictableUpdates is treated as a new write
    Given I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-hard-delete-idem"
    key: "gk-hd-k1"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "9f1c1d0a-0000-4000-8000-0000000000f1"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "9f1c1d0a-0000-4000-8000-0000000000f1"

    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-hard-delete-idem"
    key: "gk-hd-k1"
    value {
      fields { key: "v" value { number_value: 2 } }
    }
    revision_id: "9f1c1d0a-0000-4000-8000-0000000000f2"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "9f1c1d0a-0000-4000-8000-0000000000f2"

    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-hard-delete-idem"
    key: "gk-hd-k1"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "9f1c1d0a-0000-4000-8000-0000000000f1"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "9f1c1d0a-0000-4000-8000-0000000000f1"

    When I execute SQL query:
    """
    DELETE FROM memories WHERE id = '9f1c1d0a-0000-4000-8000-0000000000f1'
    """
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "delete", "filter": {"_id": "9f1c1d0a-0000-4000-8000-0000000000f1"}}
    """

    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "grpc-hard-delete-idem"
    key: "gk-hd-k1"
    value {
      fields { key: "v" value { number_value: 3 } }
    }
    revision_id: "9f1c1d0a-0000-4000-8000-0000000000f1"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "9f1c1d0a-0000-4000-8000-0000000000f1"

    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'gk-hd-k1' AND archived_at IS NULL
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "gk-hd-k1", "archived_at": {"$exists": false}}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |

    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE id = '9f1c1d0a-0000-4000-8000-0000000000f2' AND archived_at IS NOT NULL
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"_id": "9f1c1d0a-0000-4000-8000-0000000000f2", "archived_at": {"$exists": true}}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
