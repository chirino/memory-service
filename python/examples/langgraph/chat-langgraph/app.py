from __future__ import annotations

import copy
import logging
import os
from pathlib import Path

from fastapi import FastAPI, File, HTTPException, Query, Request, UploadFile
from fastapi.responses import FileResponse, JSONResponse, StreamingResponse
from langchain_openai import ChatOpenAI
from langgraph.graph import START, StateGraph
from langgraph.graph.message import MessagesState
from pydantic import BaseModel
from memory_service_langchain import (
    MemoryServiceCheckpointSaver,
    MemoryServiceHistoryMiddleware,
    MemoryServiceProxy,
    MemoryServiceResponseRecordingManager,
    install_fastapi_authorization_middleware,
    memory_service_scope,
    stream_chunks_as_sse,
    to_fastapi_response,
)

LOG = logging.getLogger("uvicorn.error")


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
    streaming=True,
)

checkpointer = MemoryServiceCheckpointSaver()
history_middleware = MemoryServiceHistoryMiddleware(
    indexed_content_provider=pass_through_indexed_content,
)

async def call_model(state: MessagesState) -> dict[str, list[Any]]:
    messages = [{"role": "system", "content": "You are a helpful assistant."}] + list(
        state["messages"]
    )
    response = await model.ainvoke(messages)
    return {"messages": [response]}


builder = StateGraph(MessagesState)
builder.add_node("call_model", call_model)
builder.add_edge(START, "call_model")
graph = builder.compile(checkpointer=checkpointer)

app = FastAPI(title="Python LangGraph Chat Example")


@app.get("/ready")
async def ready() -> dict[str, str]:
    return {"status": "ok"}
install_fastapi_authorization_middleware(app, validate_jwt=False)
proxy = MemoryServiceProxy()
recording_manager = MemoryServiceResponseRecordingManager()
LOG.info("chat response memory-service integration enabled")

def find_repo_root(start: Path) -> Path:
    for candidate in (start, *start.parents):
        if (candidate / "Taskfile.yml").is_file():
            return candidate
    return start.parent


_repo_root = find_repo_root(Path(__file__).resolve())
_default_frontend_dir = _repo_root / "frontends" / "chat-frontend" / "dist"
_frontend_dir = Path(
    os.getenv("CHAT_FRONTEND_DIR", str(_default_frontend_dir))
).resolve()
_frontend_index = _frontend_dir / "index.html"


@app.post("/chat/{conversation_id}")
async def chat(
    conversation_id: str,
    body: MessageRequest,
) -> StreamingResponse:
    message = body.message.strip()
    if not message:
        raise HTTPException(400, "Message is required")

    if bool(body.forkedAtConversationId) != bool(body.forkedAtEntryId):
        raise HTTPException(
            400,
            "forkedAtConversationId and forkedAtEntryId must be provided together",
        )

    stream_mode = "events"

    async def source():
        with memory_service_scope(
            conversation_id,
            body.forkedAtConversationId,
            body.forkedAtEntryId,
            stream_mode,
        ):
            history_middleware.append_user_history(
                conversation_id,
                message,
                forked_at_conversation_id=body.forkedAtConversationId,
                forked_at_entry_id=body.forkedAtEntryId,
            )

            async def chunk_stream():
                async for chunk, _metadata in graph.astream(
                    {"messages": [{"role": "user", "content": message}]},
                    config={"configurable": {"thread_id": conversation_id}},
                    stream_mode="messages",
                ):
                    yield chunk

            async for event in stream_chunks_as_sse(
                conversation_id=conversation_id,
                stream_mode=stream_mode,
                chunk_stream=chunk_stream(),
                append_ai_history=history_middleware.append_ai_history,
                log=LOG,
                source="graph",
            ):
                yield event

    stream = recording_manager.stream_from_source(conversation_id, source())
    return StreamingResponse(stream, media_type="text/event-stream")


@app.post("/v1/conversations/resume-check")
async def resume_check(conversation_ids: list[str]) -> JSONResponse:
    return JSONResponse(recording_manager.check(conversation_ids), status_code=200)


@app.get("/v1/conversations/{conversation_id}/resume")
async def resume_response(conversation_id: str) -> StreamingResponse:
    try:
        stream = recording_manager.replay_sse(
            conversation_id,
            stream_mode="events",
        )
    except ValueError as exc:
        raise HTTPException(400, "invalid conversation id") from exc
    except KeyError as exc:
        raise HTTPException(404, "no in-progress response") from exc
    return StreamingResponse(stream, media_type="text/event-stream")


@app.post("/v1/conversations/{conversation_id}/cancel")
async def cancel_response(conversation_id: str):
    LOG.info("chat cancel request conversation_id=%s", conversation_id)
    recording_manager.cancel(conversation_id)
    response = await proxy.cancel_response(conversation_id)
    LOG.info(
        "chat cancel proxied conversation_id=%s status=%s",
        conversation_id,
        response.status_code,
    )
    return to_fastapi_response(response)


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
    file: UploadFile = File(...),
    expiresIn: str | None = Query(default=None),
):
    file_bytes = await file.read()
    response = await proxy.upload_attachment(
        file_name=file.filename or "upload",
        file_bytes=file_bytes,
        content_type=file.content_type or "application/octet-stream",
        expires_in=expiresIn,
    )
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

    log_config = copy.deepcopy(uvicorn.config.LOGGING_CONFIG)
    log_config["formatters"]["default"]["fmt"] = (
        "%(asctime)s.%(msecs)03d %(levelprefix)s %(message)s"
    )
    log_config["formatters"]["default"]["datefmt"] = "%Y-%m-%d %H:%M:%S"
    log_config["formatters"]["access"]["fmt"] = (
        "%(asctime)s.%(msecs)03d %(levelprefix)s %(client_addr)s - "
        '"%(request_line)s" %(status_code)s'
    )
    log_config["formatters"]["access"]["datefmt"] = "%Y-%m-%d %H:%M:%S"

    host = os.getenv("HOST", "0.0.0.0")
    port = int(os.getenv("PORT", "3000"))
    log_level = os.getenv("LOG_LEVEL", "info").lower()
    uvicorn.run(
        "app:app",
        host=host,
        port=port,
        log_level=log_level,
        log_config=log_config,
    )
