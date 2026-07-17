Feature: Embedded local memory-service compatibility
  Applications can embed memory-service with production security checks, SQLite, plain encryption,
  and local Unix-socket authentication.

  Scenario: Create a conversation over the local Unix socket
    When I send gRPC request "ConversationsService/CreateConversation" with body:
    """
    title: "Embedded conversation"
    """
    Then the gRPC response should not have an error
    And the gRPC response field "title" should be "Embedded conversation"
    And the gRPC response field "ownerUserId" should not be null
