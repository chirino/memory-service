---
name: cucumber-features
description: Use when editing or creating Cucumber feature files (*.feature). Provides API documentation guidelines.
autoTrigger:
  - files: ["**/*.feature"]
---

# Cucumber Feature Files as API Documentation

Feature files should serve as API documentation for new users without needing to review step implementations.

## Key Principle: Show First, Then Shorthand

**First occurrence** of an API: Use raw HTTP steps to document the exact URL and JSON structure:

```gherkin
When I call POST "/v1/conversations/${conversationId}/entries" with body:
"""
{
  "channel": "HISTORY",
  "contentType": "history",
  "content": [{"text": "Hello", "role": "USER"}]
}
"""
Then the response status should be 201
And the response body should be json:
"""
{
  "id": "${response.body.id}",
  "conversationId": "${conversationId}",
  "channel": "history",
  "contentType": "history",
  "content": [{"text": "Hello", "role": "USER"}],
  "createdAt": "${response.body.createdAt}"
}
"""
```

**Subsequent occurrences**: Use shorthand steps since API is already documented:

```gherkin
Given the conversation has an entry "Hello"
When I list entries for the conversation
Then the response should contain 1 entry
```

## Raw HTTP Steps Reference

```gherkin
When I call GET "/v1/conversations/${conversationId}"
When I call POST "/v1/path" with body:
When I call DELETE "/v1/path"
```

## Variable Capture

```gherkin
And set "myVar" to the json response field "id"
And set "myVar" to "${response.body.id}"
# Use later: "/v1/conversations/${myVar}"
```
