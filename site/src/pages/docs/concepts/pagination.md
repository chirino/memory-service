---
layout: ../../../layouts/DocsLayout.astro
title: Pagination
description: How cursor-based pagination works across Memory Service list endpoints.
---

Memory Service uses **cursor-based pagination** for all list endpoints. Instead of page numbers or offsets, you pass the cursor from the previous response to fetch the next batch. This approach is efficient, consistent under concurrent writes, and scales well for large datasets.

## How It Works

Every paginated endpoint returns a response with two fields:

```json
{
  "data": [ ... ],
  "afterCursor": "d8c78c2b-b24a-46f4-86fd-22f066f74526"
}
```

| Field         | Description                                                                               |
| ------------- | ----------------------------------------------------------------------------------------- |
| `data`        | Array of results for the current page                                                     |
| `afterCursor` | Cursor to pass in the next request for more results. `null` when there are no more pages. |

To paginate through results:

1. Make the initial request (optionally with a `limit`)
2. Check `afterCursor` in the response
3. If `afterCursor` is not `null`, pass it as the `afterCursor` parameter in the next request
4. Repeat until `afterCursor` is `null`

## Paginated Endpoints

All paginated endpoints share the same pattern but differ in defaults and parameter placement.

### Agent API

| Endpoint                                 | Cursor Param          | Limit Param     | Default Limit | Max Limit |
| ---------------------------------------- | --------------------- | --------------- | ------------- | --------- |
| `GET /v1/conversations`                  | `afterCursor` (query) | `limit` (query) | 20            | 200       |
| `GET /v1/conversations/{id}/entries`     | `afterCursor` (query) | `limit` (query) | 50            | 200       |
| `GET /v1/conversations/{id}/memberships` | `afterCursor` (query) | `limit` (query) | 50            | 200       |
| `GET /v1/conversations/{id}/forks`       | `afterCursor` (query) | `limit` (query) | 50            | 200       |
| `POST /v1/conversations/search`          | `afterCursor` (body)  | `limit` (body)  | 20            | 200       |
| `GET /v1/conversations/unindexed`        | `afterCursor` (query) | `limit` (query) | 100           | 200       |
| `GET /v1/ownership-transfers`            | `afterCursor` (query) | `limit` (query) | 50            | 200       |

### Admin API

| Endpoint                                       | Cursor Param          | Limit Param     | Default Limit | Max Limit |
| ---------------------------------------------- | --------------------- | --------------- | ------------- | --------- |
| `GET /v1/admin/conversations`                  | `afterCursor` (query) | `limit` (query) | 100           | 1000      |
| `GET /v1/admin/conversations/{id}/entries`     | `afterCursor` (query) | `limit` (query) | 50            | 1000      |
| `GET /v1/admin/conversations/{id}/memberships` | `afterCursor` (query) | `limit` (query) | 50            | 1000      |
| `GET /v1/admin/conversations/{id}/forks`       | `afterCursor` (query) | `limit` (query) | 50            | 1000      |
| `POST /v1/admin/conversations/search`          | `afterCursor` (body)  | `limit` (body)  | 20            | 1000      |
| `GET /v1/admin/attachments`                    | `afterCursor` (query) | `limit` (query) | 50            | 1000      |

## Listing Conversations

Fetch conversations in pages of a given size using the `limit` and `afterCursor` query parameters.

### First page

```bash
curl "http://localhost:8080/v1/conversations?limit=2" \
  -H "Authorization: Bearer <token>"
```

Response:

```json
{
  "data": [
    {
      "id": "d8c78c2b-b24a-46f4-86fd-22f066f74526",
      "title": "Support chat",
      "ownerUserId": "user_1234",
      "createdAt": "2025-01-10T14:32:05Z",
      "updatedAt": "2025-01-10T14:45:12Z",
      "accessLevel": "owner"
    },
    {
      "id": "660e8400-e29b-41d4-a716-446655440001",
      "title": "Design discussion",
      "ownerUserId": "user_1234",
      "createdAt": "2025-01-11T09:00:00Z",
      "updatedAt": "2025-01-11T09:15:00Z",
      "accessLevel": "owner"
    }
  ],
  "afterCursor": "660e8400-e29b-41d4-a716-446655440001"
}
```

### Next page

Pass the `afterCursor` value as the `afterCursor` parameter:

```bash
curl "http://localhost:8080/v1/conversations?limit=2&afterCursor=660e8400-e29b-41d4-a716-446655440001" \
  -H "Authorization: Bearer <token>"
```

Response (last page):

