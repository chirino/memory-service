---
layout: ../../../layouts/DocsLayout.astro
title: REST API
description: Memory Service REST API reference.
---

Memory Service exposes a comprehensive REST API for managing conversations and messages.

## Base URL

```
http://localhost:8080/api/v1
```

## Authentication

If authentication is enabled, include a Bearer token:

```bash
curl -H "Authorization: Bearer <token>" http://localhost:8080/api/v1/conversations
```

## Conversations

### List Conversations

```bash
GET /api/v1/conversations
```

Query parameters:
- `limit` - Maximum results (default: 20)
- `offset` - Pagination offset (default: 0)
- `ownerId` - Filter by owner

Response:

```json
{
  "conversations": [
    {
      "id": "conv-123",
      "ownerId": "user-1",
      "createdAt": "2024-01-15T10:00:00Z",
      "updatedAt": "2024-01-15T10:30:00Z",
      "metadata": {}
    }
  ],
  "total": 42
}
```

### Create Conversation

```bash
POST /api/v1/conversations
```

```json
{
  "id": "my-conversation",
  "metadata": {
    "topic": "support"
  }
}
```

### Get Conversation

```bash
GET /api/v1/conversations/{id}
```

### Update Conversation

```bash
PATCH /api/v1/conversations/{id}
```

```json
{
  "metadata": {
    "status": "resolved"
  }
}
```

### Delete Conversation

```bash
DELETE /api/v1/conversations/{id}
```

### Fork Conversation

```bash
POST /api/v1/conversations/{id}/fork
```

```json
{
  "newId": "forked-conversation",
  "atMessage": 5
}
```

## Messages

### List Messages

```bash
GET /api/v1/conversations/{conversationId}/messages
```

Query parameters:
- `limit` - Maximum results (default: 100)
- `offset` - Pagination offset
- `after` - Messages after timestamp
- `before` - Messages before timestamp

### Add Message

```bash
POST /api/v1/conversations/{conversationId}/messages
```

```json
{
  "type": "USER",
  "content": "Hello, how can you help?",
  "metadata": {
    "client": "web"
  }
}
```

Message types: `USER`, `AI`, `SYSTEM`, `TOOL_EXECUTION`

### Get Message

```bash
GET /api/v1/conversations/{conversationId}/messages/{messageId}
```

### Delete Message

```bash
DELETE /api/v1/conversations/{conversationId}/messages/{messageId}
```

## Search

### Semantic Search

```bash
POST /api/v1/search
```

```json
{
  "query": "How do I configure authentication?",
  "limit": 10,
  "minScore": 0.7,
  "conversationIds": ["conv-1", "conv-2"],
  "messageTypes": ["USER", "AI"],
  "after": "2024-01-01T00:00:00Z"
}
```

Response:

```json
{
  "results": [
    {
      "messageId": "msg-123",
      "conversationId": "conv-456",
      "content": "To configure authentication...",
      "score": 0.92,
      "type": "AI",
      "timestamp": "2024-01-15T10:30:00Z"
    }
  ]
}
```

## Health

### Health Check

```bash
GET /q/health
```

### Liveness

```bash
GET /q/health/live
```

### Readiness

```bash
GET /q/health/ready
```

## Error Responses

All errors follow this format:

```json
{
  "error": {
    "code": "NOT_FOUND",
    "message": "Conversation not found",
    "details": {}
  }
}
```

Common error codes:
- `BAD_REQUEST` (400)
- `UNAUTHORIZED` (401)
- `FORBIDDEN` (403)
- `NOT_FOUND` (404)
- `CONFLICT` (409)
- `INTERNAL_ERROR` (500)

## Rate Limiting

Rate limits are returned in response headers:

```
X-RateLimit-Limit: 1000
X-RateLimit-Remaining: 999
X-RateLimit-Reset: 1705320000
```

## OpenAPI Specification

The full OpenAPI spec is available at:

```
GET /q/openapi
```

Or download the YAML:

```
GET /q/openapi?format=yaml
```

## Next Steps

- Learn about [gRPC API](/docs/integrations/grpc/)
- Explore [Deployment Options](/docs/deployment/docker/)
