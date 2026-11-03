Feature: PostgreSQL memory outbox transactions
  Background:
    Given I am authenticated as user "alice"

  Scenario: Store a memory through REST with the outbox enabled
    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "outbox"],
      "key": "rest",
      "value": { "source": "rest" }
    }
    """
    Then the response status should be 200
    When I call GET "/v1/memories?ns=user&ns=alice&ns=outbox&key=rest"
    Then the response status should be 200
    And the response body field "value.source" should be "rest"

  Scenario: An exact PutMemory REST retry produces exactly one outbox event
    When I call PUT "/v1/memories" with body:
    """
    {
      "revision_id": "66666666-6666-4666-8666-000000000001",
      "revision": 1,
      "namespace": ["user", "alice", "outbox"],
      "key": "rest-retry",
      "value": { "source": "rest-retry" }
    }
    """
    Then the response status should be 201
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM outbox_events WHERE kind = 'memory' AND data->>'memory' = '66666666-6666-4666-8666-000000000001'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I call PUT "/v1/memories" with body:
    """
    {
      "revision_id": "66666666-6666-4666-8666-000000000001",
      "revision": 1,
      "namespace": ["user", "alice", "outbox"],
      "key": "rest-retry",
      "value": { "source": "rest-retry" }
    }
    """
    Then the response status should be 200
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM outbox_events WHERE kind = 'memory' AND data->>'memory' = '66666666-6666-4666-8666-000000000001'
    """
    Then the SQL result should match:
      | count |
      | 1     |

  Scenario: An exact PutMemory gRPC retry produces exactly one outbox event
    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    revision_id: "66666666-6666-4666-8666-000000000002"
    revision: 1
    namespace: "user"
    namespace: "alice"
    namespace: "outbox"
    key: "grpc-retry"
    value {
      fields {
        key: "source"
        value { string_value: "grpc-retry" }
      }
    }
    """
    Then the gRPC response should not have an error
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM outbox_events WHERE kind = 'memory' AND data->>'memory' = '66666666-6666-4666-8666-000000000002'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    revision_id: "66666666-6666-4666-8666-000000000002"
    revision: 1
    namespace: "user"
    namespace: "alice"
    namespace: "outbox"
    key: "grpc-retry"
    value {
      fields {
        key: "source"
        value { string_value: "grpc-retry" }
      }
    }
    """
    Then the gRPC response should not have an error
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM outbox_events WHERE kind = 'memory' AND data->>'memory' = '66666666-6666-4666-8666-000000000002'
    """
    Then the SQL result should match:
      | count |
      | 1     |
