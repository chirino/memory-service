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

  Scenario: Admin gRPC PutMemory with revision_id and revision: 1 creates memory idempotently
    When I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "admin-grpc-idem"
    key: "agk1"
    value {
      fields {
        key: "content"
        value { string_value: "admin memory facts" }
      }
    }
    revision_id: "af1c1d0a-0000-4000-8000-000000000001"
    revision: 1
    justification: "BDD admin memory create-only"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "af1c1d0a-0000-4000-8000-000000000001"
    And set "adminGrpcFirstCreatedAt" to the gRPC response field "createdAt"

    When I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "admin-grpc-idem"
    key: "agk1"
    value {
      fields {
        key: "content"
        value { string_value: "admin memory facts" }
      }
    }
    revision_id: "af1c1d0a-0000-4000-8000-000000000001"
    revision: 1
    justification: "BDD admin memory create-only retry"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "af1c1d0a-0000-4000-8000-000000000001"
    And the gRPC response field "createdAt" should be "${adminGrpcFirstCreatedAt}"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'agk1'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "agk1"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |


  Scenario: Admin gRPC create-only on active memory returns ABORTED
    Given I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "admin-grpc-idem-conflict"
    key: "agk-active"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    justification: "BDD admin memory setup"
    """
    Then the gRPC response should not have an error

    When I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "admin-grpc-idem-conflict"
    key: "agk-active"
    value {
      fields { key: "v" value { number_value: 2 } }
    }
    revision_id: "af1c1d0a-0000-4000-8000-000000000002"
    revision: 1
    justification: "BDD admin memory create-only conflict"
    """
    Then the gRPC response should have status "ABORTED"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'agk-active'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "agk-active"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE id = 'af1c1d0a-0000-4000-8000-000000000002'
    """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"_id": "af1c1d0a-0000-4000-8000-000000000002"}}
    """
    Then the MongoDB result should match:
      | count |
      | 0     |


  Scenario: Admin gRPC PutMemory with expected_revision_id updates memory idempotently
    Given I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "admin-grpc-cas"
    key: "agk-cas"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "af1c1d0a-0000-4000-8000-000000000010"
    justification: "BDD admin memory cas setup"
    """
    Then the gRPC response should not have an error

    When I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "admin-grpc-cas"
    key: "agk-cas"
    value {
      fields { key: "v" value { number_value: 2 } }
    }
    revision_id: "af1c1d0a-0000-4000-8000-000000000011"
    expected_revision_id: "af1c1d0a-0000-4000-8000-000000000010"
    justification: "BDD admin memory cas update"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "af1c1d0a-0000-4000-8000-000000000011"

    When I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "admin-grpc-cas"
    key: "agk-cas"
    value {
      fields { key: "v" value { number_value: 2 } }
    }
    revision_id: "af1c1d0a-0000-4000-8000-000000000011"
    expected_revision_id: "af1c1d0a-0000-4000-8000-000000000010"
    justification: "BDD admin memory cas retry"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "af1c1d0a-0000-4000-8000-000000000011"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'agk-cas'
    """
    Then the SQL result should match:
      | count |
      | 2     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "agk-cas"}}
    """
    Then the MongoDB result should match:
      | count |
      | 2     |
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'agk-cas' AND archived_at IS NULL
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "agk-cas", "archived_at": {"$exists": false}}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |


  Scenario: Admin gRPC PutMemory with mismatching expected_revision_id returns ABORTED
    Given I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "admin-grpc-cas-mismatch"
    key: "agk-mismatch"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "af1c1d0a-0000-4000-8000-000000000020"
    justification: "BDD admin memory mismatch setup"
    """
    Then the gRPC response should not have an error

    When I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "admin-grpc-cas-mismatch"
    key: "agk-mismatch"
    value {
      fields { key: "v" value { number_value: 2 } }
    }
    revision_id: "af1c1d0a-0000-4000-8000-000000000021"
    expected_revision_id: "af1c1d0a-0000-4000-8000-000000000099"
    justification: "BDD admin memory mismatch"
    """
    Then the gRPC response should have status "ABORTED"
    And the gRPC error message should contain "expected_active_revision_id_mismatch"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'agk-mismatch'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "agk-mismatch"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE id = 'af1c1d0a-0000-4000-8000-000000000021'
    """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"_id": "af1c1d0a-0000-4000-8000-000000000021"}}
    """
    Then the MongoDB result should match:
      | count |
      | 0     |


  Scenario: Admin gRPC retry with different value returns ABORTED
    Given I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "admin-grpc-diff"
    key: "agk-diff"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "af1c1d0a-0000-4000-8000-000000000030"
    justification: "BDD admin memory setup"
    """
    Then the gRPC response should not have an error

    When I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "admin-grpc-diff"
    key: "agk-diff"
    value {
      fields { key: "v" value { number_value: 2 } }
    }
    revision_id: "af1c1d0a-0000-4000-8000-000000000030"
    justification: "BDD admin memory retry conflict"
    """
    Then the gRPC response should have status "ABORTED"
    And the gRPC error message should contain "memory_revision_already_exists"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'agk-diff'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "agk-diff"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |

  Scenario: Admin gRPC PutMemory rejects empty revision_id, bad revision value and orphan expected_revision_id
    When I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "admin-grpc-more"
    key: "agk-empty"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: ""
    justification: "BDD admin idempotency"
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"
    When I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "admin-grpc-more"
    key: "agk-bad-rev"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "af1c1d0a-0000-4000-8000-000000000040"
    revision: 2
    justification: "BDD admin idempotency"
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"
    When I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "admin-grpc-more"
    key: "agk-orphan"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    expected_revision_id: "af1c1d0a-0000-4000-8000-000000000041"
    justification: "BDD admin idempotency"
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key IN ('agk-empty', 'agk-bad-rev', 'agk-orphan')
    """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": {"$in": ["agk-empty", "agk-bad-rev", "agk-orphan"]}}}
    """
    Then the MongoDB result should match:
      | count |
      | 0     |

  Scenario: Admin gRPC PutMemory with expected_revision_id when no active memory exists returns ABORTED
    When I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "admin-grpc-none"
    key: "agk-no-prior"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "af1c1d0a-0000-4000-8000-000000000050"
    expected_revision_id: "af1c1d0a-0000-4000-8000-000000000051"
    justification: "BDD admin idempotency"
    """
    Then the gRPC response should have status "ABORTED"
    And the gRPC error message should contain "expected_active_revision_id_mismatch"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'agk-no-prior'
    """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "agk-no-prior"}}
    """
    Then the MongoDB result should match:
      | count |
      | 0     |

  Scenario: Admin gRPC exact retry suppresses side effects
    When I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "admin-grpc-side"
    key: "agk-side"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "af1c1d0a-0000-4000-8000-000000000060"
    justification: "BDD admin idempotency"
    """
    Then the gRPC response should not have an error
    When I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "admin-grpc-side"
    key: "agk-side"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "af1c1d0a-0000-4000-8000-000000000060"
    justification: "BDD admin idempotency"
    """
    Then the gRPC response should not have an error
    When I call GET "/v1/memories/events?ns=user&ns=alice&ns=cognition.v1&ns=admin-grpc-side&kinds=add"
    Then the response status should be 200
    And the response body "events" should have 1 item
    When I call GET "/v1/memories/events?ns=user&ns=alice&ns=cognition.v1&ns=admin-grpc-side&kinds=update"
    Then the response status should be 200
    And the response body "events" should have 0 items
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'agk-side'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "agk-side"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'agk-side' AND archived_at IS NOT NULL
    """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "agk-side", "archived_at": {"$exists": true}}}
    """
    Then the MongoDB result should match:
      | count |
      | 0     |

  Scenario: Non-admin AdminMemoriesService retry of an admin-written revision_id is rejected
    When I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "admin-grpc-authz"
    key: "agk-authz"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "af1c1d0a-0000-4000-8000-000000000070"
    justification: "BDD admin idempotency"
    """
    Then the gRPC response should not have an error
    When I am authenticated as user "eve"
    When I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "admin-grpc-authz"
    key: "agk-authz"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "af1c1d0a-0000-4000-8000-000000000070"
    justification: "BDD admin idempotency"
    """
    Then the gRPC response should have status "PERMISSION_DENIED"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'agk-authz'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "agk-authz"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |

  Scenario: Admin gRPC evicted row retry with create-only mode returns ABORTED when active row exists
    Given I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "admin-grpc-evict-idem"
    key: "agk-evict-k1"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "af1c1d0a-0000-4000-8000-0000000000e1"
    revision: 1
    justification: "BDD admin idempotency"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "af1c1d0a-0000-4000-8000-0000000000e1"

    When I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "admin-grpc-evict-idem"
    key: "agk-evict-k1"
    value {
      fields { key: "v" value { number_value: 2 } }
    }
    revision_id: "af1c1d0a-0000-4000-8000-0000000000e2"
    justification: "BDD admin idempotency"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "af1c1d0a-0000-4000-8000-0000000000e2"

    When I execute SQL query:
    """
    DELETE FROM memories WHERE id = 'af1c1d0a-0000-4000-8000-0000000000e1'
    """
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "delete", "filter": {"_id": "af1c1d0a-0000-4000-8000-0000000000e1"}}
    """

    When I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "admin-grpc-evict-idem"
    key: "agk-evict-k1"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "af1c1d0a-0000-4000-8000-0000000000e1"
    revision: 1
    justification: "BDD admin idempotency"
    """
    Then the gRPC response should have status "ABORTED"

    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE id = 'af1c1d0a-0000-4000-8000-0000000000e1'
    """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"_id": "af1c1d0a-0000-4000-8000-0000000000e1"}}
    """
    Then the MongoDB result should match:
      | count |
      | 0     |

  Scenario: Admin gRPC PUT with revision_id after HardDeleteEvictableUpdates is treated as a new write
    Given I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "admin-grpc-hd-idem"
    key: "agk-hd-k1"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "af1c1d0a-0000-4000-8000-0000000000f1"
    justification: "BDD admin idempotency"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "af1c1d0a-0000-4000-8000-0000000000f1"

    When I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "admin-grpc-hd-idem"
    key: "agk-hd-k1"
    value {
      fields { key: "v" value { number_value: 2 } }
    }
    revision_id: "af1c1d0a-0000-4000-8000-0000000000f2"
    justification: "BDD admin idempotency"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "af1c1d0a-0000-4000-8000-0000000000f2"

    When I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "admin-grpc-hd-idem"
    key: "agk-hd-k1"
    value {
      fields { key: "v" value { number_value: 1 } }
    }
    revision_id: "af1c1d0a-0000-4000-8000-0000000000f1"
    justification: "BDD admin idempotency"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "af1c1d0a-0000-4000-8000-0000000000f1"

    When I execute SQL query:
    """
    DELETE FROM memories WHERE id = 'af1c1d0a-0000-4000-8000-0000000000f1'
    """
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "delete", "filter": {"_id": "af1c1d0a-0000-4000-8000-0000000000f1"}}
    """

    When I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "admin-grpc-hd-idem"
    key: "agk-hd-k1"
    value {
      fields { key: "v" value { number_value: 3 } }
    }
    revision_id: "af1c1d0a-0000-4000-8000-0000000000f1"
    justification: "BDD admin idempotency"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should be "af1c1d0a-0000-4000-8000-0000000000f1"

    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'agk-hd-k1' AND archived_at IS NULL
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "agk-hd-k1", "archived_at": {"$exists": false}}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |

    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE id = 'af1c1d0a-0000-4000-8000-0000000000f2' AND archived_at IS NOT NULL
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"_id": "af1c1d0a-0000-4000-8000-0000000000f2", "archived_at": {"$exists": true}}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
