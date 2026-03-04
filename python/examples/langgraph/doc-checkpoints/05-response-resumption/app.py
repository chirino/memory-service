from __future__ import annotations

import json
import os
from typing import Any

from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import JSONResponse, StreamingResponse
from langchain_openai import ChatOpenAI
from langgraph.graph import START, StateGraph
from langgraph.graph.message import MessagesState
from memory_service_langchain import (
    MemoryServiceCheckpointSaver,
    MemoryServiceHistoryMiddleware,
    MemoryServiceProxy,
    MemoryServiceResponseRecordingManager,
    install_fastapi_authorization_middleware,
    memory_service_scope,
    to_fastapi_response,
)


def parse_optional_int(value: str | None) -> int | None:
    if value is None or value == "":
        return None
    try:
        return int(value)
    except ValueError as exc:
        raise HTTPException(400, f"invalid integer value: {value}") from exc


def to_sse_chunk(payload: Any) -> str:
    return f"data: {json.dumps(payload, separators=(',', ':'))}\n\n"


def extract_text_chunks(message_chunk: Any) -> list[str]:
    tokens: list[str] = []
    blocks = getattr(message_chunk, "content_blocks", None)
    if isinstance(blocks, list):
        for block in blocks:
            if not isinstance(block, dict):
                continue
            for key in ("text", "content", "value"):
                value = block.get(key)
                if isinstance(value, str) and value:
                    tokens.append(value)
                    break
    if tokens:
        return tokens

    content = getattr(message_chunk, "content", None)
    if isinstance(content, str) and content:
        return [content]

    if isinstance(content, list):
        for item in content:
            if isinstance(item, str) and item:
                tokens.append(item)
                continue
            if not isinstance(item, dict):
                continue
            for key in ("text", "content", "value"):
                value = item.get(key)
                if isinstance(value, str) and value:
                    tokens.append(value)
                    break
            delta = item.get("delta")
            if isinstance(delta, dict):
                for key in ("text", "content", "value"):
                    value = delta.get(key)
                    if isinstance(value, str) and value:
                        tokens.append(value)
                        break
    return tokens


SSE_HEADERS = {
    "Cache-Control": "no-cache",
    "Connection": "keep-alive",
    "X-Accel-Buffering": "no",
}


openai_base_url = os.getenv("OPENAI_BASE_URL")
if openai_base_url and not openai_base_url.rstrip("/").endswith("/v1"):
    openai_base_url = openai_base_url.rstrip("/") + "/v1"
if openai_base_url:
    os.environ.setdefault("OPENAI_API_BASE", openai_base_url)

model = ChatOpenAI(
    model=os.getenv("OPENAI_MODEL", "gpt-4o"),
    openai_api_base=openai_base_url,
    api_key=os.getenv("OPENAI_API_KEY", "not-needed-for-tests"),
    streaming=True,
)

checkpointer = MemoryServiceCheckpointSaver()
history_middleware = MemoryServiceHistoryMiddleware()


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

app = FastAPI(title="LangGraph Chatbot with Response Resumption")
install_fastapi_authorization_middleware(app)
proxy = MemoryServiceProxy()
recording_manager = MemoryServiceResponseRecordingManager()


@app.post("/chat/{conversation_id}")
async def chat(conversation_id: str, request: Request) -> StreamingResponse:
    user_message = (await request.body()).decode("utf-8").strip()
    if not user_message:
        raise HTTPException(400, "message is required")

    async def source():
        response_text_parts: list[str] = []
        partial_history_persisted = False

        def persist_partial_history() -> None:
            nonlocal partial_history_persisted
            if partial_history_persisted:
                return
            partial_text = "".join(response_text_parts).strip()
            if not partial_text:
                return
            history_middleware.append_ai_history(conversation_id, partial_text)
            partial_history_persisted = True

        with memory_service_scope(conversation_id):
            history_middleware.append_user_history(conversation_id, user_message)
            try:
                async for chunk, _metadata in graph.astream(
                    {"messages": [{"role": "user", "content": user_message}]},
                    config={"configurable": {"thread_id": conversation_id}},
                    stream_mode="messages",
                ):
                    for token in extract_text_chunks(chunk):
                        response_text_parts.append(token)
                        yield to_sse_chunk(
                            {"eventType": "PartialResponse", "chunk": token}
                        )
            except BaseException:
                persist_partial_history()
                raise

        if response_text_parts and not partial_history_persisted:
            history_middleware.append_ai_history(
                conversation_id,
                "".join(response_text_parts),
            )
        yield to_sse_chunk(
            {"eventType": "ChatCompleted", "text": "".join(response_text_parts)}
        )

    stream = recording_manager.stream_from_source(conversation_id, source())
    return StreamingResponse(stream, media_type="text/event-stream", headers=SSE_HEADERS)


@app.get("/v1/conversations/{conversation_id}")
async def get_conversation(conversation_id: str):
    response = await proxy.get_conversation(conversation_id)
    return to_fastapi_response(response)


@app.get("/v1/conversations/{conversation_id}/entries")
async def get_entries(conversation_id: str, request: Request):
    response = await proxy.list_conversation_entries(
        conversation_id,
        after_cursor=request.query_params.get("afterCursor"),
        limit=parse_optional_int(request.query_params.get("limit")),
        channel="history",
        forks="all",
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


@app.get("/v1/conversations/{conversation_id}/forks")
async def list_forks(conversation_id: str):
    response = await proxy.list_conversation_forks(conversation_id)
    return to_fastapi_response(response)


@app.post("/v1/conversations/resume-check")
async def resume_check(conversation_ids: list[str]) -> JSONResponse:
    return JSONResponse(recording_manager.check(conversation_ids), status_code=200)


@app.get("/v1/conversations/{conversation_id}/resume")
async def resume_response(conversation_id: str):
    try:
        stream = recording_manager.replay_sse(conversation_id, stream_mode="events")
    except ValueError as exc:
        raise HTTPException(400, "invalid conversation id") from exc
    except KeyError as exc:
        raise HTTPException(404, "no in-progress response")
    return StreamingResponse(stream, media_type="text/event-stream", headers=SSE_HEADERS)


@app.post("/v1/conversations/{conversation_id}/cancel")
async def cancel_response(conversation_id: str):
    recording_manager.cancel(conversation_id)
    response = await proxy.cancel_response(conversation_id)
    return to_fastapi_response(response)
