from __future__ import annotations

from typing import Any


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


def extract_stream_text(event: Any) -> str:
    message: Any = event
    if isinstance(event, tuple) and event:
        message = event[0]

    text_attr = getattr(message, "text", None)
    if isinstance(text_attr, str) and text_attr:
        return text_attr

    content = getattr(message, "content", None)
    if isinstance(content, str):
        return content

    if isinstance(content, list):
        parts: list[str] = []
        for item in content:
            if isinstance(item, str):
                parts.append(item)
                continue
            if not isinstance(item, dict):
                continue
            value = item.get("text")
            if isinstance(value, str) and value:
                parts.append(value)
        return "".join(parts)

    return ""
