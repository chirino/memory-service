from __future__ import annotations

import os
import uuid

from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import PlainTextResponse
from langchain_core.runnables import RunnableConfig
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
from memory_service_langgraph import AsyncMemoryServiceStore


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


async def call_model(state: MessagesState, config: RunnableConfig) -> dict:
    configurable = config.get("configurable") or {}
    user_id = configurable.get("user_id", "anonymous")
    token = configurable.get("token", "")
    namespace = ("user", user_id, "memories")

    async with AsyncMemoryServiceStore(token=token) as store:
        # Recall recent memories for context
        memories = await store.asearch(namespace, limit=10)
        memory_context = ""
        if memories:
            facts = "\n".join(
                f"- {m.value.get('text', '')}" for m in memories if m.value.get("text")
            )
            if facts:
                memory_context = f"\n\nWhat you remember about this user:\n{facts}"

        messages = [
            {
                "role": "system",
                "content": (
                    "You are a helpful assistant that remembers information about users."
                    + memory_context
                ),
            }
        ] + list(state["messages"])

        user_text = state["messages"][-1].content
        response = history_middleware.wrap_model_call(user_text, lambda: model.invoke(messages))

        # Save the user's message as a new memory
        await store.aput(namespace, str(uuid.uuid4()), {"text": user_text})

    return {"messages": [response]}


builder = StateGraph(MessagesState)
builder.add_node("call_model", call_model)
builder.add_edge(START, "call_model")
graph = builder.compile(checkpointer=checkpointer)

app = FastAPI(title="LangGraph Chatbot with Episodic Memories")
install_fastapi_authorization_middleware(app)
proxy = MemoryServiceProxy()


@app.post("/chat/{user_id}/{conversation_id}")
async def chat(user_id: str, conversation_id: str, request: Request) -> PlainTextResponse:
    user_message = (await request.body()).decode("utf-8").strip()
    if not user_message:
        raise HTTPException(400, "message is required")

    auth = request.headers.get("Authorization", "")
    token = auth.removeprefix("Bearer ").strip()

    with memory_service_scope(conversation_id):
        result = await graph.ainvoke(
            {"messages": [{"role": "user", "content": user_message}]},
            config={"configurable": {
                "thread_id": conversation_id,
                "user_id": user_id,
                "token": token,
            }},
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
