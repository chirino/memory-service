from __future__ import annotations

import os
from pathlib import Path
from urllib.parse import urlparse

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
    unix_socket = resolve_unix_socket(os.getenv("MEMORY_SERVICE_UNIX_SOCKET"))
    env_base_url = os.getenv("MEMORY_SERVICE_URL", DEFAULT_BASE_URL).rstrip("/")
    return {
        "base_url": resolve_rest_base_url(env_base_url, unix_socket),
        "unix_socket": unix_socket,
        "api_key": os.getenv("MEMORY_SERVICE_API_KEY", ""),
    }


def httpx_client_kwargs(
    *,
    base_url: str,
    unix_socket: str | None = None,
    timeout: float = 30.0,
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
    timeout: float = 30.0,
) -> dict[str, object]:
    socket_path = resolve_unix_socket(unix_socket)
    kwargs: dict[str, object] = {
        "base_url": resolve_rest_base_url(base_url, socket_path),
        "timeout": timeout,
    }
    if socket_path:
        kwargs["transport"] = httpx.AsyncHTTPTransport(uds=socket_path)
    return kwargs


def resolve_grpc_target(
    *,
    base_url: str,
    grpc_target: str | None = None,
    unix_socket: str | None = None,
    grpc_port: str | None = None,
) -> str:
    configured_target = (grpc_target or "").strip()
    if configured_target:
        return configured_target

    socket_path = resolve_unix_socket(unix_socket)
    if socket_path:
        return f"unix://{socket_path}"

    parsed = urlparse(base_url.rstrip("/"))
    grpc_host = parsed.hostname or "localhost"
    resolved_grpc_port = grpc_port or _grpc_port_from_url(parsed)
    return f"{grpc_host}:{resolved_grpc_port}"


def _grpc_port_from_url(parsed_url: object) -> str:
    port = getattr(parsed_url, "port", None)
    if port is not None:
        return str(port)
    scheme = getattr(parsed_url, "scheme", "")
    if scheme == "https":
        return "443"
    return "80"
