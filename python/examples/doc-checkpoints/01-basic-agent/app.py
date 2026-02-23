from __future__ import annotations

import os
from typing import Any

from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import PlainTextResponse
from langchain.agents import create_agent
from langchain_openai import ChatOpenAI


def extract_assistant_text(result: Any) -> str:
    if not isinstance(result, dict):
        return str(result)

    messages = result.get("messages")
    if not messages:
        return str(result)

    # LangChain quickstart pattern: read the final assistant message directly.
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

agent = create_agent(
    model=model,
    tools=[],
    system_prompt="You are a Python memory-service demo agent.",
)

app = FastAPI(title="Python LangChain Quickstart Agent")


@app.post("/chat")
async def chat(request: Request) -> PlainTextResponse:
    user_message = (await request.body()).decode("utf-8").strip()
    if not user_message:
        raise HTTPException(400, "message is required")

    result = agent.invoke({"messages": [{"role": "user", "content": user_message}]})
    return PlainTextResponse(extract_assistant_text(result))
