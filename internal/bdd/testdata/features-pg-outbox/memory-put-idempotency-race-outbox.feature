Feature: Concurrent PutMemory Idempotency Race (PostgreSQL outbox)
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

    # One outbox record for the raced revision; the seed write has its own record.
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM outbox_events WHERE kind = 'memory' AND data->>'memory' = '77777777-7777-4777-8777-000000000002'
    """
    Then the SQL result should match:
      | count |
      | 1     |
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'race-outbox-key' AND archived_at IS NULL
    """
    Then the SQL result should match:
      | count |
      | 1     |
