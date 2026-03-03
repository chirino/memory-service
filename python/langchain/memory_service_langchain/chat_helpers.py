from __future__ import annotations

import asyncio
import json
import logging
import time
from typing import Any, AsyncIterator, Callable


def _string_from_nested(value: Any) -> str:
    if isinstance(value, str):
        return value
    if isinstance(value, dict):
        for key in ("text", "value", "delta", "content"):
            nested = value.get(key)
            if isinstance(nested, str) and nested:
                return nested
            if isinstance(nested, dict):
                nested_text = _string_from_nested(nested)
                if nested_text:
                    return nested_text
    return ""


def _collect_text(value: Any) -> str:
    if isinstance(value, str):
        return value

    if isinstance(value, list):
        parts: list[str] = []
        for item in value:
            extracted = _collect_text(item)
            if extracted:
                parts.append(extracted)
        return "".join(parts)

    if isinstance(value, dict):
        # Common streaming chunk shapes across providers.
        for key in ("text", "delta", "value", "content"):
            extracted = _string_from_nested(value.get(key))
            if extracted:
                return extracted

        parts: list[str] = []
        for key in ("content_blocks", "blocks", "parts", "output"):
            nested = value.get(key)
            if nested is None:
                continue
            extracted = _collect_text(nested)
            if extracted:
                parts.append(extracted)
        return "".join(parts)

    return ""


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
    tokens = extract_stream_tokens(event)
    if tokens:
        return "".join(tokens)
    message: Any = event[0] if isinstance(event, tuple) and event else event
    if isinstance(message, dict):
        from_message = _collect_text(message)
        if from_message:
            return from_message
    return _collect_text(message)


def to_sse_chunk(payload: Any) -> str:
    return f"data: {json.dumps(payload, separators=(',', ':'))}\n\n"


def summarize_stream_event(event: Any) -> str:
    message: Any = event[0] if isinstance(event, tuple) and event else event
    message_type = type(message).__name__
    text_attr = getattr(message, "text", None)
    if isinstance(text_attr, str) and text_attr:
        return f"{message_type} text_len={len(text_attr)}"
    content = getattr(message, "content", None)
    if isinstance(content, str):
        return f"{message_type} content_str_len={len(content)}"
    if isinstance(content, list):
        sample_types = []
        for item in content[:3]:
            if isinstance(item, dict):
                sample_types.append(str(item.get("type")))
            else:
                sample_types.append(type(item).__name__)
        return (
            f"{message_type} content_list_len={len(content)} "
            f"content_types={sample_types}"
        )
    return f"{message_type} content_type={type(content).__name__}"


def chunk_to_json_log(chunk: Any) -> str:
    for attr in ("toJSON", "to_json"):
        method = getattr(chunk, attr, None)
        if not callable(method):
            continue
        try:
            return json.dumps(method(), separators=(",", ":"), default=str)
        except Exception as exc:
            return f"<{attr}() failed: {exc}>"

    dump_json = getattr(chunk, "model_dump_json", None)
    if callable(dump_json):
        try:
            dumped = dump_json()
            if isinstance(dumped, str):
                return dumped
        except Exception:
            pass

    dump = getattr(chunk, "model_dump", None)
    if callable(dump):
        try:
            return json.dumps(dump(), separators=(",", ":"), default=str)
        except Exception:
            pass

    return summarize_stream_event(chunk)


def extract_stream_tokens(event: Any) -> list[str]:
    message: Any = event[0] if isinstance(event, tuple) and event else event
    tokens: list[str] = []
    blocks = getattr(message, "content_blocks", None)
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

    content = getattr(message, "content", None)
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


async def stream_chunks_as_sse(
    *,
    conversation_id: str,
    stream_mode: str,
    chunk_stream: AsyncIterator[Any],
    append_ai_history: Callable[[str, str], None],
    log: logging.Logger,
    source: str,
    log_prefix: str = "chat stream",
) -> AsyncIterator[str]:
    started_at = time.perf_counter()
    event_count = 0
    emitted_count = 0
    first_emit_elapsed_ms: float | None = None
    empty_event_logs = 0
    response_text_parts: list[str] = []
    partial_history_persisted = False

    def persist_partial_history(reason: str) -> None:
        nonlocal partial_history_persisted
        if partial_history_persisted:
            return
        partial_text = "".join(response_text_parts).strip()
        if not partial_text:
            return
        append_ai_history(conversation_id, partial_text)
        partial_history_persisted = True
        log.info(
            "%s persisted partial AI history conversation_id=%s reason=%s chars=%d",
            log_prefix,
            conversation_id,
            reason,
            len(partial_text),
        )

    log.info(
        "%s start conversation_id=%s stream_mode=%s",
        log_prefix,
        conversation_id,
        stream_mode,
    )
    try:
        async for event in chunk_stream:
            event_count += 1
            tokens = extract_stream_tokens(event)
            if not tokens:
                if empty_event_logs < 3:
                    empty_event_logs += 1
                    log.info(
                        "%s empty token event conversation_id=%s event_index=%d chunk=%s",
                        log_prefix,
                        conversation_id,
                        event_count,
                        chunk_to_json_log(event),
                    )
                continue

            for token in tokens:
                emitted_count += 1
                if first_emit_elapsed_ms is None:
                    first_emit_elapsed_ms = (time.perf_counter() - started_at) * 1000.0
                    log.info(
                        "%s first token conversation_id=%s event_index=%d latency_ms=%.1f token_len=%d source=%s",
                        log_prefix,
                        conversation_id,
                        event_count,
                        first_emit_elapsed_ms,
                        len(token),
                        source,
                    )
                response_text_parts.append(token)
                if stream_mode == "events":
                    yield to_sse_chunk({"eventType": "PartialResponse", "chunk": token})
                else:
                    yield to_sse_chunk({"token": token})
    except asyncio.CancelledError:
        persist_partial_history("cancelled")
        log.info(
            "%s cancelled conversation_id=%s after events=%d emitted_tokens=%d",
            log_prefix,
            conversation_id,
            event_count,
            emitted_count,
        )
        raise
    except Exception as exc:
        persist_partial_history("failed")
        log.warning(
            "%s failed conversation_id=%s after events=%d emitted_tokens=%d: %s",
            log_prefix,
            conversation_id,
            event_count,
            emitted_count,
            exc,
        )
        return

    if response_text_parts and not partial_history_persisted:
        append_ai_history(conversation_id, "".join(response_text_parts))

    elapsed_ms = (time.perf_counter() - started_at) * 1000.0
    if stream_mode == "events":
        yield to_sse_chunk(
            {
                "eventType": "ChatCompleted",
                "text": "".join(response_text_parts),
            }
        )
    log.info(
        "%s end conversation_id=%s stream_mode=%s events=%d emitted_tokens=%d elapsed_ms=%.1f",
        log_prefix,
        conversation_id,
        stream_mode,
        event_count,
        emitted_count,
        elapsed_ms,
    )
