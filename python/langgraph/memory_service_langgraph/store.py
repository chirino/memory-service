"""Synchronous LangGraph BaseStore implementation backed by the Memory Service REST API."""

from __future__ import annotations

import os
from typing import Any, Optional

import httpx
from langgraph.store.base import BaseStore, Item, SearchItem, NamespacePath


class MemoryServiceStore(BaseStore):
    """Synchronous LangGraph BaseStore backed by the Memory Service episodic API.

    Configure via environment variables or constructor arguments:

        MEMORY_SERVICE_URL   — base URL of the memory service (e.g. http://localhost:8083)
        MEMORY_SERVICE_TOKEN — Bearer token for authentication
    """

    def __init__(
        self,
        base_url: str | None = None,
        token: str | None = None,
        timeout: float = 10.0,
    ) -> None:
        self._base_url = (base_url or os.environ.get("MEMORY_SERVICE_URL", "http://localhost:8083")).rstrip("/")
        self._token = token or os.environ.get("MEMORY_SERVICE_TOKEN", "")
        self._client = httpx.Client(
            base_url=self._base_url,
            headers={"Authorization": f"Bearer {self._token}"} if self._token else {},
            timeout=timeout,
        )

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
        if index is False:
            body["index_disabled"] = True
        elif isinstance(index, list):
            body["index_fields"] = index

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
        body: dict[str, Any] = {
            "namespace_prefix": list(namespace_prefix),
            "limit": min(limit, 100),
            "offset": offset,
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
