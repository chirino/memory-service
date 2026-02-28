from __future__ import annotations

import json
import logging
import os
from typing import Any, Callable
from urllib.parse import urlparse

import httpx
from langchain.agents.middleware import AgentMiddleware
from langchain.agents.middleware.types import ModelRequest, ModelResponse
from langchain_core.callbacks import BaseCallbackHandler

from .request_context import (
    get_request_authorization,
    get_request_conversation_id,
    get_request_forked_at_conversation_id,
    get_request_forked_at_entry_id,
)
from .response_recorder import BaseResponseRecorder, create_grpc_response_recorder


LOG = logging.getLogger(__name__)


class _RecorderCallback(BaseCallbackHandler):
    run_inline = True

    def __init__(self, recorder: BaseResponseRecorder, *, record_mode: str):
        self.recorder = recorder
        self.record_mode = record_mode
        self.recorded_chunks = False
        self.token_buffer: list[str] = []

    def on_llm_new_token(self, token: str, **kwargs: Any) -> None:
        del kwargs
        if not token:
            return
        self.recorded_chunks = True
        self.token_buffer.append(token)
        if self.record_mode == "events":
            self.recorder.record(json.dumps({"eventType": "PartialResponseEvent", "chunk": token}) + "\n")
        else:
            self.recorder.record(token)

    def buffered_text(self) -> str:
        if not self.token_buffer:
            return ""
        return "".join(self.token_buffer)


