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
