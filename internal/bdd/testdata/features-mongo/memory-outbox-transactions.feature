Feature: MongoDB memory outbox transactions
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

  Scenario: Store a memory through gRPC with the outbox enabled
    When I send gRPC request "MemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "outbox"
    key: "grpc"
    value {
      fields {
        key: "source"
        value { string_value: "grpc" }
      }
    }
    """
    Then the gRPC response should not have an error
    When I send gRPC request "MemoriesService/GetMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "outbox"
    key: "grpc"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "value.source" should be "grpc"

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
    When I execute MongoDB query:
    """
    {"collection":"outbox_events","operation":"count","filter":{"kind":"memory","data.memory":"66666666-6666-4666-8666-000000000001"}}
    """
    Then the MongoDB result should match:
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
    When I execute MongoDB query:
    """
    {"collection":"outbox_events","operation":"count","filter":{"kind":"memory","data.memory":"66666666-6666-4666-8666-000000000001"}}
    """
    Then the MongoDB result should match:
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
    When I execute MongoDB query:
    """
    {"collection":"outbox_events","operation":"count","filter":{"kind":"memory","data.memory":"66666666-6666-4666-8666-000000000002"}}
    """
    Then the MongoDB result should match:
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
    When I execute MongoDB query:
    """
    {"collection":"outbox_events","operation":"count","filter":{"kind":"memory","data.memory":"66666666-6666-4666-8666-000000000002"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |

  Scenario: An exact PutMemory retry in update mode produces exactly one outbox event
    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "outbox"],
      "key": "update-retry",
      "value": { "v": 1 },
      "revision_id": "66666666-6666-4666-8666-0000000000a1"
    }
    """
    Then the response status should be 201
    When I execute MongoDB query:
    """
    {"collection":"outbox_events","operation":"count","filter":{"kind":"memory","data.memory":"66666666-6666-4666-8666-0000000000a1"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "outbox"],
      "key": "update-retry",
      "value": { "v": 2 },
      "revision_id": "66666666-6666-4666-8666-0000000000a2",
      "expected_revision_id": "66666666-6666-4666-8666-0000000000a1"
    }
    """
    Then the response status should be 201
    When I execute MongoDB query:
    """
    {"collection":"outbox_events","operation":"count","filter":{"kind":"memory","data.memory":"66666666-6666-4666-8666-0000000000a2"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "outbox"],
      "key": "update-retry",
      "value": { "v": 2 },
      "revision_id": "66666666-6666-4666-8666-0000000000a2",
      "expected_revision_id": "66666666-6666-4666-8666-0000000000a1"
    }
    """
    Then the response status should be 200
    When I execute MongoDB query:
    """
    {"collection":"outbox_events","operation":"count","filter":{"kind":"memory","data.memory":"66666666-6666-4666-8666-0000000000a2"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection":"outbox_events","operation":"count","filter":{"kind":"memory","data.memory":"66666666-6666-4666-8666-0000000000a1"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |

  Scenario: A rejected PutMemory conflict produces no outbox event
    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "outbox"],
      "key": "conflict",
      "value": { "v": 1 },
      "revision_id": "66666666-6666-4666-8666-0000000000b1"
    }
    """
    Then the response status should be 201
    When I execute MongoDB query:
    """
    {"collection":"outbox_events","operation":"count","filter":{"kind":"memory","data.memory":"66666666-6666-4666-8666-0000000000b1"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "outbox"],
      "key": "conflict",
      "value": { "v": 2 },
      "revision_id": "66666666-6666-4666-8666-0000000000b1"
    }
    """
    Then the response status should be 409
    When I execute MongoDB query:
    """
    {"collection":"outbox_events","operation":"count","filter":{"kind":"memory","data.memory":"66666666-6666-4666-8666-0000000000b1"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
    When I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "outbox"],
      "key": "conflict",
      "value": { "v": 3 },
      "revision_id": "66666666-6666-4666-8666-0000000000b2",
      "revision": 1
    }
    """
    Then the response status should be 409
    When I execute MongoDB query:
    """
    {"collection":"outbox_events","operation":"count","filter":{"kind":"memory","data.memory":"66666666-6666-4666-8666-0000000000b2"}}
    """
    Then the MongoDB result should match:
      | count |
      | 0     |

  Scenario: An exact admin PutMemory retry produces exactly one outbox event
    Given I am authenticated as admin user "alice"
    When I call PUT "/admin/v1/memories?justification=outbox%20admin%20retry" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "outbox"],
      "key": "admin-retry",
      "value": { "v": 1 },
      "revision_id": "66666666-6666-4666-8666-0000000000c1",
      "revision": 1
    }
    """
    Then the response status should be 201
    When I execute MongoDB query:
    """
    {"collection":"outbox_events","operation":"count","filter":{"kind":"memory","data.memory":"66666666-6666-4666-8666-0000000000c1"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
    When I call PUT "/admin/v1/memories?justification=outbox%20admin%20retry" with body:
    """
    {
      "namespace": ["user", "alice", "cognition.v1", "outbox"],
      "key": "admin-retry",
      "value": { "v": 1 },
      "revision_id": "66666666-6666-4666-8666-0000000000c1",
      "revision": 1
    }
    """
    Then the response status should be 200
    When I execute MongoDB query:
    """
    {"collection":"outbox_events","operation":"count","filter":{"kind":"memory","data.memory":"66666666-6666-4666-8666-0000000000c1"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |

  Scenario: An exact admin PutMemory gRPC retry produces exactly one outbox event
    Given I am authenticated as admin user "alice"
    When I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "outbox"
    key: "admin-grpc-retry"
    value {
      fields {
        key: "source"
        value { string_value: "admin-grpc-retry" }
      }
    }
    revision_id: "66666666-6666-4666-8666-0000000000d1"
    revision: 1
    justification: "outbox admin grpc retry"
    """
    Then the gRPC response should not have an error
    When I execute MongoDB query:
    """
    {"collection":"outbox_events","operation":"count","filter":{"kind":"memory","data.memory":"66666666-6666-4666-8666-0000000000d1"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
    When I send gRPC request "AdminMemoriesService/PutMemory" with body:
    """
    namespace: "user"
    namespace: "alice"
    namespace: "cognition.v1"
    namespace: "outbox"
    key: "admin-grpc-retry"
    value {
      fields {
        key: "source"
        value { string_value: "admin-grpc-retry" }
      }
    }
    revision_id: "66666666-6666-4666-8666-0000000000d1"
    revision: 1
    justification: "outbox admin grpc retry"
    """
    Then the gRPC response should not have an error
    When I execute MongoDB query:
    """
    {"collection":"outbox_events","operation":"count","filter":{"kind":"memory","data.memory":"66666666-6666-4666-8666-0000000000d1"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
