from __future__ import annotations

import os

from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import PlainTextResponse
from langchain_openai import ChatOpenAI
from langgraph.graph import START, StateGraph
from langgraph.graph.message import MessagesState


openai_base_url = os.getenv("OPENAI_BASE_URL")
if openai_base_url and not openai_base_url.rstrip("/").endswith("/v1"):
    openai_base_url = openai_base_url.rstrip("/") + "/v1"

model = ChatOpenAI(
    model=os.getenv("OPENAI_MODEL", "gpt-4o"),
    openai_api_base=openai_base_url,
    api_key=os.getenv("OPENAI_API_KEY", "not-needed-for-tests"),
)


def call_model(state: MessagesState) -> dict:
    messages = [{"role": "system", "content": "You are a helpful assistant."}] + list(state["messages"])
    response = model.invoke(messages)
    return {"messages": [response]}


builder = StateGraph(MessagesState)
builder.add_node("call_model", call_model)
builder.add_edge(START, "call_model")
graph = builder.compile()

app = FastAPI(title="LangGraph Chatbot")


@app.post("/chat")
async def chat(request: Request) -> PlainTextResponse:
    user_message = (await request.body()).decode("utf-8").strip()
    if not user_message:
        raise HTTPException(400, "message is required")

    result = await graph.ainvoke(
        {"messages": [{"role": "user", "content": user_message}]}
    )
    message = result["messages"][-1]
    content = getattr(message, "content", "")
    return PlainTextResponse(content if isinstance(content, str) else str(content))
