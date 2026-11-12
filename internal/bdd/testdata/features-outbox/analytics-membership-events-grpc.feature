@analytics_memberships
Feature: Admin membership snapshots and filtering over gRPC
  Background:
    Given I am authenticated as user "alice"
    And I have a conversation with title "gRPC membership analytics"

  Scenario: gRPC advertises complete membership snapshots
    When I send gRPC request "SystemService/GetCapabilities" with body:
    """
    {}
    """
    Then the gRPC response should not have an error
    And the gRPC response field "features.analyticsMembershipSnapshots" should be true

  Scenario: Full current snapshots include owners and continue with live grants
    Given "alice" is connected to the admin gRPC event stream with query "kinds=membership&detail=full&initial_state=current"
    Then "alice" should receive a gRPC event with kind "membership" and event "snapshot" where data "userId" is "alice"
    And the gRPC event data "accessLevel" should be "owner"
    And the gRPC event data should contain "conversationGroupId"
    And the gRPC event data should contain "createdAt"
    When I send gRPC request "ConversationMembershipsService/ShareConversation" with body:
    """
    conversation_id: "${conversationId}"
    user_id: "bob"
    access_level: READER
    """
    Then the gRPC response should not have an error
    And "alice" should receive a gRPC event with kind "membership" and event "created" where data "userId" is "bob"
    And the gRPC event data "accessLevel" should be "reader"
    And the gRPC event cursor should be saved as "grantCursor"
    Given "alice" is connected to the admin gRPC event stream with query "kinds=membership&detail=full&after=start"
    Then "alice" should receive a gRPC event with kind "membership" and event "created" where data "userId" is "bob"
    And the gRPC event data "accessLevel" should be "reader"

  Scenario: A full membership subscription cannot use conversation IDs
    Then the admin gRPC event stream with query "kinds=membership&detail=full&conversation_ids=${conversationId}" should fail with "INVALID_ARGUMENT"

  Scenario: Omitted kinds and conversation IDs exclude memberships from every phase
    Given "alice" is connected to the admin gRPC event stream with query "detail=full&initial_state=current&conversation_ids=${conversationId}"
    Then "alice" should receive a gRPC event with kind "conversation" and event "snapshot"
    And "alice" should not receive a gRPC event with kind "membership" and event "snapshot" within 1 seconds
    When I share the conversation with user "bob" and access level "reader"
    Then "alice" should not receive a gRPC event with kind "membership" and event "created" within 1 seconds

  Scenario: gRPC role updates and revokes preserve a later grant during replay
    Given "alice" is connected to the admin gRPC event stream with query "kinds=membership&detail=full"
    When I send gRPC request "ConversationMembershipsService/ShareConversation" with body:
    """
    conversation_id: "${conversationId}"
    user_id: "bob"
    access_level: READER
    """
    Then the gRPC response should not have an error
    And "alice" should receive a gRPC event with kind "membership" and event "created" where data "userId" is "bob"
    When I send gRPC request "ConversationMembershipsService/UpdateMembership" with body:
    """
    conversation_id: "${conversationId}"
    member_user_id: "bob"
    access_level: WRITER
    """
    Then the gRPC response should not have an error
    And "alice" should receive a gRPC event with kind "membership" and event "updated" where data "accessLevel" is "writer"
    And the gRPC event cursor should be saved as "beforeRevoke"
    When I send gRPC request "ConversationMembershipsService/DeleteMembership" with body:
    """
    conversation_id: "${conversationId}"
    member_user_id: "bob"
    """
    Then the gRPC response should not have an error
    And "alice" should receive a gRPC event with kind "membership" and event "deleted" where data "user" is "bob"
    When I send gRPC request "ConversationMembershipsService/ShareConversation" with body:
    """
    conversation_id: "${conversationId}"
    user_id: "bob"
    access_level: MANAGER
    """
    Then the gRPC response should not have an error
    And "alice" should receive a gRPC event with kind "membership" and event "created" where data "accessLevel" is "manager"
    Given "alice" is connected to the admin gRPC event stream with query "kinds=membership&detail=full&after=${beforeRevoke}"
    Then "alice" should receive a gRPC event with kind "membership" and event "deleted" where data "user" is "bob"
    And "alice" should receive a gRPC event with kind "membership" and event "created" where data "accessLevel" is "manager"

  Scenario: gRPC ownership transfer publishes both roles and refreshed conversation owner
    Given I share the conversation with user "bob" and access level "reader"
    And "alice" is connected to the admin gRPC event stream with query "kinds=membership,conversation&detail=full"
    When I send gRPC request "OwnershipTransfersService/CreateOwnershipTransfer" with body:
    """
    conversation_id: "${conversationId}"
    new_owner_user_id: "bob"
    """
    Then the gRPC response should not have an error
    And set "transferId" to the gRPC response field "id"
    Given I am authenticated as user "bob"
    When I send gRPC request "OwnershipTransfersService/AcceptOwnershipTransfer" with body:
    """
    transfer_id: "${transferId | base64_to_hex_string}"
    """
    Then the gRPC response should not have an error
    And "alice" should receive a gRPC event with kind "membership" and event "updated" where data "userId" is "alice"
    And the gRPC event data "accessLevel" should be "manager"
    And "alice" should receive a gRPC event with kind "membership" and event "updated" where data "userId" is "bob"
    And the gRPC event data "accessLevel" should be "owner"
    And "alice" should receive a gRPC event with kind "conversation" and event "updated" where data "ownerUserId" is "bob"

  Scenario: A hard-deleted group is reported on the admin gRPC stream
    Given I share the conversation with user "bob" and access level "reader"
    And "alice" is connected to the admin gRPC event stream with query "kinds=conversation,membership&detail=full"
    And I am authenticated as admin user "alice"
    And the conversation was archived 100 days ago
    When I call POST "/v1/admin/evict" with body:
    """
    {"retentionPeriod":"P90D","resourceTypes":["conversations"],"justification":"Verify membership group deletion"}
    """
    Then the response status should be 204
    And "alice" should receive a gRPC event with kind "conversation" and event "deleted" where data "conversation" is "${conversationId}"
    And the gRPC event data should contain "conversation_group"
    And the gRPC event data should contain "members"
