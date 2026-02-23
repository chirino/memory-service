from __future__ import annotations

import base64
import json
import os
from pathlib import Path
from typing import Any

from fastapi import FastAPI, File, HTTPException, Query, Request, Response, UploadFile
from fastapi.responses import FileResponse, JSONResponse, StreamingResponse
from langchain.agents import create_agent
from langchain_openai import ChatOpenAI
from pydantic import BaseModel
from memory_service_langchain import (
    MemoryServiceCheckpointSaver,
    MemoryServiceHistoryMiddleware,
    MemoryServiceProxy,
    MemoryServiceResponseResumer,
    extract_stream_text,
    install_fastapi_authorization_middleware,
    memory_service_scope,
    to_fastapi_response,
)


class RequestAttachmentRef(BaseModel):
    attachmentId: str | None = None
    contentType: str | None = None
    name: str | None = None
    href: str | None = None


class MessageRequest(BaseModel):
    message: str
    attachments: list[RequestAttachmentRef] | None = None
    forkedAtConversationId: str | None = None
    forkedAtEntryId: str | None = None


def parse_optional_int(value: str | None) -> int | None:
    if value is None or value == "":
        return None
    try:
        return int(value)
    except ValueError as exc:
        raise HTTPException(400, f"invalid integer value: {value}") from exc


def parse_jwt_claims(authorization: str | None) -> dict[str, Any]:
    if authorization is None or not authorization.startswith("Bearer "):
        return {}

    token = authorization[len("Bearer ") :].strip()
    parts = token.split(".")
    if len(parts) < 2:
        return {}

    payload = parts[1]
    padding = "=" * ((4 - (len(payload) % 4)) % 4)
    try:
        decoded = base64.urlsafe_b64decode(payload + padding)
        parsed = json.loads(decoded.decode("utf-8"))
    except Exception:
        return {}

    return parsed if isinstance(parsed, dict) else {}


def to_sse_chunk(payload: Any) -> str:
    return f"data: {json.dumps(payload, separators=(',', ':'))}\\n\\n"


def history_text_from_entry(entry: dict[str, Any]) -> str:
    content = entry.get("content")
    if not isinstance(content, list):
        return ""

    lines: list[str] = []
    for block in content:
        if not isinstance(block, dict):
            continue
        text = block.get("text")
        if not isinstance(text, str) or not text.strip():
            continue
        role = block.get("role")
        if isinstance(role, str) and role.strip():
            lines.append(f"{role}: {text.strip()}")
        else:
            lines.append(text.strip())

    return "\n".join(lines)


def pass_through_indexed_content(text: str, role: str) -> str:
    del role
    return text


openai_base_url = os.getenv("OPENAI_BASE_URL")
if openai_base_url and not openai_base_url.rstrip("/").endswith("/v1"):
    openai_base_url = openai_base_url.rstrip("/") + "/v1"
if openai_base_url:
    os.environ.setdefault("OPENAI_API_BASE", openai_base_url)

model = ChatOpenAI(
    model=os.getenv("OPENAI_MODEL", "gpt-4o"),
    openai_api_base=openai_base_url,
    api_key=os.getenv("OPENAI_API_KEY", "test-openai-key"),
)

checkpointer = MemoryServiceCheckpointSaver()
history_middleware = MemoryServiceHistoryMiddleware(
    indexed_content_provider=pass_through_indexed_content,
)

agent = create_agent(
    model=model,
    tools=[],
    checkpointer=checkpointer,
    middleware=[history_middleware],
    system_prompt="You are a helpful assistant.",
)

app = FastAPI(title="Python Chat Example")
install_fastapi_authorization_middleware(app)
proxy = MemoryServiceProxy()
resumer = MemoryServiceResponseResumer()

_repo_root = Path(__file__).resolve().parents[3]
_default_frontend_dir = _repo_root / "frontends" / "chat-frontend" / "dist"
_frontend_dir = Path(
    os.getenv("CHAT_FRONTEND_DIR", str(_default_frontend_dir))
).resolve()
_frontend_index = _frontend_dir / "index.html"


@app.post("/chat/{conversation_id}")
async def chat(conversation_id: str, body: MessageRequest) -> StreamingResponse:
    message = body.message.strip()
    if not message:
        raise HTTPException(400, "Message is required")

    if bool(body.forkedAtConversationId) != bool(body.forkedAtEntryId):
        raise HTTPException(
            400,
            "forkedAtConversationId and forkedAtEntryId must be provided together",
        )

    async def source():
        with memory_service_scope(
            conversation_id,
            body.forkedAtConversationId,
            body.forkedAtEntryId,
        ):
            async for event in agent.astream(
                {"messages": [{"role": "user", "content": message}]},
                {"configurable": {"thread_id": conversation_id}},
                stream_mode="messages",
            ):
                token = extract_stream_text(event)
                if token:
                    yield to_sse_chunk({"token": token})

    return StreamingResponse(
        resumer.stream_from_source(conversation_id, source()),
        media_type="text/event-stream",
    )


