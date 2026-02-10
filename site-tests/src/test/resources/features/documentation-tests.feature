# Generated from test-scenarios.json (built from MDX)
# DO NOT EDIT: This file is auto-generated
Feature: Indexing And Search Tutorial

  Background:
    Given the memory-service is running via docker compose
    And I set up authentication tokens

  Scenario: Test 07-with-search
    # From /docs/spring/indexing-and-search/
    Given I have checkpoint "spring/examples/doc-checkpoints/07-with-search"
    When I build the checkpoint
    Then the build should succeed

    When I start the checkpoint on port 10090
    Then the application should be running

    When I execute curl command:
      """
      curl -NsSfX POST http://localhost:10090/chat/3579aac5-c86e-4b67-bbea-6ec1a3644942 \
      -H "Content-Type: application/json" \
      -H "Authorization: Bearer $(get-token)" \
      -d "Give me a random number between 1 and 100."
      """
    Then the response status should be 200
    And the response body should be text:
    """
    Sure! Here's a random number between 1 and 100: **42**.
    """

    When I stop the checkpoint

  Scenario: Test 03-with-history
    # From /docs/quarkus/conversation-history/
    Given I have checkpoint "quarkus/examples/doc-checkpoints/03-with-history"
    When I build the checkpoint
    Then the build should succeed

    When I start the checkpoint on port 10091
    Then the application should be running

    When I execute curl command:
      """
      function get-token() {
      curl -sSfX POST http://localhost:8081/realms/memory-service/protocol/openid-connect/token \
      -H "Content-Type: application/x-www-form-urlencoded" \
      -d "client_id=memory-service-client" \
      -d "client_secret=change-me" \
      -d "grant_type=password" \
      -d "username=bob" \
      -d "password=bob" \
      | jq -r '.access_token'
      }
      """

    When I execute curl command:
      """
      curl -NsSfX POST http://localhost:10091/chat/3579aac5-c86e-4b67-bbea-6ec1a3644942 \
      -H "Content-Type: text/plain" \
      -H "Authorization: Bearer $(get-token)" \
      -d "Give me a random number between 1 and 100."
      """
    Then the response status should be 200
    And the response should match pattern "\d+"

    When I execute curl command:
      """
      curl -sSfX GET http://localhost:10091/v1/conversations/3579aac5-c86e-4b67-bbea-6ec1a3644942/ \
      -H "Authorization: Bearer $(get-token)" | jq
      """
    Then the response status should be 200
    And the response body should be json:
    """
    {
    "id": "3579aac5-c86e-4b67-bbea-6ec1a3644942",
    "title": "%{response.body.title}",
    "ownerUserId": "bob",
    "createdAt": "%{response.body.createdAt}",
    "updatedAt": "%{response.body.updatedAt}",
    "accessLevel": "owner"
    }
    """

    When I execute curl command:
      """
      curl -sSfX GET http://localhost:10091/v1/conversations/3579aac5-c86e-4b67-bbea-6ec1a3644942/entries \
      -H "Authorization: Bearer $(get-token)" | jq
      """
    Then the response status should be 200
    And the response body should be json:
    """
    {
    "data": [
    {
    "id": "%{response.body.data[0].id}",
    "conversationId": "3579aac5-c86e-4b67-bbea-6ec1a3644942",
    "userId": "bob",
    "channel": "history",
    "contentType": "history",
    "content": [{"role": "USER", "text": "Give me a random number between 1 and 100."}],
    "createdAt": "%{response.body.data[0].createdAt}"
    },
    {
    "id": "%{response.body.data[1].id}",
    "conversationId": "3579aac5-c86e-4b67-bbea-6ec1a3644942",
    "userId": "bob",
    "channel": "history",
    "contentType": "history",
    "content": [{"role": "AI", "text": "%{response.body.data[1].content[0].text}"}],
    "createdAt": "%{response.body.data[1].createdAt}"
    }
    ]
    }
    """

    When I execute curl command:
      """
      curl -sSfX GET http://localhost:10091/v1/conversations \
      -H "Authorization: Bearer $(get-token)" | jq
      """
    Then the response status should be 200
    And the response body should be json:
    """
    {
    "data": [
    {
    "id": "%{response.body.data[0].id}",
    "title": "%{response.body.data[0].title}",
    "ownerUserId": "bob",
    "createdAt": "%{response.body.data[0].createdAt}",
    "updatedAt": "%{response.body.data[0].updatedAt}",
    "accessLevel": "owner"
    }
    ]
    }
    """

    When I stop the checkpoint

  Scenario: Test 01-basic-agent
    # From /docs/quarkus/getting-started/
    Given I have checkpoint "quarkus/examples/doc-checkpoints/01-basic-agent"
    When I build the checkpoint
    Then the build should succeed

    When I start the checkpoint on port 10092
    Then the application should be running

    When I execute curl command:
      """
      curl -NsSfX POST http://localhost:10092/chat \
      -H "Content-Type: text/plain" \
      -d "Hi, I'm Hiram, who are you?"
      """
    Then the response status should be 200
    And the response body should be text:
    """
    I am Claude, an AI assistant created by Anthropic. I'm here to help answer questions and have conversations on a wide variety of topics. How can I assist you today?
    """

    When I execute curl command:
      """
      curl -NsSfX POST http://localhost:10092/chat \
      -H "Content-Type: text/plain" \
      -d "Who am I?"
      """
    Then the response status should be 200
    And the response should not contain "Hiram"

    When I stop the checkpoint

  Scenario: Test 02-with-memory
    # From /docs/quarkus/getting-started/
    Given I have checkpoint "quarkus/examples/doc-checkpoints/02-with-memory"
    When I build the checkpoint
    Then the build should succeed

    When I start the checkpoint on port 10093
    Then the application should be running

    When I execute curl command:
      """
      function get-token() {
      curl -sSfX POST http://localhost:8081/realms/memory-service/protocol/openid-connect/token \
      -H "Content-Type: application/x-www-form-urlencoded" \
      -d "client_id=memory-service-client" \
      -d "client_secret=change-me" \
      -d "grant_type=password" \
      -d "username=bob" \
      -d "password=bob" \
      | jq -r '.access_token'
      }
      """

    When I execute curl command:
      """
      curl -NsSfX POST http://localhost:10093/chat/3579aac5-c86e-4b67-bbea-6ec1a3644942 \
      -H "Content-Type: text/plain" \
      -H "Authorization: Bearer $(get-token)" \
      -d "Hi, I'm Hiram, who are you?"
      """
    Then the response status should be 200
    And the response body should be text:
    """
    Hi Hiram! I'm an AI created to help answer questions and provide information. How can I assist you today?
    """

    When I execute curl command:
      """
      curl -NsSfX POST http://localhost:10093/chat/3579aac5-c86e-4b67-bbea-6ec1a3644942 \
      -H "Content-Type: text/plain" \
      -H "Authorization: Bearer $(get-token)" \
      -d "Who am I?"
      """
    Then the response status should be 200
    And the response should contain "Hiram"

    When I stop the checkpoint

  Scenario: Test 07-with-search
    # From /docs/quarkus/indexing-and-search/
    Given I have checkpoint "quarkus/examples/doc-checkpoints/07-with-search"
    When I build the checkpoint
    Then the build should succeed

    When I start the checkpoint on port 10094
    Then the application should be running

    When I execute curl command:
      """
      function get-token() {
      curl -sSfX POST http://localhost:8081/realms/memory-service/protocol/openid-connect/token \
      -H "Content-Type: application/x-www-form-urlencoded" \
      -d "client_id=memory-service-client" \
      -d "client_secret=change-me" \
      -d "grant_type=password" \
      -d "username=bob" \
      -d "password=bob" \
      | jq -r '.access_token'
      }
      """

    When I execute curl command:
      """
      curl -NsSfX POST http://localhost:10094/chat/3579aac5-c86e-4b67-bbea-6ec1a3644942 \
      -H "Content-Type: text/plain" \
      -H "Authorization: Bearer $(get-token)" \
      -d "Give me a random number between 1 and 100."
      """
    Then the response status should be 200
    And the response should match pattern "\d+"

    When I stop the checkpoint

  Scenario: Test 05-response-resumption
    # From /docs/quarkus/response-resumption/
    Given I have checkpoint "quarkus/examples/doc-checkpoints/05-response-resumption"
    When I build the checkpoint
    Then the build should succeed

    When I start the checkpoint on port 10095
    Then the application should be running

    When I execute curl command:
      """
      curl -NsSfX POST http://localhost:10095/chat/3579aac5-c86e-4b67-bbea-6ec1a3644942 \
      -H "Content-Type: text/plain" \
      -H "Authorization: Bearer $(get-token)" \
      -d "Write a 4 paragraph story about a cat."
      """
    Then the response status should be 200
    And the response should match pattern "\w+"

    When I stop the checkpoint

  Scenario: Test 06-sharing
    # From /docs/quarkus/sharing/
    Given I have checkpoint "quarkus/examples/doc-checkpoints/06-sharing"
    When I build the checkpoint
    Then the build should succeed

    When I start the checkpoint on port 10096
    Then the application should be running

    When I execute curl command:
      """
      curl -sSfX POST http://localhost:10096/chat/3579aac5-c86e-4b67-bbea-6ec1a3644942 \
      -H "Content-Type: text/plain" \
      -H "Authorization: Bearer $(get-token)" \
      -d "Hello, starting a conversation for sharing tests."
      """
    Then the response status should be 200

    When I execute curl command:
      """
      # As bob (owner), list members of the conversation
      curl -sSfX GET http://localhost:10096/v1/conversations/3579aac5-c86e-4b67-bbea-6ec1a3644942/memberships \
      -H "Authorization: Bearer $(get-token bob bob)" | jq
      """
    Then the response status should be 200
    And the response body should be json:
    """
    {
    "data": [
    {
    "conversationId": "3579aac5-c86e-4b67-bbea-6ec1a3644942",
    "userId": "bob",
    "accessLevel": "owner",
    "createdAt": "%{response.body.data[0].createdAt}"
    }
    ]
    }
    """

    When I execute curl command:
      """
      # Share conversation with alice as a writer
      curl -sSfX POST http://localhost:10096/v1/conversations/3579aac5-c86e-4b67-bbea-6ec1a3644942/memberships \
      -H "Authorization: Bearer $(get-token bob bob)" \
      -H "Content-Type: application/json" \
      -d '{
      "userId": "alice",
      "accessLevel": "writer"
      }' | jq
      """
    Then the response status should be 201
    And the response body should be json:
    """
    {
    "conversationId": "3579aac5-c86e-4b67-bbea-6ec1a3644942",
    "userId": "alice",
    "accessLevel": "writer",
    "createdAt": "%{response.body.createdAt}"
    }
    """

    When I execute curl command:
      """
      curl -sSfX POST http://localhost:10096/chat/3579aac5-c86e-4b67-bbea-6ec1a3644942 \
      -H "Authorization: Bearer $(get-token alice alice)" \
      -H "Content-Type: text/plain" \
      -d "Hi from Alice!"
      """
    Then the response status should be 200

    When I stop the checkpoint

  Scenario: Test 03-with-history
    # From /docs/spring/conversation-history/
    Given I have checkpoint "spring/examples/doc-checkpoints/03-with-history"
    When I build the checkpoint
    Then the build should succeed

    When I start the checkpoint on port 10097
    Then the application should be running

    When I execute curl command:
      """
      curl -NsSfX POST http://localhost:10097/chat/3579aac5-c86e-4b67-bbea-6ec1a3644942 \
      -H "Content-Type: application/json" \
      -H "Authorization: Bearer $(get-token)" \
      -d "Give me a random number between 1 and 100."
      """
    Then the response status should be 200
    And the response body should be text:
    """
    Sure! Here's a random number between 1 and 100: **42**.
    """

    When I execute curl command:
      """
      curl -sSfX GET http://localhost:10097/v1/conversations/3579aac5-c86e-4b67-bbea-6ec1a3644942 \
      -H "Authorization: Bearer $(get-token)" | jq
      """
    Then the response status should be 200
    And the response body should be json:
    """
    {
    "id": "3579aac5-c86e-4b67-bbea-6ec1a3644942",
    "title": "%{response.body.title}",
    "ownerUserId": "bob",
    "createdAt": "%{response.body.createdAt}",
    "updatedAt": "%{response.body.updatedAt}",
    "lastMessagePreview": null,
    "accessLevel": "owner",
    "forkedAtEntryId": null,
    "forkedAtConversationId": null
    }
    """

    When I execute curl command:
      """
      curl -sSfX GET http://localhost:10097/v1/conversations/3579aac5-c86e-4b67-bbea-6ec1a3644942/entries \
      -H "Authorization: Bearer $(get-token)" | jq
      """
    Then the response status should be 200
    And the response should contain "Give me a random number"
    And the response should contain "history"

    When I execute curl command:
      """
      curl -sSfX GET http://localhost:10097/v1/conversations \
      -H "Authorization: Bearer $(get-token)" | jq
      """
    Then the response status should be 200
    And the response should contain "3579aac5-c86e-4b67-bbea-6ec1a3644942"
    And the response should contain "owner"
    And the response should contain "bob"

    When I stop the checkpoint

  Scenario: Test 01-basic-agent
    # From /docs/spring/getting-started/
    Given I have checkpoint "spring/examples/doc-checkpoints/01-basic-agent"
    When I build the checkpoint
    Then the build should succeed

    When I start the checkpoint on port 10098
    Then the application should be running

    When I execute curl command:
      """
      curl -NsSfX POST http://localhost:10098/chat \
      -H "Content-Type: application/json" \
      -d '"Hi, I'\''m Hiram, who are you?"'
      """
    Then the response status should be 200
    And the response body should be text:
    """
    Hello Hiram! I'm an AI language model created by OpenAI, here to help answer questions and provide information on a wide range of topics. How can I assist you today?
    """

    When I execute curl command:
      """
      curl -NsSfX POST http://localhost:10098/chat \
      -H "Content-Type: application/json" \
      -d '"Who am I?"'
      """
    Then the response status should be 200
    And the response should not contain "Hiram"

    When I stop the checkpoint

  Scenario: Test 02-with-memory
    # From /docs/spring/getting-started/
    Given I have checkpoint "spring/examples/doc-checkpoints/02-with-memory"
    When I build the checkpoint
    Then the build should succeed

    When I start the checkpoint on port 10099
    Then the application should be running

    When I execute curl command:
      """
      function get-token() {
      curl -sSfX POST http://localhost:8081/realms/memory-service/protocol/openid-connect/token \
      -H "Content-Type: application/x-www-form-urlencoded" \
      -d "client_id=memory-service-client" \
      -d "client_secret=change-me" \
      -d "grant_type=password" \
      -d "username=bob" \
      -d "password=bob" \
      | jq -r '.access_token'
      }
      """

    When I execute curl command:
      """
      curl -NsSfX POST http://localhost:10099/chat/3579aac5-c86e-4b67-bbea-6ec1a3644942 \
      -H "Content-Type: application/json" \
      -H "Authorization: Bearer $(get-token)" \
      -d '"Hi, I'\''m Hiram, who are you?"'
      """
    Then the response status should be 200
    And the response body should be text:
    """
    Hello Hiram! I'm an AI assistant here to help you with any questions or information you might need. How can I assist you today?
    """

    When I execute curl command:
      """
      curl -NsSfX POST http://localhost:10099/chat/3579aac5-c86e-4b67-bbea-6ec1a3644942 \
      -H "Content-Type: application/json" \
      -H "Authorization: Bearer $(get-token)" \
      -d '"Who am I?"'
      """
    Then the response status should be 200
    And the response should contain "Hiram"

    When I stop the checkpoint

  Scenario: Test 05-response-resumption
    # From /docs/spring/response-resumption/
    Given I have checkpoint "spring/examples/doc-checkpoints/05-response-resumption"
    When I build the checkpoint
    Then the build should succeed

    When I start the checkpoint on port 10100
    Then the application should be running

    When I execute curl command:
      """
      curl -NsSfX POST http://localhost:10100/chat/3579aac5-c86e-4b67-bbea-6ec1a3644942 \
      -H "Content-Type: text/plain" \
      -H "Authorization: Bearer $(get-token)" \
      -d "Write a 4 paragraph story about a cat."
      """
    Then the response status should be 200
    And the response should match pattern "\w+"

    When I stop the checkpoint

  Scenario: Test 06-sharing
    # From /docs/spring/sharing/
    Given I have checkpoint "spring/examples/doc-checkpoints/06-sharing"
    When I build the checkpoint
    Then the build should succeed

    When I start the checkpoint on port 10101
    Then the application should be running

    When I execute curl command:
      """
      curl -sSfX POST http://localhost:10101/chat/a1b2c3d4-e5f6-4789-abcd-ef0123456789 \
      -H "Content-Type: text/plain" \
      -H "Authorization: Bearer $(get-token)" \
      -d "Hello, starting a conversation for sharing tests."
      """
    Then the response status should be 200

    When I execute curl command:
      """
      # As bob (owner), list members of the conversation
      curl -sSfX GET http://localhost:10101/v1/conversations/a1b2c3d4-e5f6-4789-abcd-ef0123456789/memberships \
      -H "Authorization: Bearer $(get-token bob bob)" | jq
      """
    Then the response status should be 200
    And the response body should be json:
    """
    {
    "data": [
    {
    "conversationId": "a1b2c3d4-e5f6-4789-abcd-ef0123456789",
    "userId": "bob",
    "accessLevel": "owner",
    "createdAt": "%{response.body.data[0].createdAt}"
    }
    ]
    }
    """

    When I execute curl command:
      """
      # Share conversation with alice as a writer
      curl -sSfX POST http://localhost:10101/v1/conversations/a1b2c3d4-e5f6-4789-abcd-ef0123456789/memberships \
      -H "Authorization: Bearer $(get-token bob bob)" \
      -H "Content-Type: application/json" \
      -d '{
      "userId": "alice",
      "accessLevel": "writer"
      }' | jq
      """
    Then the response status should be 201
    And the response body should be json:
    """
    {
    "conversationId": "a1b2c3d4-e5f6-4789-abcd-ef0123456789",
    "userId": "alice",
    "accessLevel": "writer",
    "createdAt": "%{response.body.createdAt}"
    }
    """

    When I execute curl command:
      """
      curl -sSfX POST http://localhost:10101/chat/a1b2c3d4-e5f6-4789-abcd-ef0123456789 \
      -H "Authorization: Bearer $(get-token alice alice)" \
      -H "Content-Type: text/plain" \
      -d "Hi from Alice!"
      """
    Then the response status should be 200

    When I stop the checkpoint

