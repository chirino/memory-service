from __future__ import annotations

from contextlib import contextmanager
from contextvars import ContextVar
import os
from threading import RLock
from typing import Any, Callable, Mapping, overload

import httpx


_request_authorization: ContextVar[str | None] = ContextVar(
    "request_authorization",
    default=None,
)
_request_conversation_id: ContextVar[str | None] = ContextVar(
    "request_conversation_id",
    default=None,
)
_request_forked_at_conversation_id: ContextVar[str | None] = ContextVar(
    "request_forked_at_conversation_id",
    default=None,
)
_request_forked_at_entry_id: ContextVar[str | None] = ContextVar(
    "request_forked_at_entry_id",
    default=None,
)
_conversation_authorizations: dict[str, list[str]] = {}
_conversation_authorizations_lock = RLock()


def get_request_authorization() -> str | None:
    return _request_authorization.get()


def get_request_conversation_id() -> str | None:
    return _request_conversation_id.get()


def get_request_forked_at_conversation_id() -> str | None:
    return _request_forked_at_conversation_id.get()


def get_request_forked_at_entry_id() -> str | None:
    return _request_forked_at_entry_id.get()


def get_conversation_authorization(conversation_id: str) -> str | None:
    with _conversation_authorizations_lock:
        stack = _conversation_authorizations.get(conversation_id)
        if not stack:
            return None
        return stack[-1]


def install_fastapi_authorization_middleware(app: Any, *, header_name: str = "Authorization") -> None:
    @app.middleware("http")
    async def _bind_authorization(request: Any, call_next: Any):
        token = _request_authorization.set(request.headers.get(header_name))
        try:
            return await call_next(request)
        finally:
            _request_authorization.reset(token)


@overload
def memory_service_scope(conversation_id: str): ...


@overload
def memory_service_scope(
    conversation_id: str,
    forked_at_conversation_id: str | None,
    forked_at_entry_id: str | None,
): ...


@contextmanager
def memory_service_scope(
    conversation_id: str,
    forked_at_conversation_id: str | None = None,
    forked_at_entry_id: str | None = None,
):
    authorization = get_request_authorization()
    conversation_token = _request_conversation_id.set(conversation_id)
    forked_conversation_token = _request_forked_at_conversation_id.set(
        forked_at_conversation_id
    )
    forked_entry_token = _request_forked_at_entry_id.set(forked_at_entry_id)
    pushed_authorization = False
    if authorization:
        with _conversation_authorizations_lock:
            stack = _conversation_authorizations.setdefault(conversation_id, [])
            stack.append(authorization)
            pushed_authorization = True
    try:
        yield
    finally:
        if pushed_authorization:
            with _conversation_authorizations_lock:
                stack = _conversation_authorizations.get(conversation_id)
                if stack:
                    stack.pop()
                    if not stack:
                        _conversation_authorizations.pop(conversation_id, None)
        _request_forked_at_entry_id.reset(forked_entry_token)
        _request_forked_at_conversation_id.reset(forked_conversation_token)
        _request_conversation_id.reset(conversation_token)


def memory_service_headers(
    *,
    api_key: str | None = None,
    authorization_getter: Callable[[], str | None] | None = None,
    include_api_key: bool = True,
    include_authorization: bool = True,
    extra_headers: Mapping[str, str] | None = None,
) -> dict[str, str]:
    headers: dict[str, str] = {}
    resolved_api_key = api_key or os.getenv("MEMORY_SERVICE_API_KEY", "agent-api-key-1")
    getter = authorization_getter or get_request_authorization
    if include_api_key:
        headers["X-API-Key"] = resolved_api_key
    if include_authorization:
        authorization = getter()
        if authorization:
            headers["Authorization"] = authorization
    if extra_headers:
        headers.update(extra_headers)
    return headers


async def memory_service_request(
    method: str,
    path: str,
    *,
    params: dict[str, Any] | None = None,
    json_body: Any | None = None,
    content: bytes | str | None = None,
    data: Mapping[str, Any] | None = None,
    files: Mapping[str, Any] | None = None,
    base_url: str | None = None,
    api_key: str | None = None,
    authorization_getter: Callable[[], str | None] | None = None,
    timeout_seconds: float = 30.0,
    include_api_key: bool = True,
    include_authorization: bool = True,
    extra_headers: Mapping[str, str] | None = None,
) -> httpx.Response:
    resolved_base_url = (base_url or os.getenv("MEMORY_SERVICE_URL", "http://localhost:8082")).rstrip("/")
    async with httpx.AsyncClient(base_url=resolved_base_url, timeout=timeout_seconds) as client:
        return await client.request(
            method=method,
            url=path,
            params=params,
            json=json_body,
            content=content,
            data=data,
            files=files,
            headers=memory_service_headers(
                api_key=api_key,
                authorization_getter=authorization_getter,
                include_api_key=include_api_key,
                include_authorization=include_authorization,
                extra_headers=extra_headers,
            ),
        )
