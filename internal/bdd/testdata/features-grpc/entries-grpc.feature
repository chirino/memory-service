Feature: Entries gRPC API
  As a client of the memory service
  I want to manage entries via gRPC
  So that I can store and retrieve conversation history using gRPC

  Background:
    Given I am authenticated as user "alice"
    And I am authenticated as agent with API key "test-agent-key"
    And I have a conversation with title "Test Conversation"
    And the conversation has an entry "Hello from Alice"

  Scenario: List entries via gRPC
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: HISTORY
    page {
      page_size: 10
    }
    """
    Then the gRPC response should not have an error
    And set "entryId" to the gRPC response field "entries[0].id"
    And the gRPC response field "entries[0].clientId" should not be null
    And the gRPC response text should match text proto:
    """
    entries {
      conversation_id: "${conversationId}"
      user_id: "alice"
      channel: HISTORY
      content_type: "history"
    }
    """

  Scenario: List entries defaults to the REST page size via gRPC
    Given the conversation has 30 entries
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: HISTORY
    """
    Then the gRPC response should not have an error
    And the gRPC response should contain 31 entries

  Scenario: List entries with pagination via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has 5 entries
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: HISTORY
    page {
      page_size: 2
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response should contain 2 entries
    And the gRPC response field "pageInfo.nextPageToken" should not be null
    And the gRPC response text should match text proto:
    """
    entries {
      conversation_id: "${conversationId}"
      channel: HISTORY
      content_type: "history"
    }
    page_info {
      next_page_token: "${response.body.pageInfo.nextPageToken}"
    }
    """

  Scenario: List the tail and page backward via gRPC
    Given the conversation has 5 entries
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: HISTORY
    tail: true
    page { page_size: 2 }
    """
    Then the gRPC response should not have an error
    And the gRPC response should contain 2 entries
    And the gRPC response field "entries[0].content[0].text" should be "Entry 4"
    And the gRPC response field "entries[1].content[0].text" should be "Entry 5"
    And the gRPC response field "pageInfo.previousPageToken" should not be null
    And set "grpcBeforePageToken" to the gRPC response field "pageInfo.previousPageToken"
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: HISTORY
    before_page_token: "${grpcBeforePageToken}"
    page { page_size: 2 }
    """
    Then the gRPC response should not have an error
    And the gRPC response should contain 2 entries
    And the gRPC response field "entries[0].content[0].text" should be "Entry 2"
    And the gRPC response field "entries[1].content[0].text" should be "Entry 3"
    And the gRPC response field "pageInfo.nextPageToken" should not be null

  Scenario: Admin lists the tail via gRPC
    Given the conversation has 3 entries
    And I am authenticated as admin user "alice"
    When I send gRPC request "AdminEntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: HISTORY
    tail: true
    page { page_size: 2 }
    """
    Then the gRPC response should not have an error
    And the gRPC response should contain 2 entries
    And the gRPC response field "entries[0].content[0].text" should be "Entry 2"
    And the gRPC response field "entries[1].content[0].text" should be "Entry 3"
    And the gRPC response field "pageInfo.previousPageToken" should not be null

  Scenario: User entry listing rejects a malformed backward token via gRPC
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: HISTORY
    before_page_token: "not-a-uuid"
    page { page_size: 2 }
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"

  Scenario: Admin entry listing rejects a malformed backward token via gRPC
    Given I am authenticated as admin user "alice"
    When I send gRPC request "AdminEntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: HISTORY
    before_page_token: "not-a-uuid"
    page { page_size: 2 }
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"

  Scenario: User entry listing rejects a malformed forward token via gRPC
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: HISTORY
    page { page_size: 2 page_token: "not-a-uuid" }
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"

  Scenario: Admin entry listing rejects a malformed forward token via gRPC
    Given I am authenticated as admin user "alice"
    When I send gRPC request "AdminEntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: HISTORY
    page { page_size: 2 page_token: "not-a-uuid" }
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"

  Scenario: List entries with channel filter via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has an entry "Memory entry" in channel "CONTEXT" with contentType "test.v1"
    And the conversation has an entry "History entry" in channel "HISTORY"
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: CONTEXT
    page {
      page_size: 10
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response should contain 1 entry
    And the gRPC response text should match text proto:
    """
    entries {
      conversation_id: "${conversationId}"
      channel: CONTEXT
      content_type: "test.v1"
    }
    """

  Scenario: Agent listing without channel includes context and history via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has an entry "Default visible context" in channel "CONTEXT" with contentType "test.v1"
    And the conversation has an entry "Default visible history" in channel "HISTORY"
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    page {
      page_size: 20
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "entries" should not be null
    And the gRPC response text should match text proto:
    """
    entries {
      channel: HISTORY
    }
    entries {
      channel: CONTEXT
      content_type: "test.v1"
    }
    """

  Scenario: Agent can filter entries by agent id via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    When I send gRPC request "EntriesService/AppendEntry" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      channel: CONTEXT
      content_type: "agent/state"
      agent_id: "agent-a"
      content {
        struct_value {
          fields {
            key: "text"
            value { string_value: "state from agent a" }
          }
        }
      }
    }
    """
    Then the gRPC response should not have an error
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: CONTEXT
    agent_id: "agent-a"
    page {
      page_size: 20
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response should contain 1 entry
    And the gRPC response field "entries[0].content[0].text" should be "state from agent a"

  Scenario: User can append default history entry without client id via gRPC
    Given I am authenticated as user "alice"
    When I send gRPC request "EntriesService/AppendEntry" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      content_type: "custom/message"
      content {
        struct_value {
          fields {
            key: "text"
            value { string_value: "history without client id" }
          }
        }
      }
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "contentType" should be "custom/message"

  Scenario: Agent can append a batch of entries via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    When I send gRPC request "EntriesService/AppendEntries" with body:
    """
    conversation_id: "${conversationId}"
    entries {
      channel: CONTEXT
      content_type: "test.v1"
      agent_id: "agent-batch"
      content {
        string_value: "batch entry one"
      }
    }
    entries {
      channel: CONTEXT
      content_type: "test.v1"
      agent_id: "agent-batch"
      content {
        string_value: "batch entry two"
      }
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "entries" should have size 2
    And the gRPC response field "entries[0].content[0]" should be "batch entry one"
    And the gRPC response field "entries[0].agentId" should be "agent-batch"
    And the gRPC response field "entries[1].content[0]" should be "batch entry two"
    And the gRPC response field "entries[1].agentId" should be "agent-batch"

  Scenario: User can append history entry with indexed content via gRPC
    Given I am authenticated as user "alice"
    When I send gRPC request "EntriesService/AppendEntry" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      channel: HISTORY
      content_type: "history"
      indexed_content: "indexed hello"
      content {
        struct_value {
          fields {
            key: "role"
            value { string_value: "USER" }
          }
          fields {
            key: "text"
            value { string_value: "hello indexed" }
          }
        }
      }
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "indexedContent" should be "indexed hello"

  Scenario: Agent can filter context entries by epoch via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has a context entry "Epoch One" with epoch 1 and contentType "test.v1"
    And the conversation has a context entry "Epoch Two" with epoch 2 and contentType "test.v1"
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: CONTEXT
    epoch_filter: "1"
    page {
      page_size: 10
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response should contain 1 entry
    And the gRPC response field "entries[0].content[0].text" should be "Epoch One"

  Scenario: Agent can bound context entries by entry id via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has a context entry "gRPC context before bound" with epoch 1 and contentType "test.v1"
    And the conversation has an entry "gRPC history bound"
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: HISTORY
    page {
      page_size: 10
    }
    """
    Then the gRPC response should not have an error
    And set "grpcBoundEntryId" to the gRPC response field "entries[1].id"
    And the conversation has a context entry "gRPC context after bound" with epoch 1 and contentType "test.v1"
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: CONTEXT
    epoch_filter: "1"
    up_to_entry_id: "${grpcBoundEntryId | uuid_to_hex_string}"
    page {
      page_size: 10
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response should contain 1 entry
    And the gRPC response field "entries[0].content[0].text" should be "gRPC context before bound"

  Scenario: Admin can bound context entries by entry id via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has a context entry "Admin gRPC context before bound" with epoch 1 and contentType "test.v1"
    And the conversation has an entry "Admin gRPC history bound"
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: HISTORY
    page {
      page_size: 10
    }
    """
    Then the gRPC response should not have an error
    And set "adminGrpcBoundEntryId" to the gRPC response field "entries[1].id"
    And the conversation has a context entry "Admin gRPC context after bound" with epoch 1 and contentType "test.v1"
    Given I am authenticated as admin user "alice"
    When I send gRPC request "AdminEntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: CONTEXT
    epoch_filter: "1"
    up_to_entry_id: "${adminGrpcBoundEntryId | uuid_to_hex_string}"
    page {
      page_size: 10
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response should contain 1 entry
    And the gRPC response field "entries[0].content[0].text" should be "Admin gRPC context before bound"

  Scenario: Sync context entries via gRPC is no-op when there are no changes
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has a context entry "Stable gRPC epoch" with epoch 1 and contentType "test.v1"
    When I send gRPC request "EntriesService/SyncEntries" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      channel: CONTEXT
      content_type: "test.v1"
      content {
        struct_value {
          fields {
            key: "type"
            value {
              string_value: "text"
            }
          }
          fields {
            key: "text"
            value {
              string_value: "Stable gRPC epoch"
            }
          }
        }
      }
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "epoch" should be "1"
    And the gRPC response field "noOp" should be true
    And the gRPC response field "epochIncremented" should be false
    And the gRPC response should not have entry

  Scenario: Sync context entries via gRPC creates a new epoch when history diverges
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has a context entry "Original epoch entry" with epoch 1 and contentType "test.v1"
    When I send gRPC request "EntriesService/SyncEntries" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      channel: CONTEXT
      content_type: "test.v1"
      content {
        string_value: "Updated epoch entry"
      }
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "epoch" should be "2"
    And the gRPC response field "noOp" should be false
    And the gRPC response field "epochIncremented" should be true
    And the gRPC response should have entry
    And the gRPC response field "entry.content[0]" should be "Updated epoch entry"

  # An agent compacts its context window (here: four messages replaced by one summary) and flushes
  # the result with SyncEntries. The compacted context must be stored in a new epoch, be readable
  # back through ListEntries with each epoch_filter, and the pre-compaction epoch must stay intact.
  Scenario: Compacted context via gRPC is persisted in a new epoch and readable after sync
    Given I am authenticated as agent with API key "test-agent-key"
    # Epoch 1: the uncompacted context. The conversation already exists, so this is not an increment.
    When I send gRPC request "EntriesService/SyncEntries" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      channel: CONTEXT
      content_type: "test.v1"
      content { string_value: "Question 1" }
      content { string_value: "Answer 1" }
      content { string_value: "Question 2" }
      content { string_value: "Answer 2" }
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "epoch" should be "1"
    And the gRPC response field "epochIncremented" should be false
    # Compaction: the four messages are replaced by a single summary, so the context diverges.
    When I send gRPC request "EntriesService/SyncEntries" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      channel: CONTEXT
      content_type: "test.v1"
      content { string_value: "Summary of questions 1 and 2" }
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "epoch" should be "2"
    And the gRPC response field "noOp" should be false
    And the gRPC response field "epochIncremented" should be true
    And the gRPC response field "entry.content[0]" should be "Summary of questions 1 and 2"
    # The agent keeps going: the summary is a prefix of the new context, so only the delta is
    # stored, in the same epoch 2.
    When I send gRPC request "EntriesService/SyncEntries" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      channel: CONTEXT
      content_type: "test.v1"
      content { string_value: "Summary of questions 1 and 2" }
      content { string_value: "Question 3" }
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "epoch" should be "2"
    And the gRPC response field "epochIncremented" should be false
    And the gRPC response field "entry.content[0]" should be "Question 3"
    And the gRPC response field "entry.content" should have size 1
    # Cache note. The ListEntries calls below read through the epoch-filtered database query, not
    # the entries cache, so they verify persistence and readback only.
    # Sync reads the cache, so cache-dependent behavior (delta vs. new epoch vs. no-op) is visible
    # only in the sync responses (epoch, epochIncremented, noOp).
    # Nothing here verifies the cache's contents directly.
    # The latest epoch holds exactly the compacted context, in order, with no pre-compaction messages.
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: CONTEXT
    epoch_filter: "latest"
    page {
      page_size: 10
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response should contain 2 entries
    And the gRPC response field "entries[0].content[0]" should be "Summary of questions 1 and 2"
    And the gRPC response field "entries[0].epoch" should be "2"
    And the gRPC response field "entries[1].content[0]" should be "Question 3"
    And the gRPC response field "entries[1].epoch" should be "2"
    # Each epoch-2 entry holds exactly its own items: the summary entry and the delta entry have
    # one item each.
    And the gRPC response field "entries[0].content" should have size 1
    And the gRPC response field "entries[1].content" should have size 1
    And set "summaryEntryId" to the gRPC response field "entries[0].id"
    And set "deltaEntryId" to the gRPC response field "entries[1].id"
    # Omitting epoch_filter returns the same result: the default is the latest epoch.
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: CONTEXT
    page {
      page_size: 10
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response should contain 2 entries
    And the gRPC response field "entries[0].content[0]" should be "Summary of questions 1 and 2"
    And the gRPC response field "entries[1].content[0]" should be "Question 3"
    # The pre-compaction context is preserved unchanged as epoch 1.
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: CONTEXT
    epoch_filter: "1"
    page {
      page_size: 10
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response should contain 1 entry
    And the gRPC response field "entries[0].epoch" should be "1"
    And the gRPC response field "entries[0].content" should have size 4
    And the gRPC response field "entries[0].content[0]" should be "Question 1"
    And the gRPC response field "entries[0].content[1]" should be "Answer 1"
    And the gRPC response field "entries[0].content[2]" should be "Question 2"
    And the gRPC response field "entries[0].content[3]" should be "Answer 2"
    # epoch_filter "all" returns both epochs: one entry for epoch 1 plus two for epoch 2.
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: CONTEXT
    epoch_filter: "all"
    page {
      page_size: 10
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response should contain 3 entries
    And the gRPC response field "entries[0].epoch" should be "1"
    And the gRPC response field "entries[0].content" should have size 4
    And the gRPC response field "entries[0].content[0]" should be "Question 1"
    And the gRPC response field "entries[0].content[3]" should be "Answer 2"
    And the gRPC response field "entries[1].epoch" should be "2"
    And the gRPC response field "entries[1].content[0]" should be "Summary of questions 1 and 2"
    And the gRPC response field "entries[2].epoch" should be "2"
    And the gRPC response field "entries[2].content[0]" should be "Question 3"
    # Replaying the identical context is a no-op: sync compares it with the stored latest epoch,
    # finds nothing new, and must write nothing.
    When I send gRPC request "EntriesService/SyncEntries" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      channel: CONTEXT
      content_type: "test.v1"
      content { string_value: "Summary of questions 1 and 2" }
      content { string_value: "Question 3" }
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "epoch" should be "2"
    And the gRPC response field "noOp" should be true
    And the gRPC response field "epochIncremented" should be false
    And the gRPC response should not have entry
    # The stored state is unchanged: the same two epoch-2 entries (same ids, same content), and
    # still only three context entries across all epochs.
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: CONTEXT
    epoch_filter: "latest"
    page {
      page_size: 10
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response should contain 2 entries
    And the gRPC response field "entries[0].id" should be "${summaryEntryId}"
    And the gRPC response field "entries[0].epoch" should be "2"
    And the gRPC response field "entries[0].content" should have size 1
    And the gRPC response field "entries[0].content[0]" should be "Summary of questions 1 and 2"
    And the gRPC response field "entries[1].id" should be "${deltaEntryId}"
    And the gRPC response field "entries[1].epoch" should be "2"
    And the gRPC response field "entries[1].content" should have size 1
    And the gRPC response field "entries[1].content[0]" should be "Question 3"
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: CONTEXT
    epoch_filter: "all"
    page {
      page_size: 10
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response should contain 3 entries
    # A second compaction opens epoch 3 and leaves epochs 1 and 2 untouched, so "latest" is not
    # stale. The compacted context has two items, so a store that keeps only part of a divergent
    # context is caught.
    When I send gRPC request "EntriesService/SyncEntries" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      channel: CONTEXT
      content_type: "test.v1"
      content { string_value: "Summary of questions 1 to 3" }
      content { string_value: "Next steps" }
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "epoch" should be "3"
    And the gRPC response field "noOp" should be false
    And the gRPC response field "epochIncremented" should be true
    And the gRPC response field "entry.content" should have size 2
    And the gRPC response field "entry.content[0]" should be "Summary of questions 1 to 3"
    And the gRPC response field "entry.content[1]" should be "Next steps"
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: CONTEXT
    epoch_filter: "latest"
    page {
      page_size: 10
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response should contain 1 entry
    And the gRPC response field "entries[0].epoch" should be "3"
    And the gRPC response field "entries[0].content" should have size 2
    And the gRPC response field "entries[0].content[0]" should be "Summary of questions 1 to 3"
    And the gRPC response field "entries[0].content[1]" should be "Next steps"
    And set "epoch3EntryId" to the gRPC response field "entries[0].id"
    # Replaying the epoch-3 context is a no-op. This only holds if the state sync reads (including
    # its cache) was refreshed by the second compaction; a stale view would open epoch 4 instead.
    When I send gRPC request "EntriesService/SyncEntries" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      channel: CONTEXT
      content_type: "test.v1"
      content { string_value: "Summary of questions 1 to 3" }
      content { string_value: "Next steps" }
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "epoch" should be "3"
    And the gRPC response field "noOp" should be true
    And the gRPC response field "epochIncremented" should be false
    And the gRPC response should not have entry
    # Epoch 2 is still intact after the second compaction.
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: CONTEXT
    epoch_filter: "2"
    page {
      page_size: 10
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response should contain 2 entries
    And the gRPC response field "entries[0].id" should be "${summaryEntryId}"
    And the gRPC response field "entries[0].epoch" should be "2"
    And the gRPC response field "entries[0].content" should have size 1
    And the gRPC response field "entries[0].content[0]" should be "Summary of questions 1 and 2"
    And the gRPC response field "entries[1].id" should be "${deltaEntryId}"
    And the gRPC response field "entries[1].epoch" should be "2"
    And the gRPC response field "entries[1].content" should have size 1
    And the gRPC response field "entries[1].content[0]" should be "Question 3"
    # Epoch 1 is still intact after the second compaction.
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: CONTEXT
    epoch_filter: "1"
    page {
      page_size: 10
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response should contain 1 entry
    And the gRPC response field "entries[0].epoch" should be "1"
    And the gRPC response field "entries[0].content" should have size 4
    And the gRPC response field "entries[0].content[0]" should be "Question 1"
    And the gRPC response field "entries[0].content[1]" should be "Answer 1"
    And the gRPC response field "entries[0].content[2]" should be "Question 2"
    And the gRPC response field "entries[0].content[3]" should be "Answer 2"
    # All three epochs are preserved, oldest first: 1 + 2 + 1 entries.
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: CONTEXT
    epoch_filter: "all"
    page {
      page_size: 10
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response should contain 4 entries
    And the gRPC response field "entries[0].epoch" should be "1"
    And the gRPC response field "entries[0].content[0]" should be "Question 1"
    And the gRPC response field "entries[1].id" should be "${summaryEntryId}"
    And the gRPC response field "entries[1].epoch" should be "2"
    And the gRPC response field "entries[1].content[0]" should be "Summary of questions 1 and 2"
    And the gRPC response field "entries[2].id" should be "${deltaEntryId}"
    And the gRPC response field "entries[2].epoch" should be "2"
    And the gRPC response field "entries[2].content[0]" should be "Question 3"
    And the gRPC response field "entries[3].id" should be "${epoch3EntryId}"
    And the gRPC response field "entries[3].epoch" should be "3"
    And the gRPC response field "entries[3].content" should have size 2
    And the gRPC response field "entries[3].content[0]" should be "Summary of questions 1 to 3"
    And the gRPC response field "entries[3].content[1]" should be "Next steps"

  Scenario: Sync context entries via gRPC with empty content clears memory
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has a context entry "Memory to clear" with epoch 1 and contentType "test.v1"
    When I send gRPC request "EntriesService/SyncEntries" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      channel: CONTEXT
      content_type: "test.v1"
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "epoch" should be "2"
    And the gRPC response field "noOp" should be false
    And the gRPC response field "epochIncremented" should be true
    And the gRPC response should have entry
    And the gRPC response entry content should be empty

  Scenario: Sync context entries via gRPC with empty content is no-op when no existing memory
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I send gRPC request "EntriesService/SyncEntries" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      channel: CONTEXT
      content_type: "test.v1"
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "noOp" should be true
    And the gRPC response field "epochIncremented" should be false
    And the gRPC response should not have entry

  Scenario: Append context entry requires API key via gRPC
    Given I am authenticated as user "alice"
    And the conversation exists
    When I send gRPC request "EntriesService/AppendEntry" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      channel: CONTEXT
      user_id: "alice"
      content_type: "test.v1"
      content {
        string_value: "hi"
      }
    }
    """
    Then the gRPC response should have status "PERMISSION_DENIED"

  Scenario: Agent can append entry via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I send gRPC request "EntriesService/AppendEntry" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      user_id: "alice"
      channel: CONTEXT
      content_type: "test.v1"
      content {
        string_value: "Agent entry via gRPC"
      }
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should not be null
    And the gRPC response field "conversationId" should be "${conversationId}"
    And the gRPC response field "channel" should be "CONTEXT"
    And the gRPC response field "contentType" should be "test.v1"
    And the gRPC response text should match text proto:
    """
    id: "${response.body.id | base64_to_hex_string}"
    conversation_id: "${conversationId}"
    user_id: "alice"
    channel: CONTEXT
    content_type: "test.v1"
    content {
      string_value: "Agent entry via gRPC"
    }
    """

  Scenario: Agent can append entry with multiple content blocks via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I send gRPC request "EntriesService/AppendEntry" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      user_id: "alice"
      channel: CONTEXT
      content_type: "test.v1"
      content {
        string_value: "First part"
      }
      content {
        string_value: "Second part"
      }
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "id" should not be null
    And the gRPC response field "channel" should be "CONTEXT"
    And the gRPC response field "contentType" should be "test.v1"

  Scenario: A different client cannot read context via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has an entry "Planner memory" in channel "CONTEXT" with contentType "test.v1"
    Given I am authenticated as agent with API key "test-agent-key-b"
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    channel: CONTEXT
    page {
      page_size: 10
    }
    """
    Then the gRPC response should have status "PERMISSION_DENIED"

  Scenario: A different client cannot append context via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has an entry "Planner memory" in channel "CONTEXT" with contentType "test.v1"
    Given I am authenticated as agent with API key "test-agent-key-b"
    When I send gRPC request "EntriesService/AppendEntry" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      user_id: "alice"
      channel: CONTEXT
      content_type: "test.v1"
      content {
        string_value: "Other client memory"
      }
    }
    """
    Then the gRPC response should have status "PERMISSION_DENIED"

  Scenario: A different client cannot sync context via gRPC
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation has an entry "Planner memory" in channel "CONTEXT" with contentType "test.v1"
    Given I am authenticated as agent with API key "test-agent-key-b"
    When I send gRPC request "EntriesService/SyncEntries" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      channel: CONTEXT
      content_type: "test.v1"
      content {
        string_value: "Other client sync"
      }
    }
    """
    Then the gRPC response should have status "PERMISSION_DENIED"

  Scenario: A different client can append history via gRPC
    Given I am authenticated as agent with API key "test-agent-key-b"
    And the conversation exists
    When I send gRPC request "EntriesService/AppendEntry" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      user_id: "alice"
      channel: HISTORY
      content_type: "history"
      content {
        struct_value {
          fields {
            key: "role"
            value {
              string_value: "USER"
            }
          }
          fields {
            key: "text"
            value {
              string_value: "History from another client"
            }
          }
        }
      }
    }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "channel" should be "HISTORY"
    And the gRPC response field "contentType" should be "history"

  Scenario: gRPC history channel entries must use 'history' or 'history/*' contentType
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I send gRPC request "EntriesService/AppendEntry" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      user_id: "alice"
      channel: HISTORY
      content_type: "message"
      content {
        struct_value {
          fields {
            key: "text"
            value { string_value: "Test message" }
          }
          fields {
            key: "role"
            value { string_value: "AI" }
          }
        }
      }
    }
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"
    And the gRPC error message should contain "History channel entries must use 'history' or 'history/<subtype>' as the contentType"

  Scenario: gRPC history channel entries must have exactly 1 content object
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I send gRPC request "EntriesService/AppendEntry" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      user_id: "alice"
      channel: HISTORY
      content_type: "history"
      content {
        struct_value {
          fields {
            key: "text"
            value { string_value: "First" }
          }
          fields {
            key: "role"
            value { string_value: "AI" }
          }
        }
      }
      content {
        struct_value {
          fields {
            key: "text"
            value { string_value: "Second" }
          }
          fields {
            key: "role"
            value { string_value: "USER" }
          }
        }
      }
    }
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"
    And the gRPC error message should contain "History channel entries must contain exactly 1 content object"

  Scenario: gRPC history channel entries must have valid role
    Given I am authenticated as agent with API key "test-agent-key"
    And the conversation exists
    When I send gRPC request "EntriesService/AppendEntry" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      user_id: "alice"
      channel: HISTORY
      content_type: "history"
      content {
        struct_value {
          fields {
            key: "text"
            value { string_value: "Test message" }
          }
          fields {
            key: "role"
            value { string_value: "INVALID" }
          }
        }
      }
    }
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"
    And the gRPC error message should contain "History channel content must have a 'role' field with value 'USER' or 'AI'"
