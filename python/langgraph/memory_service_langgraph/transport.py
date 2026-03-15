from __future__ import annotations

import os
from pathlib import Path

import httpx


DEFAULT_BASE_URL = "http://localhost:8082"
LOGICAL_BASE_URL = "http://localhost"


def resolve_unix_socket(unix_socket: str | None = None) -> str | None:
    candidate = (unix_socket or os.environ.get("MEMORY_SERVICE_UNIX_SOCKET", "")).strip()
    if not candidate:
        return None
    path = Path(candidate)
    if not path.is_absolute():
        raise ValueError("MEMORY_SERVICE_UNIX_SOCKET must be an absolute path")
    return str(path)


def resolve_rest_base_url(base_url: str | None = None, unix_socket: str | None = None) -> str:
    if resolve_unix_socket(unix_socket):
        return LOGICAL_BASE_URL
    return (base_url or os.environ.get("MEMORY_SERVICE_URL", DEFAULT_BASE_URL)).rstrip("/")


def httpx_client_kwargs(
    *,
    base_url: str | None = None,
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
    base_url: str | None = None,
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
