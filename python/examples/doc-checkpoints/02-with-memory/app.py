from __future__ import annotations

import os
from typing import Any

from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import PlainTextResponse
from langchain.agents import create_agent
from langchain_openai import ChatOpenAI
from memory_service_langchain import MemoryServiceCheckpointSaver, install_fastapi_authorization_middleware


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

agent = create_agent(
    model=model,
    tools=[],
    checkpointer=checkpointer,
    system_prompt="You are a Python memory-service demo agent.",
)

app = FastAPI(title="Python LangChain Agent With Conversation Memory")
install_fastapi_authorization_middleware(app)


@app.post("/chat/{conversation_id}")
async def chat(conversation_id: str, request: Request) -> PlainTextResponse:
    user_message = (await request.body()).decode("utf-8").strip()
    if not user_message:
        raise HTTPException(400, "message is required")

    result = agent.invoke(
        {"messages": [{"role": "user", "content": user_message}]},
        {"configurable": {"thread_id": conversation_id}},
    )
    return PlainTextResponse(extract_assistant_text(result))
