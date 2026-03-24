from __future__ import annotations

import logging
from typing import Any, Callable

import httpx
from langchain.agents.middleware import AgentMiddleware
from langchain.agents.middleware.types import ModelRequest

from .request_context import (
    get_conversation_authorization,
    get_request_authorization,
    get_request_conversation_id,
    get_request_forked_at_conversation_id,
    get_request_forked_at_entry_id,
)
from .transport import (
    httpx_client_kwargs,
    resolve_env_config,
    resolve_rest_base_url,
    resolve_unix_socket,
)


LOG = logging.getLogger(__name__)


class MemoryServiceHistoryMiddleware(AgentMiddleware):
    """Records USER/AI history entries. Response recording is handled by the recording-manager stream path."""

    def __init__(
        self,
        *,
        base_url: str,
        api_key: str,
        unix_socket: str | None = None,
        authorization_getter: Callable[[], str | None] | None = None,
        conversation_id_getter: Callable[[], str | None] | None = None,
        forked_at_conversation_id_getter: Callable[[], str | None] | None = None,
        forked_at_entry_id_getter: Callable[[], str | None] | None = None,
        indexed_content_provider: Callable[[str, str], str | None] | None = None,
    ):
        super().__init__()
        self.unix_socket = resolve_unix_socket(unix_socket)
        self.base_url = resolve_rest_base_url(base_url, self.unix_socket)
        self.api_key = api_key
        self.authorization_getter = authorization_getter or get_request_authorization
        self.conversation_id_getter = conversation_id_getter or get_request_conversation_id
        self.forked_at_conversation_id_getter = (
            forked_at_conversation_id_getter or get_request_forked_at_conversation_id
        )
        self.forked_at_entry_id_getter = forked_at_entry_id_getter or get_request_forked_at_entry_id
        self.indexed_content_provider = indexed_content_provider

    @classmethod
    def from_env(cls, **overrides: Any) -> "MemoryServiceHistoryMiddleware":
        config = resolve_env_config()
        config.update(overrides)
        return cls(**config)

    def _authorization(self, conversation_id: str | None = None) -> str | None:
        if not self.authorization_getter:
            authorization = None
        else:
            authorization = self.authorization_getter()
        if not authorization and conversation_id:
            authorization = get_conversation_authorization(conversation_id)
        return authorization

    def _headers(self, conversation_id: str | None = None) -> dict[str, str]:
        headers = {"X-API-Key": self.api_key}
        authorization = self._authorization(conversation_id)
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
        content_type: str = "history",
        events: list[dict[str, Any]] | None = None,
        forked_at_conversation_id: str | None = None,
        forked_at_entry_id: str | None = None,
    ) -> None:
        block: dict[str, Any] = {"role": role}
        if text:
            block["text"] = text
        if events:
            block["events"] = events
        if len(block) == 1:
            return

        payload = {
            "channel": "history",
            "contentType": content_type,
            "content": [block],
        }
        indexed_content = self._indexed_content(text, role)
        if indexed_content is not None:
            payload["indexedContent"] = indexed_content
        if role == "USER" and forked_at_conversation_id and forked_at_entry_id:
            payload["forkedAtConversationId"] = forked_at_conversation_id
            payload["forkedAtEntryId"] = forked_at_entry_id
        try:
            with httpx.Client(
                **httpx_client_kwargs(
                    base_url=self.base_url,
                    unix_socket=self.unix_socket,
                    timeout=30.0,
                )
            ) as client:
                response = client.post(
                    f"/v1/conversations/{conversation_id}/entries",
                    json=payload,
                    headers=self._headers(conversation_id),
                )
                if response.status_code == 404:
                    client.post(
                        "/v1/conversations",
                        json={"id": conversation_id, "title": f"Python checkpoint {conversation_id}"},
                        headers=self._headers(conversation_id),
                    )
                    response = client.post(
                        f"/v1/conversations/{conversation_id}/entries",
                        json=payload,
                        headers=self._headers(conversation_id),
                    )
                if self._is_duplicate_conversation_error(response):
                    response = client.post(
                        f"/v1/conversations/{conversation_id}/entries",
                        json=payload,
                        headers=self._headers(conversation_id),
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

    def append_user_history(
        self,
        conversation_id: str,
        text: str,
        *,
        forked_at_conversation_id: str | None = None,
        forked_at_entry_id: str | None = None,
    ) -> None:
        self._append_history(
            conversation_id,
            "USER",
            text,
            forked_at_conversation_id=forked_at_conversation_id,
            forked_at_entry_id=forked_at_entry_id,
        )

    def append_ai_history(
        self,
        conversation_id: str,
        text: str,
        *,
        content_type: str = "history",
        events: list[dict[str, Any]] | None = None,
    ) -> None:
        self._append_history(
            conversation_id,
            "AI",
            text,
            content_type=content_type,
            events=events,
        )

    def wrap_model_call(
        self,
        request: ModelRequest | str,
        handler: Callable,
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

        # LangChain agent pattern: wrap_model_call(ModelRequest, handler)
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

        response = handler(request)
        ai_text: str | None = None
        result = getattr(response, "result", None)
        if isinstance(result, list):
            ai_text = self._last_message_of_type(result, {"ai", "assistant"})
        if not ai_text:
            ai_text = self._extract_text(response)
        if ai_text:
            self._append_history(conversation_id, "AI", ai_text)
        return response

    async def awrap_model_call(
        self,
        request: ModelRequest | str,
        handler: Callable,
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

        # LangChain agent pattern: awrap_model_call(ModelRequest, async handler)
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

        response = await handler(request)
        ai_text: str | None = None
        result = getattr(response, "result", None)
        if isinstance(result, list):
            ai_text = self._last_message_of_type(result, {"ai", "assistant"})
        if not ai_text:
            ai_text = self._extract_text(response)
        if ai_text:
            self._append_history(conversation_id, "AI", ai_text)
        return response