class MemoryServiceHistoryMiddleware(AgentMiddleware):
    """Records USER/AI history and response chunks, similar to the Quarkus interceptor."""

    def __init__(
        self,
        *,
        base_url: str | None = None,
        api_key: str | None = None,
        authorization_getter: Callable[[], str | None] | None = None,
        conversation_id_getter: Callable[[], str | None] | None = None,
        forked_at_conversation_id_getter: Callable[[], str | None] | None = None,
        forked_at_entry_id_getter: Callable[[], str | None] | None = None,
        indexed_content_provider: Callable[[str, str], str | None] | None = None,
        grpc_target: str | None = None,
        grpc_timeout_seconds: float | None = None,
        grpc_record_mode: str | None = None,
        enable_grpc_recording: bool | None = None,
    ):
        super().__init__()
        self.base_url = (base_url or os.getenv("MEMORY_SERVICE_URL", "http://localhost:8082")).rstrip("/")
        self.api_key = api_key or os.getenv("MEMORY_SERVICE_API_KEY", "agent-api-key-1")
        self.authorization_getter = authorization_getter or get_request_authorization
        self.conversation_id_getter = conversation_id_getter or get_request_conversation_id
        self.forked_at_conversation_id_getter = (
            forked_at_conversation_id_getter or get_request_forked_at_conversation_id
        )
        self.forked_at_entry_id_getter = forked_at_entry_id_getter or get_request_forked_at_entry_id
        self.indexed_content_provider = indexed_content_provider
        parsed = urlparse(self.base_url)
        grpc_host = parsed.hostname or "localhost"
        grpc_port = os.getenv("MEMORY_SERVICE_GRPC_PORT", "9000")
        self.grpc_target = grpc_target or os.getenv("MEMORY_SERVICE_GRPC_TARGET", f"{grpc_host}:{grpc_port}")
        self.grpc_timeout_seconds = grpc_timeout_seconds or float(os.getenv("MEMORY_SERVICE_GRPC_TIMEOUT_SECONDS", "30"))
        self.grpc_record_mode = (grpc_record_mode or os.getenv("MEMORY_SERVICE_GRPC_RECORD_MODE", "tokens")).lower()
        if self.grpc_record_mode not in {"tokens", "events"}:
            self.grpc_record_mode = "tokens"
        if enable_grpc_recording is None:
            explicit = os.getenv("MEMORY_SERVICE_GRPC_RECORDING_ENABLED")
            if explicit is None:
                self.enable_grpc_recording = bool(os.getenv("MEMORY_SERVICE_GRPC_TARGET"))
            else:
                self.enable_grpc_recording = explicit.lower() not in {"0", "false", "no"}
        else:
            self.enable_grpc_recording = enable_grpc_recording

    def _authorization(self) -> str | None:
        if not self.authorization_getter:
            return None
        return self.authorization_getter()

    def _headers(self) -> dict[str, str]:
        headers = {"X-API-Key": self.api_key}
        authorization = self._authorization()
        if authorization:
            headers["Authorization"] = authorization
        return headers

    def _extract_text(self, message: Any) -> str | None:
        content: Any
        if isinstance(message, dict):
            content = message.get("content")
        else:
            content = getattr(message, "content", None)

        if isinstance(content, str):
            text = content.strip()
            return text or None

        if isinstance(content, list):
            parts: list[str] = []
            for item in content:
                if isinstance(item, str):
                    parts.append(item)
                    continue
                if not isinstance(item, dict):
                    continue
                if item.get("type") not in ("text", "output_text"):
                    continue
                text = item.get("text")
                if isinstance(text, str) and text:
                    parts.append(text)
            if parts:
                return "".join(parts).strip() or None

        text = getattr(message, "text", None)
        if isinstance(text, str):
            text = text.strip()
            return text or None
        return None

    def _message_type(self, message: Any) -> str | None:
        if isinstance(message, dict):
            role = message.get("type") or message.get("role")
            return role if isinstance(role, str) else None
        role = getattr(message, "type", None)
        return role if isinstance(role, str) else None

    def _last_message_of_type(self, messages: list[Any], accepted: set[str]) -> str | None:
        for message in reversed(messages):
            role = self._message_type(message)
            if role not in accepted:
                continue
            text = self._extract_text(message)
            if text:
                return text
        return None

    def _is_duplicate_conversation_error(self, response: httpx.Response) -> bool:
        if response.status_code < 500:
            return False
        body = response.text.lower()
        return "duplicate key value violates unique constraint" in body and "conversation_groups_pkey" in body

    def _forked_at_conversation_id(self) -> str | None:
        if self.forked_at_conversation_id_getter is None:
            return None
        return self.forked_at_conversation_id_getter()

    def _forked_at_entry_id(self) -> str | None:
        if self.forked_at_entry_id_getter is None:
            return None
        return self.forked_at_entry_id_getter()

    def _indexed_content(self, text: str, role: str) -> str | None:
        if self.indexed_content_provider is None:
            return None
        try:
            return self.indexed_content_provider(text, role)
        except Exception as exc:
            LOG.warning("indexed content provider failed for role=%s: %s", role, exc)
            return None

    def _append_history(
        self,
        conversation_id: str,
        role: str,
        text: str,
        *,
        forked_at_conversation_id: str | None = None,
        forked_at_entry_id: str | None = None,
    ) -> None:
        payload = {
            "channel": "history",
            "contentType": "history",
            "content": [{"role": role, "text": text}],
        }
        indexed_content = self._indexed_content(text, role)
        if indexed_content is not None:
            payload["indexedContent"] = indexed_content
        if role == "USER" and forked_at_conversation_id and forked_at_entry_id:
            payload["forkedAtConversationId"] = forked_at_conversation_id
            payload["forkedAtEntryId"] = forked_at_entry_id
        try:
            with httpx.Client(base_url=self.base_url, timeout=30.0) as client:
                response = client.post(
                    f"/v1/conversations/{conversation_id}/entries",
                    json=payload,
                    headers=self._headers(),
                )
                if response.status_code == 404:
                    client.post(
                        "/v1/conversations",
                        json={"id": conversation_id, "title": f"Python checkpoint {conversation_id}"},
                        headers=self._headers(),
                    )
                    response = client.post(
                        f"/v1/conversations/{conversation_id}/entries",
                        json=payload,
                        headers=self._headers(),
                    )
                if self._is_duplicate_conversation_error(response):
                    response = client.post(
                        f"/v1/conversations/{conversation_id}/entries",
                        json=payload,
                        headers=self._headers(),
                    )
                if response.status_code >= 400:
                    LOG.warning(
                        "failed to append %s history entry for conversation_id=%s: %s %s",
                        role,
                        conversation_id,
                        response.status_code,
                        response.text,
                    )
        except Exception as exc:
            LOG.warning(
                "history append failed for role=%s conversation_id=%s: %s",
                role,
                conversation_id,
                exc,
            )

    def _conversation_id(self) -> str | None:
        if self.conversation_id_getter is None:
            return None
        return self.conversation_id_getter()

    def _with_callback(self, request: ModelRequest, callback: BaseCallbackHandler) -> ModelRequest:
        model_settings = dict(request.model_settings)
        existing = model_settings.pop("callbacks", None)
        callbacks: list[Any] = []
        if existing is None:
            callbacks = [callback]
        elif isinstance(existing, list):
            callbacks = [*existing, callback]
        elif isinstance(existing, tuple):
            callbacks = [*existing, callback]
        else:
            callbacks = [existing, callback]

        configured_model = request.model.with_config({"callbacks": callbacks})
        return request.override(model=configured_model, model_settings=model_settings)

    def _create_recorder(self, conversation_id: str) -> BaseResponseRecorder:
        if not self.enable_grpc_recording:
            from .response_recorder import NoopResponseRecorder

            return NoopResponseRecorder()
        return create_grpc_response_recorder(
            target=self.grpc_target,
            conversation_id=conversation_id,
            authorization=self._authorization(),
            api_key=self.api_key,
            timeout_seconds=self.grpc_timeout_seconds,
        )

    def _record_final_ai(self, callback: _RecorderCallback, ai_text: str) -> None:
        if callback.recorded_chunks or not ai_text:
            return
        if self.grpc_record_mode == "events":
            callback.recorder.record(json.dumps({"eventType": "ChatCompletedEvent", "text": ai_text}) + "\n")
        else:
            callback.recorder.record(ai_text)

    def wrap_model_call(
        self,
        request: "ModelRequest | str",
        handler: "Callable",
    ) -> Any:
        # LangGraph pattern: wrap_model_call(user_text: str, lambda: model.invoke(messages))
        if isinstance(request, str):
            conversation_id = self._conversation_id()
            if not conversation_id:
                return handler()
            user_text = request
            if user_text:
                self._append_history(
                    conversation_id,
                    "USER",
                    user_text,
                    forked_at_conversation_id=self._forked_at_conversation_id(),
                    forked_at_entry_id=self._forked_at_entry_id(),
                )
            response = handler()
            ai_text = self._extract_text(response)
            if ai_text:
                self._append_history(conversation_id, "AI", ai_text)
            return response

        # Original LangChain agent pattern: wrap_model_call(ModelRequest, handler)
        conversation_id = self._conversation_id()
        if not conversation_id:
            return handler(request)

        state_messages = request.state.get("messages", [])
        if isinstance(state_messages, list):
            user_text = self._last_message_of_type(state_messages, {"human", "user"})
            if user_text:
                self._append_history(
                    conversation_id,
                    "USER",
                    user_text,
                    forked_at_conversation_id=self._forked_at_conversation_id(),
                    forked_at_entry_id=self._forked_at_entry_id(),
                )

        recorder = self._create_recorder(conversation_id)
        callback = _RecorderCallback(recorder, record_mode=self.grpc_record_mode)
        wrapped_request = self._with_callback(request, callback)

        try:
            response = handler(wrapped_request)
        except Exception:
            partial_text = callback.buffered_text()
            if partial_text:
                self._append_history(conversation_id, "AI", partial_text)
            recorder.complete()
            raise

        ai_text = self._last_message_of_type(response.result, {"ai", "assistant"}) or callback.buffered_text()
        if ai_text:
            self._record_final_ai(callback, ai_text)
            self._append_history(conversation_id, "AI", ai_text)
        recorder.complete()
        return response

    async def awrap_model_call(
        self,
        request: "ModelRequest | str",
        handler: "Callable",
    ) -> Any:
        # LangGraph pattern: awrap_model_call(user_text: str, lambda: model.ainvoke(messages))
        if isinstance(request, str):
            conversation_id = self._conversation_id()
            if not conversation_id:
                return await handler()
            user_text = request
            if user_text:
                self._append_history(
                    conversation_id,
                    "USER",
                    user_text,
                    forked_at_conversation_id=self._forked_at_conversation_id(),
                    forked_at_entry_id=self._forked_at_entry_id(),
                )
            response = await handler()
            ai_text = self._extract_text(response)
            if ai_text:
                self._append_history(conversation_id, "AI", ai_text)
            return response

        # Original LangChain agent pattern: awrap_model_call(ModelRequest, async handler)
        conversation_id = self._conversation_id()
        if not conversation_id:
            return await handler(request)

        state_messages = request.state.get("messages", [])
        if isinstance(state_messages, list):
            user_text = self._last_message_of_type(state_messages, {"human", "user"})
            if user_text:
                self._append_history(
                    conversation_id,
                    "USER",
                    user_text,
                    forked_at_conversation_id=self._forked_at_conversation_id(),
                    forked_at_entry_id=self._forked_at_entry_id(),
                )

        recorder = self._create_recorder(conversation_id)
        callback = _RecorderCallback(recorder, record_mode=self.grpc_record_mode)
        wrapped_request = self._with_callback(request, callback)

        try:
            response = await handler(wrapped_request)
        except Exception:
            partial_text = callback.buffered_text()
            if partial_text:
                self._append_history(conversation_id, "AI", partial_text)
            recorder.complete()
            raise

        ai_text = self._last_message_of_type(response.result, {"ai", "assistant"}) or callback.buffered_text()
        if ai_text:
            self._record_final_ai(callback, ai_text)
            self._append_history(conversation_id, "AI", ai_text)
        recorder.complete()
        return response