```json
{
  "data": [
    {
      "id": "770e8400-e29b-41d4-a716-446655440002",
      "title": "Bug triage",
      "ownerUserId": "user_1234",
      "createdAt": "2025-01-12T10:00:00Z",
      "updatedAt": "2025-01-12T10:30:00Z",
      "accessLevel": "owner"
    }
  ],
  "afterCursor": null
}
```

An `afterCursor` of `null` means there are no more results.

## Listing Entries

Entries within a conversation are paginated the same way, using `afterCursor` and `limit` query parameters.

### First page

```bash
curl "http://localhost:8080/v1/conversations/{conversationId}/entries?limit=2" \
  -H "Authorization: Bearer <token>"
```

Response:

```json
{
  "data": [
    {
      "id": "aaa7b810-9dad-11d1-80b4-00c04fd430c8",
      "conversationId": "d8c78c2b-b24a-46f4-86fd-22f066f74526",
      "userId": "user_1234",
      "channel": "history",
      "contentType": "history",
      "content": [{ "role": "USER", "text": "Hello!" }],
      "createdAt": "2025-01-10T14:40:12Z"
    },
    {
      "id": "bbb7b810-9dad-11d1-80b4-00c04fd430c9",
      "conversationId": "d8c78c2b-b24a-46f4-86fd-22f066f74526",
      "userId": "user_1234",
      "channel": "history",
      "contentType": "history",
      "content": [{ "role": "AI", "text": "Hi there! How can I help?" }],
      "createdAt": "2025-01-10T14:40:15Z"
    }
  ],
  "afterCursor": "bbb7b810-9dad-11d1-80b4-00c04fd430c9"
}
```

### Next page

```bash
curl "http://localhost:8080/v1/conversations/{conversationId}/entries?limit=2&afterCursor=bbb7b810-9dad-11d1-80b4-00c04fd430c9" \
  -H "Authorization: Bearer <token>"
```

## Search Results

Search pagination works through the request body rather than query parameters. Pass `afterCursor` and `limit` in the JSON body of the `POST` request.

### First page

```bash
curl -X POST "http://localhost:8080/v1/conversations/search" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{
    "query": "memory service design",
    "limit": 2
  }'
```

Response:

```json
{
  "data": [
    {
      "conversationId": "d8c78c2b-b24a-46f4-86fd-22f066f74526",
      "conversationTitle": "Design Discussion",
      "entryId": "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
      "score": 0.93,
      "highlights": "memory service design decisions",
      "entry": { ... }
    },
    {
      "conversationId": "660e8400-e29b-41d4-a716-446655440001",
      "conversationTitle": "Architecture Review",
      "entryId": "7ca7b810-9dad-11d1-80b4-00c04fd430d9",
      "score": 0.87,
      "highlights": "designing the memory layer",
      "entry": { ... }
    }
  ],
  "afterCursor": "eyJzY29yZSI6MC44Nywi..."
}
```

### Next page

```bash
curl -X POST "http://localhost:8080/v1/conversations/search" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{
    "query": "memory service design",
    "limit": 2,
    "afterCursor": "eyJzY29yZSI6MC44Nywi..."
  }'
```

## Limits Reference

| Constraint                    | Value                      |
| ----------------------------- | -------------------------- |
| Minimum `limit`               | 1                          |
| Maximum `limit`               | 200 (agent) / 1000 (admin) |
| Default limit (conversations) | 20                         |
| Default limit (entries)       | 50                         |
| Default limit (memberships)   | 50                         |
| Default limit (forks)         | 50                         |
| Default limit (search)        | 20                         |
| Default limit (unindexed)     | 100                        |
| Default limit (transfers)     | 50                         |

If no `limit` is provided, the endpoint-specific default is used. Requesting a `limit` above the maximum or below 1 returns a validation error.

## Best Practices

1. **Always check `afterCursor`** — don't assume a fixed number of pages. When it's `null`, you've reached the end.
2. **Use reasonable page sizes** — smaller pages (20–50) give faster individual responses; larger pages (100–200) reduce the number of round trips.
3. **Don't store cursors long-term** — cursors are opaque position markers. They may become invalid if the underlying data changes (e.g., a conversation is deleted).
4. **Paginate in loops** — for batch processing, loop until `afterCursor` is `null` rather than guessing when to stop.

## Next Steps

- Learn about [Conversations](/docs/concepts/conversations/)
- Learn about [Entries](/docs/concepts/entries/)
- Explore [Indexing & Search](/docs/concepts/indexing-and-search/)