@app.post("/v1/conversations/resume-check")
async def resume_check(conversation_ids: list[str]) -> JSONResponse:
    return JSONResponse(resumer.check(conversation_ids), status_code=200)


@app.get("/v1/conversations/{conversation_id}/resume")
async def resume_response(conversation_id: str) -> StreamingResponse:
    try:
        stream = resumer.replay(conversation_id)
    except KeyError as exc:
        raise HTTPException(404, "no in-progress response") from exc
    return StreamingResponse(stream, media_type="text/event-stream")


@app.post("/v1/conversations/{conversation_id}/cancel")
async def cancel_response(conversation_id: str):
    resumer.cancel(conversation_id)
    return Response(status_code=200)


@app.get("/v1/conversations")
async def list_conversations(request: Request):
    response = await proxy.list_conversations(
        mode=request.query_params.get("mode"),
        after_cursor=request.query_params.get("afterCursor"),
        limit=parse_optional_int(request.query_params.get("limit")),
        query=request.query_params.get("query"),
    )
    return to_fastapi_response(response)


@app.get("/v1/conversations/{conversation_id}")
async def get_conversation(conversation_id: str):
    response = await proxy.get_conversation(conversation_id)
    return to_fastapi_response(response)


@app.patch("/v1/conversations/{conversation_id}")
async def update_conversation(conversation_id: str, request: Request):
    payload = await request.json()
    if not isinstance(payload, dict):
        raise HTTPException(400, "Invalid request body")
    response = await proxy.update_conversation(conversation_id, payload)
    return to_fastapi_response(response)


@app.delete("/v1/conversations/{conversation_id}")
async def delete_conversation(conversation_id: str):
    response = await proxy.delete_conversation(conversation_id)
    return to_fastapi_response(response)


@app.get("/v1/conversations/{conversation_id}/entries")
async def list_entries(conversation_id: str, request: Request):
    response = await proxy.list_conversation_entries(
        conversation_id,
        after_cursor=request.query_params.get("afterCursor"),
        limit=parse_optional_int(request.query_params.get("limit")),
        channel="history",
        forks="all",
    )
    return to_fastapi_response(response)


@app.get("/v1/conversations/{conversation_id}/forks")
async def list_forks(conversation_id: str):
    response = await proxy.list_conversation_forks(conversation_id)
    return to_fastapi_response(response)


@app.get("/v1/conversations/{conversation_id}/memberships")
async def list_memberships(conversation_id: str):
    response = await proxy.list_memberships(conversation_id)
    return to_fastapi_response(response)


@app.post("/v1/conversations/{conversation_id}/memberships")
async def create_membership(conversation_id: str, request: Request):
    payload = await request.json()
    if not isinstance(payload, dict):
        raise HTTPException(400, "Invalid request body")
    response = await proxy.create_membership(conversation_id, payload)
    return to_fastapi_response(response)


@app.patch("/v1/conversations/{conversation_id}/memberships/{user_id}")
async def update_membership(conversation_id: str, user_id: str, request: Request):
    payload = await request.json()
    if not isinstance(payload, dict):
        raise HTTPException(400, "Invalid request body")
    response = await proxy.update_membership(conversation_id, user_id, payload)
    return to_fastapi_response(response)


@app.delete("/v1/conversations/{conversation_id}/memberships/{user_id}")
async def delete_membership(conversation_id: str, user_id: str):
    response = await proxy.delete_membership(conversation_id, user_id)
    return to_fastapi_response(response)


@app.post("/v1/conversations/search")
async def search_conversations(request: Request):
    payload = await request.json()
    if not isinstance(payload, dict):
        raise HTTPException(400, "Invalid request body")
    response = await proxy.search_conversations(payload)
    return to_fastapi_response(response)


@app.post("/v1/conversations/{conversation_id}/index")
async def index_conversation(conversation_id: str):
    entries: list[dict[str, Any]] = []
    after_cursor: str | None = None

    while True:
        list_response = await proxy.list_conversation_entries(
            conversation_id,
            after_cursor=after_cursor,
            limit=200,
            channel="history",
            forks="all",
        )
        if list_response.status_code >= 400:
            return to_fastapi_response(list_response)

        body = list_response.json()
        page = body.get("data")
        if isinstance(page, list):
            entries.extend(item for item in page if isinstance(item, dict))

        next_cursor = body.get("afterCursor")
        if not isinstance(next_cursor, str) or not next_cursor:
            break
        after_cursor = next_cursor

    if not entries:
        return Response(status_code=204)

    transcript_parts = [history_text_from_entry(entry) for entry in entries]
    transcript = "\n---\n".join(part for part in transcript_parts if part).strip()
    if not transcript:
        return Response(status_code=204)

    last_entry_id = entries[-1].get("id")
    if not isinstance(last_entry_id, str) or not last_entry_id:
        return Response(status_code=204)

    index_response = await proxy.index_conversations(
        [
            {
                "conversationId": conversation_id,
                "entryId": last_entry_id,
                "indexedContent": transcript,
            }
        ]
    )
    if index_response.status_code in (200, 201):
        return Response(status_code=201)
    return to_fastapi_response(index_response)


