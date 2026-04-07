Feature: MCP server modes with SQLite
  As a coding agent
  I want MCP to work in both embedded and remote modes
  So that I can persist session notes in either deployment model

  Scenario: save_session_notes persists data through mcp embedded
    Given `memory-service mcp embedded` is running with sqlite database "embedded.db"
    When I call the MCP tool "save_session_notes" with arguments:
    """
    {
      "title": "Embedded MCP session",
      "notes": "Saved through the embedded MCP server.",
      "tags": "embedded,mcp"
    }
    """
    Then the MCP tool call should succeed
    And the MCP tool response should contain "Session notes saved."
    And the sqlite database "embedded.db" should contain 1 conversation
    And the sqlite database "embedded.db" should contain an entry with text "Saved through the embedded MCP server."

  Scenario: save_session_notes persists data through mcp remote
    Given `memory-service mcp remote` is running against the scenario server with API key "test-key" and bearer token "alice"
    When I call the MCP tool "save_session_notes" with arguments:
    """
    {
      "title": "Remote MCP session",
      "notes": "Saved through the remote MCP bridge.",
      "tags": "remote,mcp"
    }
    """
    Then the MCP tool call should succeed
    And the MCP tool response should contain "Session notes saved."
    And the sqlite database "remote.db" should contain 1 conversation
    And the sqlite database "remote.db" should contain an entry with text "Saved through the remote MCP bridge."

  Scenario: mcp remote returns an auth failure when no bearer token is configured
    Given `memory-service mcp remote` is running against the scenario server with API key "test-key"
    When I call the MCP tool "list_sessions" with arguments:
    """
    {
      "limit": 10
    }
    """
    And the MCP tool response should contain "Failed to list sessions"
