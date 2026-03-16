from __future__ import annotations

from contextlib import contextmanager
from contextvars import ContextVar
import base64
import json
import os
from threading import RLock
import time
from typing import Any, Callable, Mapping, overload

import httpx
from fastapi.responses import JSONResponse

from .transport import httpx_async_client_kwargs


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
_request_stream_mode: ContextVar[str | None] = ContextVar(
    "request_stream_mode",
    default=None,
)
_conversation_authorizations: dict[str, list[str]] = {}
_conversation_authorizations_lock = RLock()


def _env_bool(name: str, default: bool) -> bool:
    raw = os.getenv(name)
    if raw is None:
        return default
    value = raw.strip().lower()
    return value not in {"0", "false", "no", "off", ""}


def _default_issuer() -> str | None:
    keycloak_url = os.getenv("KEYCLOAK_FRONTEND_URL") or os.getenv("KEYCLOAK_URL")
    keycloak_realm = os.getenv("KEYCLOAK_REALM")
    if not keycloak_url or not keycloak_realm:
        return None
    return f"{keycloak_url.rstrip('/')}/realms/{keycloak_realm}"


class _JwtValidationError(RuntimeError):
    pass


class _JwtValidator:
    def __init__(self, *, enabled_override: bool | None = None):
        if enabled_override is None:
            self._enabled = _env_bool("MEMORY_SERVICE_JWT_VALIDATION_ENABLED", False)
        else:
            self._enabled = enabled_override
        self._issuer = os.getenv("MEMORY_SERVICE_JWT_ISSUER") or _default_issuer()
        self._audience = os.getenv("MEMORY_SERVICE_JWT_AUDIENCE")
        self._leeway_seconds = float(os.getenv("MEMORY_SERVICE_JWT_LEEWAY_SECONDS", "0"))

    def _manual_validate(self, encoded_token: str) -> None:
        parts = encoded_token.split(".")
        if len(parts) < 2:
            raise _JwtValidationError("malformed JWT")

        payload_segment = parts[1]
        padding = "=" * ((4 - (len(payload_segment) % 4)) % 4)
        try:
            payload_bytes = base64.urlsafe_b64decode(payload_segment + padding)
            claims = json.loads(payload_bytes.decode("utf-8"))
        except Exception as exc:
            raise _JwtValidationError(f"invalid JWT payload: {exc}") from exc

        if not isinstance(claims, dict):
            raise _JwtValidationError("invalid JWT payload claims")

        exp = claims.get("exp")
        if not isinstance(exp, (int, float)):
            raise _JwtValidationError("Token is missing the \"exp\" claim")

        now = time.time()
        if now > float(exp) + self._leeway_seconds:
            raise _JwtValidationError("Signature has expired")

        nbf = claims.get("nbf")
        if isinstance(nbf, (int, float)) and now + self._leeway_seconds < float(nbf):
            raise _JwtValidationError("The token is not yet valid (nbf)")

        if self._issuer:
            iss = claims.get("iss")
            if not isinstance(iss, str) or iss != self._issuer:
                raise _JwtValidationError("Invalid issuer")

        if self._audience:
            aud = claims.get("aud")
            if isinstance(aud, str):
                matched = aud == self._audience
            elif isinstance(aud, list):
                matched = self._audience in aud
            else:
                matched = False
            if not matched:
                raise _JwtValidationError("Audience doesn't match")

    def validate(self, authorization_header: str | None) -> None:
        if not self._enabled:
            return
        if authorization_header is None:
            return
        if not authorization_header.startswith("Bearer "):
            raise _JwtValidationError("expected Bearer token")

        encoded_token = authorization_header[len("Bearer ") :].strip()
        if not encoded_token:
            raise _JwtValidationError("missing bearer token value")

        try:
            self._manual_validate(encoded_token)
        except Exception as exc:
            raise _JwtValidationError(str(exc)) from exc


def get_request_authorization() -> str | None:
    return _request_authorization.get()


def get_request_conversation_id() -> str | None:
    return _request_conversation_id.get()


def get_request_forked_at_conversation_id() -> str | None:
    return _request_forked_at_conversation_id.get()


def get_request_forked_at_entry_id() -> str | None:
    return _request_forked_at_entry_id.get()


def get_request_stream_mode() -> str | None:
    return _request_stream_mode.get()


def get_conversation_authorization(conversation_id: str) -> str | None:
    with _conversation_authorizations_lock:
        stack = _conversation_authorizations.get(conversation_id)
        if not stack:
            return None
        return stack[-1]


def install_fastapi_authorization_middleware(
    app: Any,
    *,
    header_name: str = "Authorization",
    validate_jwt: bool | None = None,
) -> None:
    validator = _JwtValidator(enabled_override=validate_jwt)

    @app.middleware("http")
    async def _bind_authorization(request: Any, call_next: Any):
        authorization = request.headers.get(header_name)
        try:
            validator.validate(authorization)
        except _JwtValidationError as exc:
            return JSONResponse({"error": f"invalid JWT: {exc}"}, status_code=401)

        token = _request_authorization.set(authorization)
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
    stream_mode: str | None = None,
): ...


@contextmanager
def memory_service_scope(
    conversation_id: str,
    forked_at_conversation_id: str | None = None,
    forked_at_entry_id: str | None = None,
    stream_mode: str | None = None,
):
    authorization = get_request_authorization()
    conversation_token = _request_conversation_id.set(conversation_id)
    forked_conversation_token = _request_forked_at_conversation_id.set(
        forked_at_conversation_id
    )
    forked_entry_token = _request_forked_at_entry_id.set(forked_at_entry_id)
    stream_mode_token = _request_stream_mode.set(stream_mode)
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
        _request_stream_mode.reset(stream_mode_token)


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
    async with httpx.AsyncClient(
        **httpx_async_client_kwargs(base_url=base_url, timeout=timeout_seconds)
    ) as client:
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
