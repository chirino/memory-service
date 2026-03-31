Feature: Knowledge Clustering REST API
  As an admin
  I want to cluster conversation entries by semantic similarity
  So that AI agents can query structured knowledge instead of raw data

  Background:
    Given I am authenticated as user "alice"

  Scenario: Trigger clustering and list clusters
    # Create three conversations about distinct topics with indexedContent
    Given I have a conversation with title "Python Migration"
    And set "conv1" to "${conversationId}"
    And the conversation has an entry "Flask to FastAPI migration"
    When I list entries for the conversation
    And set "entry1" to the json response field "data[0].id"

    Given I have a conversation with title "K8s Deployment"
    And set "conv2" to "${conversationId}"
    And the conversation has an entry "Kubernetes pod resource limits"
    When I list entries for the conversation
    And set "entry2" to the json response field "data[0].id"

    Given I have a conversation with title "Database Tuning"
    And set "conv3" to "${conversationId}"
    And the conversation has an entry "PostgreSQL index optimization"
    When I list entries for the conversation
    And set "entry3" to the json response field "data[0].id"

    # Index entries so BackgroundIndexer generates embeddings
    Given I am authenticated as admin user "alice"
    When I call POST "/v1/conversations/index" with body:
    """
    [
      {
        "conversationId": "${conv1}",
        "entryId": "${entry1}",
        "indexedContent": "migrate Flask app to FastAPI SQLAlchemy models async routes Python ORM patterns web framework"
      },
      {
        "conversationId": "${conv2}",
        "entryId": "${entry2}",
        "indexedContent": "Kubernetes resource limits pod OOMKilled container spec memory leaks pprof deployment"
      },
      {
        "conversationId": "${conv3}",
        "entryId": "${entry3}",
        "indexedContent": "PostgreSQL queries slow orders table missing index customer_id EXPLAIN ANALYZE CREATE INDEX"
      }
    ]
    """
    Then the response status should be 200

    # Wait for BackgroundIndexer to generate embeddings
    Given I wait for embeddings to be generated

    # Trigger clustering
    When I call POST "/admin/v1/knowledge/trigger" with body:
    """
    {}
    """
    Then the response status should be 200
    And the response body "users_processed" should be "1"

    # List clusters
    When I call GET "/admin/v1/knowledge/clusters"
    Then the response status should be 200
    And the response body should have field "clusters" that is not null

  Scenario: Cluster endpoint requires admin role
    Given I am authenticated as user "bob"
    When I call GET "/admin/v1/knowledge/clusters"
    Then the response status should be 403

  Scenario: Trigger endpoint requires admin role
    Given I am authenticated as user "bob"
    When I call POST "/admin/v1/knowledge/trigger" with body:
    """
    {}
    """
    Then the response status should be 403
