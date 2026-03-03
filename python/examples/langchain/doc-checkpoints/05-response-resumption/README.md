# 05-response-resumption

Python docs checkpoint for Memory Service tutorial testing.

Implements the same response-resumption flow as `python/examples/langchain/chat-langchain`:

- `POST /chat/{conversation_id}` streams SSE events from a plain text request body.
- `POST /v1/conversations/resume-check` checks active recordings.
- `GET /v1/conversations/{conversation_id}/resume` replays SSE from Memory Service.
- `POST /v1/conversations/{conversation_id}/cancel` cancels local + proxied Memory Service recording.
