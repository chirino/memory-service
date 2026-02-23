from __future__ import annotations

import os
from typing import Any

from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import PlainTextResponse
from langchain.agents import create_agent
from langchain_openai import ChatOpenAI
from memory_service_langchain import (
    MemoryServiceCheckpointSaver,
    MemoryServiceHistoryMiddleware,
    MemoryServiceProxy,
    install_fastapi_authorization_middleware,
    memory_service_scope,
    to_fastapi_response,
)


def extract_assistant_text(result: Any) -> str:
    if not isinstance(result, dict):
        return str(result)

    messages = result.get("messages")
    if not messages:
        return str(result)

    message = messages[-1]

    text = getattr(message, "text", None)
    if isinstance(text, str) and text:
        return text

    content = getattr(message, "content", "")
    if isinstance(content, str):
        return content

    return str(content)


async def parse_optional_json(request: Request) -> Any | None:
    body = (await request.body()).decode("utf-8").strip()
    if not body:
        return None
    return await request.json()


openai_base_url = os.getenv("OPENAI_BASE_URL")
if openai_base_url and not openai_base_url.rstrip("/").endswith("/v1"):
    openai_base_url = openai_base_url.rstrip("/") + "/v1"
if openai_base_url:
    os.environ.setdefault("OPENAI_API_BASE", openai_base_url)

model = ChatOpenAI(
    model=os.getenv("OPENAI_MODEL", "gpt-4o"),
    openai_api_base=openai_base_url,
    api_key=os.getenv("OPENAI_API_KEY", "not-needed-for-tests"),
)

checkpointer = MemoryServiceCheckpointSaver()
history_middleware = MemoryServiceHistoryMiddleware()

agent = create_agent(
    model=model,
    tools=[],
    checkpointer=checkpointer,
    middleware=[history_middleware],
    system_prompt="You are a Python memory-service demo agent.",
)

app = FastAPI(title="Python LangChain Agent With Sharing")
install_fastapi_authorization_middleware(app)
proxy = MemoryServiceProxy()


@app.post("/chat/{conversation_id}")
async def chat(conversation_id: str, request: Request) -> PlainTextResponse:
    user_message = (await request.body()).decode("utf-8").strip()
    if not user_message:
        raise HTTPException(400, "message is required")

    with memory_service_scope(conversation_id):
        result = agent.invoke(
            {"messages": [{"role": "user", "content": user_message}]},
            {"configurable": {"thread_id": conversation_id}},
        )

    response_text = extract_assistant_text(result)
    return PlainTextResponse(response_text)


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
