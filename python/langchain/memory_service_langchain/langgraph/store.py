"""Synchronous LangGraph BaseStore implementation backed by the Memory Service REST API."""

from __future__ import annotations

from typing import Any, Optional

import httpx
from langgraph.store.base import BaseStore, Item, SearchItem, NamespacePath

from .indexing import IndexBuilder, IndexRedactor, build_index_payload
from .transport import (
    httpx_client_kwargs,
    resolve_env_config,
    resolve_rest_base_url,
    resolve_unix_socket,
)


class MemoryServiceStore(BaseStore):
    """Synchronous LangGraph BaseStore backed by the Memory Service episodic API.

    Use explicit constructor arguments, or `from_env()` for env-based setup.
    """

    def __init__(
        self,
        *,
        base_url: str,
        token: str,
        unix_socket: str | None = None,
        timeout: float = 10.0,
        index_builder: IndexBuilder | None = None,
        index_redactor: IndexRedactor | None = None,
    ) -> None:
        self._unix_socket = resolve_unix_socket(unix_socket)
        self._base_url = resolve_rest_base_url(base_url, self._unix_socket)
        self._token = token
        if index_builder is not None and index_redactor is not None:
            raise ValueError("index_builder and index_redactor are mutually exclusive")
        self._client = httpx.Client(
            **httpx_client_kwargs(
                base_url=self._base_url,
                unix_socket=self._unix_socket,
                timeout=timeout,
            ),
            headers={"Authorization": f"Bearer {self._token}"} if self._token else {},
        )
        if index_builder is not None:
            self._index_builder = index_builder
        else:
            self._index_builder = lambda namespace, key, value, index: build_index_payload(
                namespace, key, value, index, redactor=index_redactor
            )

    @classmethod
    def from_env(cls, **overrides: Any) -> "MemoryServiceStore":
        config = resolve_env_config()
        config.update(overrides)
        return cls(**config)

    # ------------------------------------------------------------------
    # BaseStore interface
    # ------------------------------------------------------------------

    def put(
        self,
        namespace: tuple[str, ...],
        key: str,
        value: dict[str, Any],
        index: bool | list[str] | None = None,
    ) -> None:
        """Write or overwrite an item."""
        body: dict[str, Any] = {
            "namespace": list(namespace),
            "key": key,
            "value": value,
        }
        built_index = self._index_builder(namespace, key, value, index)
        if built_index is not None:
            body["index"] = built_index

        resp = self._client.put("/v1/memories", json=body)
        resp.raise_for_status()

    def get(self, namespace: tuple[str, ...], key: str) -> Optional[Item]:
        """Retrieve a single item."""
        params = _ns_params(namespace)
        params.append(("key", key))
        resp = self._client.get("/v1/memories", params=params)
        if resp.status_code == 404:
            return None
        resp.raise_for_status()
        return _to_item(resp.json())

    def delete(self, namespace: tuple[str, ...], key: str) -> None:
        """Delete an item."""
        params = _ns_params(namespace)
        params.append(("key", key))
        resp = self._client.delete("/v1/memories", params=params)
        if resp.status_code == 404:
            return
        resp.raise_for_status()

    def search(
        self,
        namespace_prefix: tuple[str, ...],
        /,
        *,
        query: str | None = None,
        filter: dict[str, Any] | None = None,
        limit: int = 10,
        offset: int = 0,
    ) -> list[SearchItem]:
        """Search memories by namespace prefix, optional query, and optional filter."""
        if offset:
            raise ValueError("Memory Service memory search does not support offset pagination")
        body: dict[str, Any] = {
            "namespace_prefix": list(namespace_prefix),
            "limit": min(limit, 100),
        }
        if query:
            body["query"] = query
        if filter:
            body["filter"] = filter

        resp = self._client.post("/v1/memories/search", json=body)
        resp.raise_for_status()
        data = resp.json()
        return [_to_search_item(item) for item in data.get("items", [])]

    def list_namespaces(
        self,
        *,
        prefix: NamespacePath | None = None,
        suffix: NamespacePath | None = None,
        max_depth: int | None = None,
        limit: int = 100,
        offset: int = 0,
    ) -> list[tuple[str, ...]]:
        """List namespaces matching the given prefix / suffix."""
        params: list[tuple[str, str]] = []
        if prefix:
            for seg in prefix:
                if seg == "*":
                    break  # wildcard: stop prefix at this point
                params.append(("prefix", seg))
        if suffix:
            for seg in suffix:
                params.append(("suffix", seg))
        if max_depth is not None:
            params.append(("max_depth", str(max_depth)))

        resp = self._client.get("/v1/memories/namespaces", params=params)
        resp.raise_for_status()
        data = resp.json()
        return [tuple(ns) for ns in data.get("namespaces", [])]

    # ------------------------------------------------------------------
    # Context manager support
    # ------------------------------------------------------------------

    def __enter__(self) -> "MemoryServiceStore":
        return self

    def __exit__(self, *_: Any) -> None:
        self._client.close()


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _ns_params(namespace: tuple[str, ...]) -> list[tuple[str, str]]:
    return [("ns", seg) for seg in namespace]


def _to_item(data: dict[str, Any]) -> Item:
    return Item(
        namespace=tuple(data["namespace"]),
        key=data["key"],
        value=data.get("value", {}),
        created_at=data.get("createdAt"),
        updated_at=data.get("createdAt"),  # memory service uses createdAt only
    )


def _to_search_item(data: dict[str, Any]) -> SearchItem:
    return SearchItem(
        namespace=tuple(data["namespace"]),
        key=data["key"],
        value=data.get("value", {}),
        created_at=data.get("createdAt"),
        updated_at=data.get("createdAt"),
        score=data.get("score"),
    )