@app.get("/v1/ownership-transfers")
async def list_ownership_transfers(request: Request):
    response = await proxy.list_ownership_transfers(
        role=request.query_params.get("role"),
        after_cursor=request.query_params.get("afterCursor"),
        limit=parse_optional_int(request.query_params.get("limit")),
    )
    return to_fastapi_response(response)


@app.post("/v1/ownership-transfers")
async def create_ownership_transfer(request: Request):
    payload = await request.json()
    if not isinstance(payload, dict):
        raise HTTPException(400, "Invalid request body")
    response = await proxy.create_ownership_transfer(payload)
    return to_fastapi_response(response)


@app.get("/v1/ownership-transfers/{transfer_id}")
async def get_ownership_transfer(transfer_id: str):
    response = await proxy.get_ownership_transfer(transfer_id)
    return to_fastapi_response(response)


@app.delete("/v1/ownership-transfers/{transfer_id}")
async def delete_ownership_transfer(transfer_id: str):
    response = await proxy.delete_ownership_transfer(transfer_id)
    return to_fastapi_response(response)


@app.post("/v1/ownership-transfers/{transfer_id}/accept")
async def accept_ownership_transfer(transfer_id: str):
    response = await proxy.accept_ownership_transfer(transfer_id)
    return to_fastapi_response(response)


@app.post("/v1/attachments")
async def create_attachment(
    request: Request,
    file: UploadFile | None = File(default=None),
    expiresIn: str | None = Query(default=None),
):
    content_type = request.headers.get("content-type", "")
    if "multipart/form-data" in content_type:
        if file is None:
            raise HTTPException(400, "file is required")
        file_bytes = await file.read()
        response = await proxy.upload_attachment(
            file_name=file.filename or "upload",
            file_bytes=file_bytes,
            content_type=file.content_type or "application/octet-stream",
            expires_in=expiresIn,
        )
        return to_fastapi_response(response)

    payload = await request.json()
    if not isinstance(payload, dict):
        raise HTTPException(400, "Invalid request body")
    response = await proxy.create_attachment_from_url(payload)
    return to_fastapi_response(response)


@app.get("/v1/attachments/{attachment_id}")
async def get_attachment(attachment_id: str):
    response = await proxy.get_attachment(attachment_id)
    return to_fastapi_response(response)


@app.get("/v1/attachments/{attachment_id}/download-url")
async def get_attachment_download_url(attachment_id: str):
    response = await proxy.get_attachment_download_url(attachment_id)
    return to_fastapi_response(response)


@app.delete("/v1/attachments/{attachment_id}")
async def delete_attachment(attachment_id: str):
    response = await proxy.delete_attachment(attachment_id)
    return to_fastapi_response(response)


@app.get("/v1/attachments/download/{token}/{filename}")
async def download_attachment_by_token(token: str, filename: str):
    response = await proxy.download_attachment_by_token(token, filename)
    return to_fastapi_response(response)


@app.get("/v1/me")
async def get_current_user(request: Request) -> dict[str, str]:
    claims = parse_jwt_claims(request.headers.get("Authorization"))

    user_id = claims.get("preferred_username")
    if not isinstance(user_id, str) or not user_id:
        fallback = claims.get("sub")
        user_id = fallback if isinstance(fallback, str) and fallback else "anonymous"

    result = {"userId": user_id}

    name = claims.get("name")
    if isinstance(name, str) and name:
        result["name"] = name

    email = claims.get("email")
    if isinstance(email, str) and email:
        result["email"] = email

    return result


@app.get("/config.json")
async def config() -> dict[str, str]:
    return {
        "keycloakUrl": os.getenv("KEYCLOAK_FRONTEND_URL", "http://localhost:8081"),
        "keycloakRealm": os.getenv("KEYCLOAK_REALM", "memory-service"),
        "keycloakClientId": os.getenv("KEYCLOAK_CLIENT_ID", "frontend"),
    }


@app.get("/", include_in_schema=False)
async def serve_frontend_root() -> FileResponse:
    if not _frontend_index.is_file():
        raise HTTPException(
            404,
            (
                f"Frontend bundle not found at {_frontend_index}. "
                "Build frontends/chat-frontend first."
            ),
        )
    return FileResponse(_frontend_index)


@app.get("/{full_path:path}", include_in_schema=False)
async def serve_frontend_path(full_path: str) -> FileResponse:
    if not _frontend_index.is_file():
        raise HTTPException(
            404,
            (
                f"Frontend bundle not found at {_frontend_index}. "
                "Build frontends/chat-frontend first."
            ),
        )

    requested = (_frontend_dir / full_path).resolve()
    if not requested.is_relative_to(_frontend_dir):
        raise HTTPException(404, "Not found")

    if requested.is_file():
        return FileResponse(requested)

    # SPA fallback for client-side routes.
    return FileResponse(_frontend_index)


if __name__ == "__main__":
    import uvicorn

    host = os.getenv("HOST", "0.0.0.0")
    port = int(os.getenv("PORT", "8080"))
    uvicorn.run("app:app", host=host, port=port)
