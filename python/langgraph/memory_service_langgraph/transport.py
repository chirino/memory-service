from __future__ import annotations

import os
from pathlib import Path

import httpx


DEFAULT_BASE_URL = "http://localhost:8082"
LOGICAL_BASE_URL = "http://localhost"


def resolve_unix_socket(unix_socket: str | None = None) -> str | None:
    candidate = (unix_socket or "").strip()
    if not candidate:
        return None
    path = Path(candidate)
    if not path.is_absolute():
        raise ValueError("MEMORY_SERVICE_UNIX_SOCKET must be an absolute path")
    return str(path)


def resolve_rest_base_url(base_url: str, unix_socket: str | None = None) -> str:
    if unix_socket:
        return LOGICAL_BASE_URL
    return base_url.rstrip("/")


def resolve_env_config() -> dict[str, str | None]:
    unix_socket = resolve_unix_socket(os.environ.get("MEMORY_SERVICE_UNIX_SOCKET"))
    env_base_url = os.environ.get("MEMORY_SERVICE_URL", DEFAULT_BASE_URL).rstrip("/")
    return {
        "base_url": resolve_rest_base_url(env_base_url, unix_socket),
        "unix_socket": unix_socket,
        "token": os.environ.get("MEMORY_SERVICE_TOKEN", ""),
    }


def httpx_client_kwargs(
    *,
    base_url: str,
    unix_socket: str | None = None,
    timeout: float = 10.0,
) -> dict[str, object]:
    socket_path = resolve_unix_socket(unix_socket)
    kwargs: dict[str, object] = {
        "base_url": resolve_rest_base_url(base_url, socket_path),
        "timeout": timeout,
    }
    if socket_path:
        kwargs["transport"] = httpx.HTTPTransport(uds=socket_path)
    return kwargs


def httpx_async_client_kwargs(
    *,
    base_url: str,
    unix_socket: str | None = None,
    timeout: float = 10.0,
) -> dict[str, object]:
    socket_path = resolve_unix_socket(unix_socket)
    kwargs: dict[str, object] = {
        "base_url": resolve_rest_base_url(base_url, socket_path),
        "timeout": timeout,
    }
    if socket_path:
        kwargs["transport"] = httpx.AsyncHTTPTransport(uds=socket_path)
    return kwargs
