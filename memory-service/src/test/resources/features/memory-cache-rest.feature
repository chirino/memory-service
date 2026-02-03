Feature: Memory entries cache via REST
  As an agent
  I want memory entries to be cached
  So that repeated reads are fast

  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "Cache test"

  Scenario: Cache is populated when entries are added and used on subsequent reads
    Given I am authenticated as agent with API key "test-agent-key"
    # Adding an entry proactively populates the cache
    And the conversation has a memory entry "Hello from cache test" with epoch 1 and contentType "test.v1"
    And I record the current cache metrics
    # First read - should be cache hit (cache was populated by append)
    When I list memory entries for the conversation with epoch "LATEST"
    Then the response status should be 200
    And the response should contain 1 entry
    And the cache hit count should have increased by at least 1
    # Record metrics again before second read
    Given I record the current cache metrics
    # Second read - should also be cache hit
    When I list memory entries for the conversation with epoch "LATEST"
    Then the response status should be 200
    And the response should contain 1 entry
    And the cache hit count should have increased by at least 1

  Scenario: Cache is updated after sync
    Given I am authenticated as agent with API key "test-agent-key-b"
    And the conversation has a memory entry "Initial content" with epoch 1 and contentType "test.v1"
    And I record the current cache metrics
    # Read to populate cache
    When I list memory entries for the conversation with epoch "LATEST"
    Then the response status should be 200
    And the response should contain 1 entry
    # Sync new content (should update cache, not invalidate it)
    When I sync memory entries with request:
      """
      {
        "channel": "MEMORY",
        "contentType": "test.v1",
        "content": [{"type": "text", "text": "Initial content"}, {"type": "text", "text": "Additional content"}]
      }
      """
    Then the response status should be 200
    # Record metrics before next read
    Given I record the current cache metrics
    # Read after sync - should be cache hit (cache was updated by sync)
    When I list memory entries for the conversation with epoch "LATEST"
    Then the response status should be 200
    And the cache hit count should have increased by at least 1

  Scenario: Cache is updated after append (not just sync)
    Given I am authenticated as agent with API key "test-agent-key-c"
    And the conversation has a memory entry "Initial content" with epoch 1 and contentType "test.v1"
    And I record the current cache metrics
    # Read to populate cache
    When I list memory entries for the conversation with epoch "LATEST"
    Then the response status should be 200
    And the response should contain 1 entry
    # Append a new memory entry (NOT using sync API)
    When I append an entry with content "Appended content" and channel "MEMORY" and contentType "test.v1"
    Then the response status should be 201
    # Record metrics before next read
    Given I record the current cache metrics
    # Read after append - should be cache hit (cache was updated by append)
    When I list memory entries for the conversation with epoch "LATEST"
    Then the response status should be 200
    And the response should contain 2 entries
    And the cache hit count should have increased by at least 1
