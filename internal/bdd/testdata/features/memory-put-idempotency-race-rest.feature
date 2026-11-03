Feature: Concurrent PutMemory Idempotency Race
  Concurrent retries of PUT with a single revision_id create exactly one revision,
  return exactly one 201 response, and suppress duplicate side effects.

  Background:
    Given I am authenticated as user "alice"

  Scenario: Concurrent exact retries create one revision and archive predecessor once
    # Seed a predecessor so the race has something to archive. This write has no
    # revision_id, so it is a plain update of a new key and returns 200.
    Given I call PUT "/v1/memories" with body:
    """
    {
      "namespace": ["user", "alice", "race-mem"],
      "key": "race-key-01",
      "value": { "version": 1 }
    }
    """
    Then the response status should be 200
    And set "predecessorId" to the json response field "id"

    # 8 identical requests with the same revision_id: exactly one wins (201), the other
    # seven are recognized as exact retries (200). No 409 and no 5xx may appear.
    When I call PUT "/v1/memories" concurrently 8 times with body:
    """
    {
      "namespace": ["user", "alice", "race-mem"],
      "key": "race-key-01",
      "value": { "version": 2 },
      "revision_id": "77777777-7777-4777-8777-000000000001"
    }
    """
    Then exactly one response should have status 201 and the rest should have status 200
    And every concurrent response body field "id" should be "77777777-7777-4777-8777-000000000001"

    # The active revision is the raced one, and it is not the predecessor.
    When I call GET "/v1/memories?ns=user&ns=alice&ns=race-mem&key=race-key-01"
    Then the response status should be 200
    And the response body "id" should be "77777777-7777-4777-8777-000000000001"
    And the response body field "value.version" should be "2"

    # Memory events: one add (the seed) and exactly one update (the race winner).
    # Memory events are admin-only on the SSE stream, so assert through the events endpoint.
    When I call GET "/v1/memories/events?ns=user&ns=alice&ns=race-mem&kinds=update"
    Then the response status should be 200
    And the response body "events" should have 1 item
    When I call GET "/v1/memories/events?ns=user&ns=alice&ns=race-mem&kinds=add"
    Then the response status should be 200
    And the response body "events" should have 1 item

    # Exactly one row carries the raced ID.
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE id = '77777777-7777-4777-8777-000000000001'
    """
    Then the SQL result should match:
      | count |
      | 1     |

    # Seed + winner: two rows for the key, no duplicates from the losers.
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'race-key-01'
    """
    Then the SQL result should match:
      | count |
      | 2     |

    # One active row for (namespace, key).
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'race-key-01' AND archived_at IS NULL
    """
    Then the SQL result should match:
      | count |
      | 1     |

    # The predecessor was archived exactly once, as superseded (deleted_reason = 0).
    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE key = 'race-key-01' AND archived_at IS NOT NULL AND deleted_reason = 0
    """
    Then the SQL result should match:
      | count |
      | 1     |

    When I execute SQL query:
    """
    SELECT COUNT(*) AS count FROM memories WHERE id = '${predecessorId}' AND archived_at IS NOT NULL AND deleted_reason = 0
    """
    Then the SQL result should match:
      | count |
      | 1     |

    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"_id": "77777777-7777-4777-8777-000000000001"}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |

    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "race-key-01"}}
    """
    Then the MongoDB result should match:
      | count |
      | 2     |

    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "race-key-01", "archived_at": {"$exists": false}}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |

    When I execute MongoDB query:
    """
    {"collection": "memories", "operation": "count", "filter": {"key": "race-key-01", "archived_at": {"$exists": true}, "deleted_reason": 0}}
    """
    Then the MongoDB result should match:
      | count |
      | 1     |
