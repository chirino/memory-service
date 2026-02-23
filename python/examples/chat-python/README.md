# chat-python

Python FastAPI chat app that mirrors the API surface of `quarkus/examples/chat-quarkus` using the Python LangChain integration package.

## Run

```bash
# build frontend bundle once (or after frontend changes)
cd frontends/chat-frontend
npm run build

# run backend + static frontend host
cd ../../python/examples/chat-python
uv sync
uv run python app.py
```

Defaults: `HOST=0.0.0.0`, `PORT=8080`.
Frontend assets are served from `frontends/chat-frontend/dist` (override with `CHAT_FRONTEND_DIR`).

## Endpoints

- `POST /chat/{conversation_id}` (JSON body, SSE response)
- `POST /v1/conversations/resume-check`
- `GET /v1/conversations/{conversation_id}/resume`
- `POST /v1/conversations/{conversation_id}/cancel`
- `GET /v1/conversations`
- `GET|PATCH|DELETE /v1/conversations/{conversation_id}`
- `GET /v1/conversations/{conversation_id}/entries`
- `GET /v1/conversations/{conversation_id}/forks`
- `GET|POST /v1/conversations/{conversation_id}/memberships`
- `PATCH|DELETE /v1/conversations/{conversation_id}/memberships/{user_id}`
- `POST /v1/conversations/search`
- `POST /v1/conversations/{conversation_id}/index`
- `GET|POST /v1/ownership-transfers`
- `GET|DELETE /v1/ownership-transfers/{transfer_id}`
- `POST /v1/ownership-transfers/{transfer_id}/accept`
- `POST /v1/attachments` (multipart upload or JSON create-from-url)
- `GET|DELETE /v1/attachments/{attachment_id}`
- `GET /v1/attachments/{attachment_id}/download-url`
- `GET /v1/attachments/download/{token}/{filename}`
- `GET /v1/me`
- `GET /config.json`
