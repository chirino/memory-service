from __future__ import annotations

import os

from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import PlainTextResponse
from langchain.agents import create_agent
from langchain_openai import ChatOpenAI
from memory_service_langchain import (
    MemoryServiceCheckpointSaver,
    MemoryServiceHistoryMiddleware,
    MemoryServiceProxy,
    extract_assistant_text,
    install_fastapi_authorization_middleware,
    memory_service_scope,
    to_fastapi_response,
)

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

stateful_agent = create_agent(
    model=model,
    tools=[],
    checkpointer=checkpointer,
    middleware=[history_middleware],
    system_prompt="You are a Python memory-service demo agent.",
)

app = FastAPI(title="Python LangChain Agent With Conversation Forking")
install_fastapi_authorization_middleware(app)
proxy = MemoryServiceProxy()
agent = stateful_agent


@app.post("/chat/{conversation_id}")
async def chat(conversation_id: str, request: Request) -> PlainTextResponse:
    user_message = (await request.body()).decode("utf-8").strip()
    if not user_message:
        raise HTTPException(400, "message is required")
    forked_at_conversation_id = request.query_params.get("forkedAtConversationId")
    forked_at_entry_id = request.query_params.get("forkedAtEntryId")
    if bool(forked_at_conversation_id) != bool(forked_at_entry_id):
        raise HTTPException(
            400,
            "forkedAtConversationId and forkedAtEntryId must be provided together",
        )

    with memory_service_scope(
        conversation_id,
        forked_at_conversation_id,
        forked_at_entry_id,
    ):
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


@app.get("/v1/conversations/{conversation_id}/forks")
async def list_conversation_forks(conversation_id: str, request: Request):
    response = await proxy.list_conversation_forks(
        conversation_id,
        after_cursor=request.query_params.get("afterCursor"),
        limit=int(limit) if (limit := request.query_params.get("limit")) is not None else None,
    )
    return to_fastapi_response(response)
