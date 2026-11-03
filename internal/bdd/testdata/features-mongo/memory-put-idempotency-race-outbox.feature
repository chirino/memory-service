Feature: Concurrent PutMemory Idempotency Race (MongoDB outbox)
  Concurrent PutMemory retries with one revision_id produce exactly one outbox record.

  Background:
    Given I am authenticated as user "alice"

  Scenario: Concurrent exact retries emit exactly one memory outbox record
    Given I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "race-outbox"],
      "key": "race-outbox-key",
      "value": { "version": 1 }
    }
    """
    Then the response status should be 200

    When I call PUT "/v1/memories" concurrently 8 times with body:
    """
    {
      "namespace": ["user", "alice", "race-outbox"],
      "key": "race-outbox-key",
      "value": { "version": 2 },
      "revision_id": "77777777-7777-4777-8777-000000000002"
    }
    """
    Then exactly one response should have status 201 and the rest should have status 200
    And every concurrent response body field "id" should be "77777777-7777-4777-8777-000000000002"

    When I call GET "/v1/memories/events?ns=user&ns=alice&ns=race-outbox&kinds=update"
    Then the response status should be 200
    And the response body "events" should have 1 item

    When I execute MongoDB query:
    """
    {"collection":"outbox_events","operation":"count","filter":{"kind":"memory","data.memory":"77777777-7777-4777-8777-000000000002"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "race-outbox-key", "archived_at": {"$exists": false}}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
