from __future__ import annotations

import os
from typing import Any

from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import PlainTextResponse
from langchain_openai import ChatOpenAI
from langgraph.graph import START, StateGraph
from langgraph.graph.message import MessagesState
from memory_service_langchain import (
    MemoryServiceCheckpointSaver,
    MemoryServiceHistoryMiddleware,
    MemoryServiceProxy,
    install_fastapi_authorization_middleware,
    memory_service_scope,
    to_fastapi_response,
)


async def parse_optional_json(request: Request) -> Any | None:
    body = (await request.body()).decode("utf-8").strip()
    if not body:
        return None
    return await request.json()


openai_base_url = os.getenv("OPENAI_BASE_URL")
if openai_base_url and not openai_base_url.rstrip("/").endswith("/v1"):
    openai_base_url = openai_base_url.rstrip("/") + "/v1"

model = ChatOpenAI(
    model=os.getenv("OPENAI_MODEL", "gpt-4o"),
    openai_api_base=openai_base_url,
    api_key=os.getenv("OPENAI_API_KEY", "not-needed-for-tests"),
)

checkpointer = MemoryServiceCheckpointSaver()
history_middleware = MemoryServiceHistoryMiddleware()


def call_model(state: MessagesState) -> dict:
    messages = [{"role": "system", "content": "You are a helpful assistant."}] + list(state["messages"])
    user_text = state["messages"][-1].content
    response = history_middleware.wrap_model_call(user_text, lambda: model.invoke(messages))
    return {"messages": [response]}


builder = StateGraph(MessagesState)
builder.add_node("call_model", call_model)
builder.add_edge(START, "call_model")
graph = builder.compile(checkpointer=checkpointer)

app = FastAPI(title="LangGraph Chatbot with Sharing")
install_fastapi_authorization_middleware(app)
proxy = MemoryServiceProxy()


@app.post("/chat/{conversation_id}")
async def chat(conversation_id: str, request: Request) -> PlainTextResponse:
    user_message = (await request.body()).decode("utf-8").strip()
    if not user_message:
        raise HTTPException(400, "message is required")

    with memory_service_scope(conversation_id):
        result = await graph.ainvoke(
            {"messages": [{"role": "user", "content": user_message}]},
            config={"configurable": {"thread_id": conversation_id}},
        )

    message = result["messages"][-1]
    content = getattr(message, "content", "")
    return PlainTextResponse(content if isinstance(content, str) else str(content))


@app.get("/v1/conversations/{conversation_id}")
async def get_conversation(conversation_id: str):
    response = await proxy.get_conversation(conversation_id)
    return to_fastapi_response(response)


@app.get("/v1/conversations/{conversation_id}/entries")
async def get_entries(conversation_id: str, request: Request):
    response = await proxy.list_conversation_entries(
        conversation_id,
        after_cursor=request.query_params.get("afterCursor"),
        limit=int(limit) if (limit := request.query_params.get("limit")) is not None else None,
        channel="history",
    )
    return to_fastapi_response(response)


@app.get("/v1/conversations")
async def list_conversations(request: Request):
    response = await proxy.list_conversations(
        mode=request.query_params.get("mode"),
        after_cursor=request.query_params.get("afterCursor"),
        limit=int(limit) if (limit := request.query_params.get("limit")) is not None else None,
        query=request.query_params.get("query"),
    )
    return to_fastapi_response(response)


@app.get("/v1/conversations/{conversation_id}/memberships")
async def list_memberships(conversation_id: str):
    response = await proxy.list_memberships(conversation_id)
    return to_fastapi_response(response)


@app.post("/v1/conversations/{conversation_id}/memberships")
async def create_membership(conversation_id: str, request: Request):
    response = await proxy.create_membership(conversation_id, await parse_optional_json(request) or {})
    return to_fastapi_response(response)


@app.patch("/v1/conversations/{conversation_id}/memberships/{user_id}")
async def update_membership(conversation_id: str, user_id: str, request: Request):
    response = await proxy.update_membership(
        conversation_id,
        user_id,
        await parse_optional_json(request) or {},
    )
    return to_fastapi_response(response)


@app.delete("/v1/conversations/{conversation_id}/memberships/{user_id}")
async def delete_membership(conversation_id: str, user_id: str):
    response = await proxy.delete_membership(conversation_id, user_id)
    return to_fastapi_response(response)


@app.get("/v1/ownership-transfers")
async def list_ownership_transfers(request: Request):
    response = await proxy.list_ownership_transfers(
        role=request.query_params.get("role"),
        after_cursor=request.query_params.get("afterCursor"),
        limit=int(limit) if (limit := request.query_params.get("limit")) is not None else None,
    )
    return to_fastapi_response(response)


@app.post("/v1/ownership-transfers")
async def create_ownership_transfer(request: Request):
    response = await proxy.create_ownership_transfer(await parse_optional_json(request) or {})
    return to_fastapi_response(response)


@app.post("/v1/ownership-transfers/{transfer_id}/accept")
async def accept_ownership_transfer(transfer_id: str):
    response = await proxy.accept_ownership_transfer(transfer_id)
    return to_fastapi_response(response)


@app.delete("/v1/ownership-transfers/{transfer_id}")
async def delete_ownership_transfer(transfer_id: str):
    response = await proxy.delete_ownership_transfer(transfer_id)
    return to_fastapi_response(response)
