Feature: Admin Memory REST API
  As an administrator
  I want to write memories across namespaces via admin REST endpoints
  So that I can store namespace-scoped cognition memories through admin authorization

  Background:
    Given I am authenticated as admin user "alice"

  Scenario: Admin can write memory in a user namespace via REST
    When I call PUT "/admin/v1/memories?justification=BDD%20admin%20memory%20write" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "facts"],
      "key": "fact-rest-001",
      "value": {
        "content": "User prefers light theme",
        "confidence": 0.90
      }
    }
    """
    Then the response status should be 200
    And the response body field "namespace[0]" should be "user"
    And the response body field "namespace[1]" should be "alice"
    And the response body field "namespace[2]" should be "cognition.v1"
    And the response body field "key" should be "fact-rest-001"
    And the response body field "revision" should not be null

  Scenario: Admin can update (archive) memory via REST
    Given I call PUT "/admin/v1/memories" with body:
    """
    {
      "namespace": ["user", "bob", "cognition.v1", "facts"],
      "key": "fact-to-archive",
      "value": {
        "content": "temporary"
      }
    }
    """
    When I call PATCH "/admin/v1/memories?ns=user&ns=bob&ns=cognition.v1&ns=facts&key=fact-to-archive&justification=BDD%20admin%20memory%20archive" with body:
    """
    {
      "archived": true
    }
    """
    Then the response status should be 204

  Scenario: Admin REST put rejects negative TTL
    When I call PUT "/admin/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "facts"],
      "key": "negative-ttl",
      "value": {
        "content": "invalid ttl"
      },
      "ttl_seconds": -1
    }
    """
    Then the response status should be 400
    And the response body should contain "ttl_seconds must be"

  Scenario: Admin REST put detects stale expected revision
    Given I call PUT "/admin/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "facts"],
      "key": "cas-rest",
      "value": {
        "content": "first value"
      }
    }
    """
    And the response status should be 200
    And set "adminRevision" to the json response field "revision"
    And I call PUT "/admin/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "facts"],
      "key": "cas-rest",
      "value": {
        "content": "second value"
      },
      "expected_revision": ${adminRevision}
    }
    """
    And the response status should be 200
    When I call PUT "/admin/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "facts"],
      "key": "cas-rest",
      "value": {
        "content": "stale value"
      },
      "expected_revision": ${adminRevision}
    }
    """
    Then the response status should be 409
    And the response body should contain "memory revision conflict"

  Scenario: Admin write with TTL via REST
    When I call PUT "/admin/v1/memories" with body:
    """
    {
      "namespace": ["user", "charlie", "cognition.v1", "facts"],
      "key": "temp-fact",
      "value": {
        "content": "expires in an hour"
      },
      "ttl_seconds": 3600
    }
    """
    Then the response status should be 200
    And the response body field "expiresAt" should not be null

  Scenario: Admin write with index fields via REST
    When I call PUT "/admin/v1/memories" with body:
    """
    {
      "namespace": ["user", "dave", "cognition.v1", "preferences"],
      "key": "indexed-pref",
      "value": {
        "theme": "dark",
        "language": "en"
      },
      "index": {
        "category": "ui",
        "priority": "high"
      }
    }
    """
    Then the response status should be 200

  Scenario: Non-admin receives 403 on admin memory endpoints
    Given I am authenticated as user "eve"
    When I call PUT "/admin/v1/memories" with body:
    """
    {
      "namespace": ["user", "eve", "test"],
      "key": "unauthorized",
      "value": {"test": "value"}
    }
    """
    Then the response status should be 403

  Scenario: Admin PUT with revision_id and revision: 1 creates memory idempotently
    When I call PUT "/admin/v1/memories?justification=admin-idem-create" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "admin-idem"],
      "key": "adm-create-1",
      "value": {"content": "admin facts"},
      "revision_id": "8f1c1d0a-0000-4000-8000-000000000001",
      "revision": 1
    }
    """
    Then the response status should be 201
    And the response body "id" should be "8f1c1d0a-0000-4000-8000-000000000001"
    And set "adminFirstCreatedAt" to the json response field "createdAt"

    When I call PUT "/admin/v1/memories?justification=admin-idem-create-retry" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "admin-idem"],
      "key": "adm-create-1",
      "value": {"content": "admin facts"},
      "revision_id": "8f1c1d0a-0000-4000-8000-000000000001",
      "revision": 1
    }
    """
    Then the response status should be 200
    And the response body "id" should be "8f1c1d0a-0000-4000-8000-000000000001"
    And the response body "createdAt" should be "${adminFirstCreatedAt}"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'adm-create-1'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "adm-create-1"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |


  Scenario: Admin create-only on active memory returns revision_already_exists
    Given I call PUT "/admin/v1/memories?justification=admin-setup" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "admin-idem-conflict"],
      "key": "adm-k1",
      "value": {"content": "initial"}
    }
    """
    Then the response status should be 200

    When I call PUT "/admin/v1/memories?justification=admin-create-only-attempt" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "admin-idem-conflict"],
      "key": "adm-k1",
      "value": {"content": "conflict"},
      "revision_id": "8f1c1d0a-0000-4000-8000-000000000002",
      "revision": 1
    }
    """
    Then the response status should be 409
    And the response body field "code" should be "revision_already_exists"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'adm-k1'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "adm-k1"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE id = '8f1c1d0a-0000-4000-8000-000000000002'
    """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"_id": "8f1c1d0a-0000-4000-8000-000000000002"}}
    """
    Then the MongoDB result should match:
      | count |
      | 0     |


  Scenario: Admin PUT with expected_revision_id updates memory idempotently
    Given I call PUT "/admin/v1/memories?justification=admin-cas-setup" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "admin-cas"],
      "key": "adm-cas-key",
      "value": {"v": 1},
      "revision_id": "8f1c1d0a-0000-4000-8000-000000000010"
    }
    """
    Then the response status should be 201

    When I call PUT "/admin/v1/memories?justification=admin-cas-update" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "admin-cas"],
      "key": "adm-cas-key",
      "value": {"v": 2},
      "revision_id": "8f1c1d0a-0000-4000-8000-000000000011",
      "expected_revision_id": "8f1c1d0a-0000-4000-8000-000000000010"
    }
    """
    Then the response status should be 201
    And the response body "id" should be "8f1c1d0a-0000-4000-8000-000000000011"

    When I call PUT "/admin/v1/memories?justification=admin-cas-retry" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "admin-cas"],
      "key": "adm-cas-key",
      "value": {"v": 2},
      "revision_id": "8f1c1d0a-0000-4000-8000-000000000011",
      "expected_revision_id": "8f1c1d0a-0000-4000-8000-000000000010"
    }
    """
    Then the response status should be 200
    And the response body "id" should be "8f1c1d0a-0000-4000-8000-000000000011"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'adm-cas-key'
    """
    Then the SQL result should match:
      | count |
      | 2     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "adm-cas-key"}}
    """
    Then the MongoDB result should match:
      | count |
      | 2     |
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'adm-cas-key' AND archived_at IS NULL
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "adm-cas-key", "archived_at": {"$exists": false}}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |


  Scenario: Admin PUT with mismatching expected_revision_id returns expected_active_revision_id_mismatch
    Given I call PUT "/admin/v1/memories?justification=admin-cas-setup" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "admin-cas-mismatch"],
      "key": "adm-mismatch-key",
      "value": {"v": 1},
      "revision_id": "8f1c1d0a-0000-4000-8000-000000000020"
    }
    """
    Then the response status should be 201

    When I call PUT "/admin/v1/memories?justification=admin-cas-mismatch" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "admin-cas-mismatch"],
      "key": "adm-mismatch-key",
      "value": {"v": 2},
      "revision_id": "8f1c1d0a-0000-4000-8000-000000000021",
      "expected_revision_id": "8f1c1d0a-0000-4000-8000-000000000099"
    }
    """
    Then the response status should be 409
    And the response body field "code" should be "expected_active_revision_id_mismatch"


  Scenario: Admin retry with different value returns memory_revision_already_exists
    Given I call PUT "/admin/v1/memories?justification=admin-setup" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "admin-diff"],
      "key": "diff-key",
      "value": {"v": 1},
      "revision_id": "8f1c1d0a-0000-4000-8000-000000000030"
    }
    """
    Then the response status should be 201

    When I call PUT "/admin/v1/memories?justification=admin-retry" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "admin-diff"],
      "key": "diff-key",
      "value": {"v": 2},
      "revision_id": "8f1c1d0a-0000-4000-8000-000000000030"
    }
    """
    Then the response status should be 409
    And the response body field "code" should be "memory_revision_already_exists"
    And the response body field "details.revisionId" should be "8f1c1d0a-0000-4000-8000-000000000030"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'diff-key'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "diff-key"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |

  Scenario: Admin empty revision_id returns 400
    When I call PUT "/admin/v1/memories?justification=admin-empty-rev-id" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "admin-invalid"],
      "key": "adm-empty-rev-id",
      "value": {"v": 1},
      "revision_id": ""
    }
    """
    Then the response status should be 400
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'adm-empty-rev-id'
    """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "adm-empty-rev-id"}}
    """
    Then the MongoDB result should match:
      | count |
      | 0     |

  Scenario: Admin PUT with expected_revision_id when no active memory exists returns expected_active_revision_id_mismatch
    When I call PUT "/admin/v1/memories?justification=admin-cas-nonexistent" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "admin-cas-none"],
      "key": "adm-no-prior",
      "value": {"v": 1},
      "revision_id": "8f1c1d0a-0000-4000-8000-000000000040",
      "expected_revision_id": "8f1c1d0a-0000-4000-8000-000000000041"
    }
    """
    Then the response status should be 409
    And the response body field "code" should be "expected_active_revision_id_mismatch"
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'adm-no-prior'
    """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "adm-no-prior"}}
    """
    Then the MongoDB result should match:
      | count |
      | 0     |

  Scenario: Non-admin retry of an admin-written revision_id receives 403
    Given I call PUT "/admin/v1/memories?justification=admin-authz-setup" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "admin-authz"],
      "key": "adm-authz-key",
      "value": {"v": 1},
      "revision_id": "8f1c1d0a-0000-4000-8000-000000000050"
    }
    """
    Then the response status should be 201

    When I am authenticated as user "eve"
    And I call PUT "/admin/v1/memories?justification=eve-retry" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "admin-authz"],
      "key": "adm-authz-key",
      "value": {"v": 1},
      "revision_id": "8f1c1d0a-0000-4000-8000-000000000050"
    }
    """
    Then the response status should be 403
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'adm-authz-key'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "adm-authz-key"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |

  Scenario: Admin evicted row retry with create-only mode returns revision_already_exists when active row exists
    Given I am authenticated as admin user "alice"
    When I call PUT "/admin/v1/memories?justification=admin-evict" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "admin-evict-idem"],
      "key": "adm-evict-k1",
      "value": {"v": 1},
      "revision_id": "8f1c1d0a-0000-4000-8000-0000000000e1",
      "revision": 1
    }
    """
    Then the response status should be 201
    And the response body "id" should be "8f1c1d0a-0000-4000-8000-0000000000e1"

    When I call PUT "/admin/v1/memories?justification=admin-evict" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "admin-evict-idem"],
      "key": "adm-evict-k1",
      "value": {"v": 2},
      "revision_id": "8f1c1d0a-0000-4000-8000-0000000000e2"
    }
    """
    Then the response status should be 201
    And the response body "id" should be "8f1c1d0a-0000-4000-8000-0000000000e2"

    When I execute SQL query:
    """
    DELETE FROM memories WHERE id = '8f1c1d0a-0000-4000-8000-0000000000e1'
    """
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "delete", "filter": {"_id": "8f1c1d0a-0000-4000-8000-0000000000e1"}}
    """

    When I call PUT "/admin/v1/memories?justification=admin-evict" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "admin-evict-idem"],
      "key": "adm-evict-k1",
      "value": {"v": 1},
      "revision_id": "8f1c1d0a-0000-4000-8000-0000000000e1",
      "revision": 1
    }
    """
    Then the response status should be 409
    And the response body field "code" should be "revision_already_exists"
    And the response body field "error" should be "an active memory already exists for the namespace and key"

    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE id = '8f1c1d0a-0000-4000-8000-0000000000e1'
    """
    Then the SQL result should match:
      | count |
      | 0     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"_id": "8f1c1d0a-0000-4000-8000-0000000000e1"}}
    """
    Then the MongoDB result should match:
      | count |
      | 0     |

  Scenario: Admin PUT with revision_id after HardDeleteEvictableUpdates is treated as a new write
    Given I am authenticated as admin user "alice"
    When I call PUT "/admin/v1/memories?justification=admin-hd" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "admin-hd-idem"],
      "key": "adm-hd-k1",
      "value": {"v": 1},
      "revision_id": "8f1c1d0a-0000-4000-8000-0000000000f1"
    }
    """
    Then the response status should be 201
    And the response body "id" should be "8f1c1d0a-0000-4000-8000-0000000000f1"

    When I call PUT "/admin/v1/memories?justification=admin-hd" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "admin-hd-idem"],
      "key": "adm-hd-k1",
      "value": {"v": 2},
      "revision_id": "8f1c1d0a-0000-4000-8000-0000000000f2"
    }
    """
    Then the response status should be 201
    And the response body "id" should be "8f1c1d0a-0000-4000-8000-0000000000f2"

    When I call PUT "/admin/v1/memories?justification=admin-hd" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "admin-hd-idem"],
      "key": "adm-hd-k1",
      "value": {"v": 1},
      "revision_id": "8f1c1d0a-0000-4000-8000-0000000000f1"
    }
    """
    Then the response status should be 200
    And the response body "id" should be "8f1c1d0a-0000-4000-8000-0000000000f1"

    When I execute SQL query:
    """
    DELETE FROM memories WHERE id = '8f1c1d0a-0000-4000-8000-0000000000f1'
    """
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "delete", "filter": {"_id": "8f1c1d0a-0000-4000-8000-0000000000f1"}}
    """

    When I call PUT "/admin/v1/memories?justification=admin-hd" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "admin-hd-idem"],
      "key": "adm-hd-k1",
      "value": {"v": 3},
      "revision_id": "8f1c1d0a-0000-4000-8000-0000000000f1"
    }
    """
    Then the response status should be 201
    And the response body "id" should be "8f1c1d0a-0000-4000-8000-0000000000f1"

    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'adm-hd-k1' AND archived_at IS NULL
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "adm-hd-k1", "archived_at": {"$exists": false}}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |

    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE id = '8f1c1d0a-0000-4000-8000-0000000000f2' AND archived_at IS NOT NULL
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"_id": "8f1c1d0a-0000-4000-8000-0000000000f2", "archived_at": {"$exists": true}}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
