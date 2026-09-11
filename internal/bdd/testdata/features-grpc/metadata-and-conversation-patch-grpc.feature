Feature: Metadata filter and conversation patch via gRPC
  As a client of the memory service
  I want to filter conversations by metadata and patch conversation state atomically with entry operations
  So that I have full metadata and patch parity with the REST API

  Background:
    Given I am authenticated as user "alice"
    And I am authenticated as agent with API key "test-agent-key"

  # ── metadata filter on ListConversations ─────────────────────────────────

  Scenario: List conversations filtered by metadata key-value via gRPC
    When I send gRPC request "ConversationsService/CreateConversation" with body:
    """
    title: "Waiting Task"
    metadata {
      fields {
        key: "status"
        value { string_value: "waiting" }
      }
    }
    """
    Then the gRPC response should not have an error
    And set "waitingId" to the gRPC response field "id"
    When I send gRPC request "ConversationsService/CreateConversation" with body:
    """
    title: "Running Task"
    metadata {
      fields {
        key: "status"
        value { string_value: "running" }
      }
    }
    """
    Then the gRPC response should not have an error
    When I send gRPC request "ConversationsService/ListConversations" with body:
    """
    mode: ALL
    metadata_filter_key: "status"
    metadata_filter_value: "waiting"
    page { page_size: 20 }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "conversations" should have size 1
    And the gRPC response field "conversations[0].id" should be "${waitingId}"

  Scenario: List conversations with metadata filter returns empty when no match via gRPC
    When I send gRPC request "ConversationsService/CreateConversation" with body:
    """
    title: "No Match Conversation"
    metadata {
      fields {
        key: "status"
        value { string_value: "running" }
      }
    }
    """
    Then the gRPC response should not have an error
    When I send gRPC request "ConversationsService/ListConversations" with body:
    """
    mode: ALL
    metadata_filter_key: "status"
    metadata_filter_value: "nonexistent"
    page { page_size: 20 }
    """
    Then the gRPC response should not have an error
    And the gRPC response should contain 0 entries

  Scenario: ListConversations returns INVALID_ARGUMENT when value set without key via gRPC
    When I send gRPC request "ConversationsService/ListConversations" with body:
    """
    mode: ALL
    metadata_filter_value: "orphan"
    page { page_size: 20 }
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"

  Scenario: ListConversations returns INVALID_ARGUMENT for invalid metadata filter key via gRPC
    When I send gRPC request "ConversationsService/ListConversations" with body:
    """
    mode: ALL
    metadata_filter_key: "bad key!"
    metadata_filter_value: "val"
    page { page_size: 20 }
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"

  Scenario: ListConversations returns INVALID_ARGUMENT for dotted metadata filter key via gRPC
    When I send gRPC request "ConversationsService/ListConversations" with body:
    """
    mode: ALL
    metadata_filter_key: "key.nested"
    metadata_filter_value: "val"
    page { page_size: 20 }
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"

  Scenario: ListConversations returns INVALID_ARGUMENT when key set without value via gRPC
    When I send gRPC request "ConversationsService/ListConversations" with body:
    """
    mode: ALL
    metadata_filter_key: "status"
    page { page_size: 20 }
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"

  Scenario: ListConversations returns INVALID_ARGUMENT for empty key with present value via gRPC
    When I send gRPC request "ConversationsService/ListConversations" with body:
    """
    mode: ALL
    metadata_filter_key: ""
    metadata_filter_value: "waiting"
    page { page_size: 20 }
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"

  Scenario: ListConversations with repeated metadata_filters using EQUAL and NOT_EQUAL
    When I send gRPC request "ConversationsService/CreateConversation" with body:
    """
    title: "Waiting Worker 1"
    metadata {
      fields {
        key: "status"
        value { string_value: "waiting" }
      }
      fields {
        key: "agent-id"
        value { string_value: "worker-1" }
      }
    }
    """
    Then the gRPC response should not have an error
    And set "matchGrpcId" to the gRPC response field "id"
    When I send gRPC request "ConversationsService/CreateConversation" with body:
    """
    title: "Waiting Worker 2"
    metadata {
      fields {
        key: "status"
        value { string_value: "waiting" }
      }
      fields {
        key: "agent-id"
        value { string_value: "worker-2" }
      }
    }
    """
    Then the gRPC response should not have an error
    When I send gRPC request "ConversationsService/ListConversations" with body:
    """
    mode: ALL
    metadata_filters {
      key: "status"
      comparison: CONVERSATION_METADATA_COMPARISON_EQUAL
      value: "waiting"
    }
    metadata_filters {
      key: "agent-id"
      comparison: CONVERSATION_METADATA_COMPARISON_NOT_EQUAL
      value: "worker-2"
    }
    page { page_size: 20 }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "conversations" should have size 1
    And the gRPC response field "conversations[0].id" should be "${matchGrpcId}"

  Scenario: ListConversations rejects more than five metadata filters via gRPC
    When I send gRPC request "ConversationsService/ListConversations" with body:
    """
    metadata_filters { key: "a" comparison: CONVERSATION_METADATA_COMPARISON_EQUAL value: "1" }
    metadata_filters { key: "b" comparison: CONVERSATION_METADATA_COMPARISON_EQUAL value: "2" }
    metadata_filters { key: "c" comparison: CONVERSATION_METADATA_COMPARISON_EQUAL value: "3" }
    metadata_filters { key: "d" comparison: CONVERSATION_METADATA_COMPARISON_EQUAL value: "4" }
    metadata_filters { key: "e" comparison: CONVERSATION_METADATA_COMPARISON_EQUAL value: "5" }
    metadata_filters { key: "f" comparison: CONVERSATION_METADATA_COMPARISON_EQUAL value: "6" }
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"

  Scenario: ListConversations rejects mixing legacy and repeated metadata filters via gRPC
    When I send gRPC request "ConversationsService/ListConversations" with body:
    """
    metadata_filter_key: "status"
    metadata_filter_value: "waiting"
    metadata_filters { key: "agent" comparison: CONVERSATION_METADATA_COMPARISON_EQUAL value: "worker" }
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"

  Scenario: ListConversations rejects unspecified comparison in metadata filter via gRPC
    When I send gRPC request "ConversationsService/ListConversations" with body:
    """
    metadata_filters { key: "status" comparison: CONVERSATION_METADATA_COMPARISON_UNSPECIFIED value: "waiting" }
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"

  Scenario: ListConversations filters by empty string value via gRPC
    When I send gRPC request "ConversationsService/CreateConversation" with body:
    """
    title: "Empty Status gRPC"
    metadata {
      fields {
        key: "status"
        value { string_value: "" }
      }
    }
    """
    Then the gRPC response should not have an error
    And set "emptyStatusGrpcId" to the gRPC response field "id"
    When I send gRPC request "ConversationsService/CreateConversation" with body:
    """
    title: "Other Status gRPC"
    metadata {
      fields {
        key: "other"
        value { string_value: "value" }
      }
    }
    """
    Then the gRPC response should not have an error
    When I send gRPC request "ConversationsService/ListConversations" with body:
    """
    mode: ALL
    metadata_filter_key: "status"
    metadata_filter_value: ""
    page { page_size: 20 }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "conversations" should have size 1
    And the gRPC response field "conversations[0].id" should be "${emptyStatusGrpcId}"

  Scenario: ListConversations matches string "1" but not numeric 1 via gRPC
    When I send gRPC request "ConversationsService/CreateConversation" with body:
    """
    title: "String One gRPC"
    metadata {
      fields {
        key: "count"
        value { string_value: "1" }
      }
    }
    """
    Then the gRPC response should not have an error
    And set "stringOneGrpcId" to the gRPC response field "id"
    When I send gRPC request "ConversationsService/CreateConversation" with body:
    """
    title: "Numeric One gRPC"
    metadata {
      fields {
        key: "count"
        value { number_value: 1 }
      }
    }
    """
    Then the gRPC response should not have an error
    When I send gRPC request "ConversationsService/CreateConversation" with body:
    """
    title: "Boolean True gRPC"
    metadata {
      fields {
        key: "count"
        value { bool_value: true }
      }
    }
    """
    Then the gRPC response should not have an error
    When I send gRPC request "ConversationsService/ListConversations" with body:
    """
    mode: ALL
    metadata_filter_key: "count"
    metadata_filter_value: "1"
    page { page_size: 20 }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "conversations" should have size 1
    And the gRPC response field "conversations[0].id" should be "${stringOneGrpcId}"

  # ── UpdateConversation with all three fields ─────────────────────────────

  Scenario: UpdateConversation with archived, title, and metadata in same request applies all three via gRPC
    Given I have a conversation with title "gRPC Triple Patch"
    When I send gRPC request "ConversationsService/UpdateConversation" with body:
    """
    conversation_id: "${conversationId}"
    archived: true
    title: "gRPC Archived And Renamed"
    metadata {
      fields {
        key: "status"
        value { string_value: "done" }
      }
    }
    """
    Then the gRPC response should not have an error
    When I send gRPC request "ConversationsService/ListConversations" with body:
    """
    mode: ALL
    archived: ARCHIVE_FILTER_ONLY
    page { page_size: 20 }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "conversations" should have size 1
    And the gRPC response field "conversations[0].id" should be "${conversationId}"
    And the gRPC response field "conversations[0].title" should be "gRPC Archived And Renamed"
    And the gRPC response field "conversations[0].metadata.status" should be "done"

  # ── conversation_patch on AppendEntry ────────────────────────────────────

  Scenario: conversation_patch sets metadata atomically with AppendEntry via gRPC
    Given I have a conversation with title "gRPC Patch Append"
    When I send gRPC request "EntriesService/AppendEntry" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      channel: HISTORY
      content_type: "history"
      content {
        struct_value {
          fields {
            key: "role"
            value { string_value: "USER" }
          }
          fields {
            key: "text"
            value { string_value: "hello" }
          }
        }
      }
    }
    conversation_patch {
      metadata {
        fields {
          key: "status"
          value { string_value: "running" }
        }
      }
    }
    """
    Then the gRPC response should not have an error
    When I send gRPC request "ConversationsService/GetConversation" with body:
    """
    conversation_id: "${conversationId}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "metadata.status" should be "running"

  Scenario: conversation_patch updates title atomically with AppendEntry via gRPC
    Given I have a conversation with title "Old Title"
    When I send gRPC request "EntriesService/AppendEntry" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      channel: HISTORY
      content_type: "history"
      content {
        struct_value {
          fields {
            key: "role"
            value { string_value: "USER" }
          }
          fields {
            key: "text"
            value { string_value: "renaming" }
          }
        }
      }
    }
    conversation_patch {
      title: "New Title via gRPC Patch"
    }
    """
    Then the gRPC response should not have an error
    When I send gRPC request "ConversationsService/GetConversation" with body:
    """
    conversation_id: "${conversationId}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "title" should be "New Title via gRPC Patch"

  Scenario: conversation_patch with archived:true archives conversation atomically via gRPC AppendEntry
    Given I have a conversation with title "Archive Patch gRPC"
    When I send gRPC request "EntriesService/AppendEntry" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      channel: HISTORY
      content_type: "history"
      content {
        struct_value {
          fields {
            key: "role"
            value { string_value: "USER" }
          }
          fields {
            key: "text"
            value { string_value: "archiving" }
          }
        }
      }
    }
    conversation_patch {
      archived: true
    }
    """
    Then the gRPC response should not have an error
    When I send gRPC request "ConversationsService/ListConversations" with body:
    """
    mode: ALL
    archived: ARCHIVE_FILTER_ONLY
    page { page_size: 20 }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "conversations" should have size 1
    And the gRPC response field "conversations[0].id" should be "${conversationId}"

  Scenario: conversation_patch with archived:false unarchives conversation atomically via gRPC AppendEntry
    Given I have a conversation with title "Unarchive Patch gRPC"
    When I send gRPC request "ConversationsService/UpdateConversation" with body:
    """
    conversation_id: "${conversationId}"
    archived: true
    """
    Then the gRPC response should not have an error
    When I send gRPC request "EntriesService/AppendEntry" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      channel: HISTORY
      content_type: "history"
      content {
        struct_value {
          fields {
            key: "role"
            value { string_value: "USER" }
          }
          fields {
            key: "text"
            value { string_value: "unarchiving" }
          }
        }
      }
    }
    conversation_patch {
      archived: false
    }
    """
    Then the gRPC response should not have an error
    When I send gRPC request "ConversationsService/ListConversations" with body:
    """
    mode: ALL
    page { page_size: 20 }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "conversations" should have size 1
    And the gRPC response field "conversations[0].id" should be "${conversationId}"

  Scenario: conversation_patch with archived and title combined applies both via gRPC
    Given I have a conversation with title "Combined Patch gRPC"
    When I send gRPC request "EntriesService/AppendEntry" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      channel: HISTORY
      content_type: "history"
      content {
        struct_value {
          fields {
            key: "role"
            value { string_value: "USER" }
          }
          fields {
            key: "text"
            value { string_value: "combined" }
          }
        }
      }
    }
    conversation_patch {
      archived: true
      title: "Combined Title gRPC"
    }
    """
    Then the gRPC response should not have an error
    When I send gRPC request "ConversationsService/ListConversations" with body:
    """
    mode: ALL
    archived: ARCHIVE_FILTER_ONLY
    page { page_size: 20 }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "conversations" should have size 1
    And the gRPC response field "conversations[0].id" should be "${conversationId}"
    And the gRPC response field "conversations[0].title" should be "Combined Title gRPC"

  # ── conversation_patch on AppendEntries (batch) ─────────────────────────

  Scenario: conversation_patch sets metadata atomically with batch AppendEntries via gRPC
    Given I have a conversation with title "gRPC Batch Patch"
    When I send gRPC request "EntriesService/AppendEntries" with body:
    """
    conversation_id: "${conversationId}"
    entries {
      channel: HISTORY
      content_type: "history"
      content {
        struct_value {
          fields {
            key: "role"
            value { string_value: "USER" }
          }
          fields {
            key: "text"
            value { string_value: "first message" }
          }
        }
      }
    }
    entries {
      channel: HISTORY
      content_type: "history"
      content {
        struct_value {
          fields {
            key: "role"
            value { string_value: "AI" }
          }
          fields {
            key: "text"
            value { string_value: "first reply" }
          }
        }
      }
    }
    conversation_patch {
      metadata {
        fields {
          key: "status"
          value { string_value: "completed" }
        }
      }
      title: "Patched Batch Title"
    }
    """
    Then the gRPC response should not have an error
    When I send gRPC request "ConversationsService/GetConversation" with body:
    """
    conversation_id: "${conversationId}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "metadata.status" should be "completed"
    And the gRPC response field "title" should be "Patched Batch Title"

  # ── conversation_patch on SyncEntries ────────────────────────────────────

  Scenario: conversation_patch sets metadata atomically with SyncEntries via gRPC
    Given I have a conversation with title "gRPC Patch Sync"
    When I send gRPC request "EntriesService/SyncEntries" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      channel: CONTEXT
      content_type: "history/lc4j"
      content {
        struct_value {
          fields {
            key: "type"
            value { string_value: "text" }
          }
          fields {
            key: "text"
            value { string_value: "synced context" }
          }
        }
      }
    }
    conversation_patch {
      metadata {
        fields {
          key: "status"
          value { string_value: "waiting" }
        }
      }
    }
    """
    Then the gRPC response should not have an error
    When I send gRPC request "ConversationsService/GetConversation" with body:
    """
    conversation_id: "${conversationId}"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "metadata.status" should be "waiting"

  # ── admin metadata filter on ListConversations ──────────────────────────

  Scenario: Admin list returns INVALID_ARGUMENT when key set without value via gRPC
    Given I am authenticated as admin user "alice"
    When I send gRPC request "AdminConversationsService/ListConversations" with body:
    """
    owner_user_id: "alice"
    mode: ALL
    metadata_filter_key: "status"
    page { page_size: 20 }
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"

  Scenario: Admin list returns INVALID_ARGUMENT when value set without key via gRPC
    Given I am authenticated as admin user "alice"
    When I send gRPC request "AdminConversationsService/ListConversations" with body:
    """
    owner_user_id: "alice"
    mode: ALL
    metadata_filter_value: "orphan"
    page { page_size: 20 }
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"

  Scenario: Admin list returns INVALID_ARGUMENT for empty key with present value via gRPC
    Given I am authenticated as admin user "alice"
    When I send gRPC request "AdminConversationsService/ListConversations" with body:
    """
    owner_user_id: "alice"
    mode: ALL
    metadata_filter_key: ""
    metadata_filter_value: "waiting"
    page { page_size: 20 }
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"

  Scenario: Admin list filters by empty string value via gRPC
    Given I am authenticated as user "bob"
    When I send gRPC request "ConversationsService/CreateConversation" with body:
    """
    title: "Admin Empty Status gRPC"
    metadata {
      fields {
        key: "status"
        value { string_value: "" }
      }
    }
    """
    Then the gRPC response should not have an error
    And set "adminEmptyStatusGrpcId" to the gRPC response field "id"
    When I send gRPC request "ConversationsService/CreateConversation" with body:
    """
    title: "Admin Other Status gRPC"
    metadata {
      fields {
        key: "other"
        value { string_value: "value" }
      }
    }
    """
    Then the gRPC response should not have an error
    Given I am authenticated as admin user "alice"
    When I send gRPC request "AdminConversationsService/ListConversations" with body:
    """
    owner_user_id: "bob"
    mode: ALL
    metadata_filter_key: "status"
    metadata_filter_value: ""
    page { page_size: 20 }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "conversations" should have size 1
    And the gRPC response field "conversations[0].id" should be "${adminEmptyStatusGrpcId}"

  Scenario: Admin list matches string "1" but not numeric 1 via gRPC
    Given I am authenticated as user "bob"
    When I send gRPC request "ConversationsService/CreateConversation" with body:
    """
    title: "Admin String One gRPC"
    metadata {
      fields {
        key: "count"
        value { string_value: "1" }
      }
    }
    """
    Then the gRPC response should not have an error
    And set "adminStringOneGrpcId" to the gRPC response field "id"
    When I send gRPC request "ConversationsService/CreateConversation" with body:
    """
    title: "Admin Numeric One gRPC"
    metadata {
      fields {
        key: "count"
        value { number_value: 1 }
      }
    }
    """
    Then the gRPC response should not have an error
    When I send gRPC request "ConversationsService/CreateConversation" with body:
    """
    title: "Admin Boolean True gRPC"
    metadata {
      fields {
        key: "count"
        value { bool_value: true }
      }
    }
    """
    Then the gRPC response should not have an error
    Given I am authenticated as admin user "alice"
    When I send gRPC request "AdminConversationsService/ListConversations" with body:
    """
    owner_user_id: "bob"
    mode: ALL
    metadata_filter_key: "count"
    metadata_filter_value: "1"
    page { page_size: 20 }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "conversations" should have size 1
    And the gRPC response field "conversations[0].id" should be "${adminStringOneGrpcId}"

  Scenario: Admin list returns INVALID_ARGUMENT for invalid metadata filter key via gRPC
    Given I am authenticated as admin user "alice"
    When I send gRPC request "AdminConversationsService/ListConversations" with body:
    """
    owner_user_id: "alice"
    mode: ALL
    metadata_filter_key: "bad key!"
    metadata_filter_value: "val"
    page { page_size: 20 }
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"

  Scenario: Admin list returns INVALID_ARGUMENT for dotted metadata filter key via gRPC
    Given I am authenticated as admin user "alice"
    When I send gRPC request "AdminConversationsService/ListConversations" with body:
    """
    owner_user_id: "alice"
    mode: ALL
    metadata_filter_key: "key.nested"
    metadata_filter_value: "val"
    page { page_size: 20 }
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"

  Scenario: Admin list with repeated metadata_filters using EQUAL and NOT_EQUAL
    Given I am authenticated as user "bob"
    When I send gRPC request "ConversationsService/CreateConversation" with body:
    """
    title: "Admin Filter Match"
    metadata {
      fields {
        key: "status"
        value { string_value: "active" }
      }
      fields {
        key: "tier"
        value { string_value: "gold" }
      }
    }
    """
    Then the gRPC response should not have an error
    And set "adminMatchGrpcId" to the gRPC response field "id"
    When I send gRPC request "ConversationsService/CreateConversation" with body:
    """
    title: "Admin Filter Non-Match"
    metadata {
      fields {
        key: "status"
        value { string_value: "active" }
      }
      fields {
        key: "tier"
        value { string_value: "silver" }
      }
    }
    """
    Then the gRPC response should not have an error
    Given I am authenticated as admin user "alice"
    When I send gRPC request "AdminConversationsService/ListConversations" with body:
    """
    owner_user_id: "bob"
    mode: ALL
    metadata_filters {
      key: "status"
      comparison: CONVERSATION_METADATA_COMPARISON_EQUAL
      value: "active"
    }
    metadata_filters {
      key: "tier"
      comparison: CONVERSATION_METADATA_COMPARISON_NOT_EQUAL
      value: "silver"
    }
    page { page_size: 20 }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "conversations" should have size 1
    And the gRPC response field "conversations[0].id" should be "${adminMatchGrpcId}"

  Scenario: Admin list rejects more than five metadata filters via gRPC
    Given I am authenticated as admin user "alice"
    When I send gRPC request "AdminConversationsService/ListConversations" with body:
    """
    metadata_filters { key: "a" comparison: CONVERSATION_METADATA_COMPARISON_EQUAL value: "1" }
    metadata_filters { key: "b" comparison: CONVERSATION_METADATA_COMPARISON_EQUAL value: "2" }
    metadata_filters { key: "c" comparison: CONVERSATION_METADATA_COMPARISON_EQUAL value: "3" }
    metadata_filters { key: "d" comparison: CONVERSATION_METADATA_COMPARISON_EQUAL value: "4" }
    metadata_filters { key: "e" comparison: CONVERSATION_METADATA_COMPARISON_EQUAL value: "5" }
    metadata_filters { key: "f" comparison: CONVERSATION_METADATA_COMPARISON_EQUAL value: "6" }
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"

  Scenario: Admin list rejects mixing legacy and repeated metadata filters via gRPC
    Given I am authenticated as admin user "alice"
    When I send gRPC request "AdminConversationsService/ListConversations" with body:
    """
    metadata_filter_key: "status"
    metadata_filter_value: "waiting"
    metadata_filters { key: "tier" comparison: CONVERSATION_METADATA_COMPARISON_EQUAL value: "gold" }
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"

  Scenario: Admin list rejects unspecified comparison in metadata filter via gRPC
    Given I am authenticated as admin user "alice"
    When I send gRPC request "AdminConversationsService/ListConversations" with body:
    """
    metadata_filters { key: "status" comparison: CONVERSATION_METADATA_COMPARISON_UNSPECIFIED value: "waiting" }
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"

  Scenario: UpdateConversation with empty metadata Struct is a valid no-op via gRPC
    Given I have a conversation with title "Empty Struct Metadata No-Op"
    When I send gRPC request "ConversationsService/UpdateConversation" with body:
    """
    conversation_id: "${conversationId}"
    metadata {
      fields {
        key: "keep"
        value { string_value: "yes" }
      }
    }
    """
    Then the gRPC response should not have an error
    When I send gRPC request "ConversationsService/UpdateConversation" with body:
    """
    conversation_id: "${conversationId}"
    metadata {}
    """
    Then the gRPC response should not have an error
    And the gRPC response field "metadata.keep" should be "yes"

  Scenario: Admin UpdateConversation with empty metadata Struct is a valid no-op via gRPC
    Given I am authenticated as user "bob"
    And I have a conversation with title "Admin Empty Struct Metadata No-Op"
    And set "adminEmptyStructId" to "${conversationId}"
    Given I am authenticated as admin user "alice"
    When I send gRPC request "AdminConversationsService/UpdateConversation" with body:
    """
    conversation_id: "${adminEmptyStructId}"
    metadata {
      fields {
        key: "keep"
        value { string_value: "yes" }
      }
    }
    justification: "set initial metadata"
    """
    Then the gRPC response should not have an error
    When I send gRPC request "AdminConversationsService/UpdateConversation" with body:
    """
    conversation_id: "${adminEmptyStructId}"
    metadata {}
    justification: "empty struct no-op"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "metadata.keep" should be "yes"

  Scenario: UpdateConversation with NaN metadata value returns INVALID_ARGUMENT via gRPC
    Given I have a conversation with title "NaN Metadata Encoding"
    When I send gRPC request "ConversationsService/UpdateConversation" with body:
    """
    conversation_id: "${conversationId}"
    metadata {
      fields {
        key: "bad"
        value { number_value: nan }
      }
    }
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"

  Scenario: Admin UpdateConversation with NaN metadata value returns INVALID_ARGUMENT via gRPC
    Given I am authenticated as user "bob"
    And I have a conversation with title "Admin NaN Metadata Encoding"
    And set "adminNaNMetadataId" to "${conversationId}"
    Given I am authenticated as admin user "alice"
    When I send gRPC request "AdminConversationsService/UpdateConversation" with body:
    """
    conversation_id: "${adminNaNMetadataId}"
    metadata {
      fields {
        key: "bad"
        value { number_value: nan }
      }
    }
    justification: "nan metadata validation"
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"

  Scenario: AppendEntry conversation_patch with title over 500 characters is rejected before entry write
    Given I have a conversation with title "gRPC Title Validation"
    When I send gRPC request "EntriesService/AppendEntry" with body:
    """
    conversation_id: "${conversationId}"
    entry {
      channel: HISTORY
      content_type: "history"
      content {
        struct_value {
          fields {
            key: "role"
            value { string_value: "USER" }
          }
          fields {
            key: "text"
            value { string_value: "Hello" }
          }
        }
      }
    }
    conversation_patch {
      title: "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
    }
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"
    When I send gRPC request "EntriesService/ListEntries" with body:
    """
    conversation_id: "${conversationId}"
    page { page_size: 20 }
    """
    Then the gRPC response should not have an error
    And the gRPC response field "entries" should have size 0

  Scenario: AdminConversationsService UpdateConversation with only conversation_id and justification is rejected
    Given I have a conversation with title "Admin Update Validation"
    Given I am authenticated as admin user "alice"
    When I send gRPC request "AdminConversationsService/UpdateConversation" with body:
    """
    conversation_id: "${conversationId}"
    justification: "testing validation"
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"



  # ── Admin PATCH validation order prevents partial mutations ──────────────

  Scenario: AdminConversationsService UpdateConversation with archived=true and title exceeding max length returns InvalidArgument and leaves conversation unarchived
    Given I have a conversation with title "gRPC Archive Long Title Test"
    And set "grpcArchiveLongTitleId" to "${conversationId}"
    Given I am authenticated as admin user "alice"
    When I send gRPC request "AdminConversationsService/UpdateConversation" with body:
    """
    conversation_id: "${grpcArchiveLongTitleId}"
    archived: true
    title: "This title is way too long and exceeds the maximum allowed length of 500 characters. Lorem ipsum dolor sit amet, consectetur adipiscing elit. Sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat. Duis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore eu fugiat nulla pariatur. Excepteur sint occaecat cupidatat non proident, sunt in culpa qui officia deserunt mollit anim id est laborum. Sed ut perspiciatis unde omnis iste natus error."
    justification: "gRPC archive with long title test"
    """
    Then the gRPC response should have status "INVALID_ARGUMENT"
    And the gRPC error message should contain "title exceeds maximum length"
    When I send gRPC request "AdminConversationsService/GetConversation" with body:
    """
    conversation_id: "${grpcArchiveLongTitleId}"
    justification: "verify unarchived"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "archived" should be "false"
