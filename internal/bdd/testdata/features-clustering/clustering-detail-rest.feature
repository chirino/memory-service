Feature: Knowledge Cluster Detail REST API
  As an admin
  I want to get detailed information about a specific cluster
  So that the skill extractor can consume cluster data with representative texts

  Background:
    Given I am authenticated as user "alice"

  Scenario: Get cluster detail with members and representative texts
    # Create conversations with indexed content
    Given I have a conversation with title "Python Migration"
    And set "conv1" to "${conversationId}"
    And the conversation has an entry "Flask to FastAPI migration"
    When I list entries for the conversation
    And set "entry1" to the json response field "data[0].id"

    Given I have a conversation with title "Python Async"
    And set "conv2" to "${conversationId}"
    And the conversation has an entry "asyncio patterns in Python"
    When I list entries for the conversation
    And set "entry2" to the json response field "data[0].id"

    Given I have a conversation with title "Python Testing"
    And set "conv3" to "${conversationId}"
    And the conversation has an entry "pytest fixtures and mocking"
    When I list entries for the conversation
    And set "entry3" to the json response field "data[0].id"

    # Index entries with semantic content
    Given I am authenticated as admin user "alice"
    When I call POST "/v1/conversations/index" with body:
    """
    [
      {
        "conversationId": "${conv1}",
        "entryId": "${entry1}",
        "indexedContent": "migrate Flask app to FastAPI SQLAlchemy models async routes Python ORM patterns web framework migration"
      },
      {
        "conversationId": "${conv2}",
        "entryId": "${entry2}",
        "indexedContent": "Python asyncio patterns await async coroutines event loop concurrent programming FastAPI async routes"
      },
      {
        "conversationId": "${conv3}",
        "entryId": "${entry3}",
        "indexedContent": "Python pytest fixtures mocking unit testing integration tests conftest parametrize test automation"
      }
    ]
    """
    Then the response status should be 200

    # Wait for embeddings then trigger clustering
    Given I wait for embeddings to be generated
    When I call POST "/admin/v1/knowledge/trigger" with body:
    """
    {}
    """
    Then the response status should be 200
    And the response body "users_processed" should be "1"

    # List clusters and capture first cluster ID
    When I call GET "/admin/v1/knowledge/clusters"
    Then the response status should be 200
    And the response body should have field "clusters" that is not null
    And set "clusterId" to the json response field "clusters[0].id"

    # Get cluster detail
    When I call GET "/admin/v1/knowledge/clusters/${clusterId}"
    Then the response status should be 200
    And the response body field "id" should not be null
    And the response body field "label" should not be null
    And the response body field "keywords" should not be null
    And the response body field "members" should not be null
    And the response body field "representative_texts" should not be null
    And the response body field "user_id" should be "alice"

  Scenario: Get cluster detail with custom representative count
    # Reuse clusters from the database (previous scenario may have cleaned)
    Given I am authenticated as admin user "alice"
    When I call GET "/admin/v1/knowledge/clusters"
    Then the response status should be 200
    And set "clusterId" to the json response field "clusters[0].id"

    When I call GET "/admin/v1/knowledge/clusters/${clusterId}?representative_count=2"
    Then the response status should be 200
    And the response body field "members" should not be null
    And the response body field "representative_texts" should not be null

  Scenario: Get cluster detail returns 404 for unknown ID
    Given I am authenticated as admin user "alice"
    When I call GET "/admin/v1/knowledge/clusters/00000000-0000-0000-0000-000000000000"
    Then the response status should be 404

  Scenario: Get cluster detail returns 400 for invalid ID
    Given I am authenticated as admin user "alice"
    When I call GET "/admin/v1/knowledge/clusters/not-a-uuid"
    Then the response status should be 400

  Scenario: Cluster detail endpoint requires admin role
    Given I am authenticated as user "bob"
    When I call GET "/admin/v1/knowledge/clusters/00000000-0000-0000-0000-000000000000"
    Then the response status should be 403
