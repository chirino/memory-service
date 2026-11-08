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
