"""Async LangGraph BaseStore implementation backed by the Memory Service REST API."""

from __future__ import annotations

import asyncio
import logging
import os
from typing import Any, Iterable

import httpx

logger = logging.getLogger(__name__)
from langgraph.store.base import (
    BaseStore,
    GetOp,
    Item,
    ListNamespacesOp,
    PutOp,
    Result,
    SearchItem,
    SearchOp,
)


class AsyncMemoryServiceStore(BaseStore):
    """Async LangGraph BaseStore backed by the Memory Service episodic API.

    Configure via environment variables or constructor arguments:

        MEMORY_SERVICE_URL   — base URL of the memory service (e.g. http://localhost:8082)
        MEMORY_SERVICE_TOKEN — Bearer token for authentication
    """

    def __init__(
        self,
        base_url: str | None = None,
        token: str | None = None,
        timeout: float = 10.0,
    ) -> None:
        self._base_url = (base_url or os.environ.get("MEMORY_SERVICE_URL", "http://localhost:8082")).rstrip("/")
        self._token = token or os.environ.get("MEMORY_SERVICE_TOKEN", "")
        async def _log_request(request: httpx.Request) -> None:
            logger.debug("memory-service request: %s %s", request.method, request.url)

        self._client = httpx.AsyncClient(
            base_url=self._base_url,
            headers={"Authorization": f"Bearer {self._token}"} if self._token else {},
            timeout=timeout,
            event_hooks={"request": [_log_request]},
        )

    # ------------------------------------------------------------------
    # BaseStore interface — the only two abstract methods in langgraph 1.x
    # ------------------------------------------------------------------

    async def abatch(self, ops: Iterable[GetOp | SearchOp | PutOp | ListNamespacesOp]) -> list[Result]:
        results: list[Result] = []
        for op in ops:
            if isinstance(op, GetOp):
                results.append(await self._aget(op.namespace, op.key))
            elif isinstance(op, PutOp):
                if op.value is None:
                    await self._adelete(op.namespace, op.key)
                else:
                    await self._aput(op.namespace, op.key, op.value, op.index)
                results.append(None)
            elif isinstance(op, SearchOp):
                results.append(await self._asearch(op.namespace_prefix, query=op.query, filter=op.filter, limit=op.limit, offset=op.offset))
            elif isinstance(op, ListNamespacesOp):
                results.append(await self._alist_namespaces(op))
            else:
                results.append(None)
        return results

    def batch(self, ops: Iterable[GetOp | SearchOp | PutOp | ListNamespacesOp]) -> list[Result]:
        return asyncio.get_event_loop().run_until_complete(self.abatch(ops))

    # ------------------------------------------------------------------
    # Private async helpers (HTTP calls)
    # ------------------------------------------------------------------

    async def _aput(
        self,
        namespace: tuple[str, ...],
        key: str,
        value: dict[str, Any],
        index: bool | list[str] | None = None,
    ) -> None:
        body: dict[str, Any] = {
            "namespace": list(namespace),
            "key": key,
            "value": value,
        }
        if index is False:
            body["index_disabled"] = True
        elif isinstance(index, list):
            body["index_fields"] = index

        resp = await self._client.put("/v1/memories", json=body)
        resp.raise_for_status()

    async def _aget(self, namespace: tuple[str, ...], key: str) -> Item | None:
        params = _ns_params(namespace)
        params.append(("key", key))
        resp = await self._client.get("/v1/memories", params=params)
        if resp.status_code == 404:
            return None
        resp.raise_for_status()
        return _to_item(resp.json())

    async def _adelete(self, namespace: tuple[str, ...], key: str) -> None:
        params = _ns_params(namespace)
        params.append(("key", key))
        resp = await self._client.delete("/v1/memories", params=params)
        if resp.status_code == 404:
            return
        resp.raise_for_status()

    async def _asearch(
        self,
        namespace_prefix: tuple[str, ...],
        *,
        query: str | None = None,
        filter: dict[str, Any] | None = None,
        limit: int = 10,
        offset: int = 0,
    ) -> list[SearchItem]:
        body: dict[str, Any] = {
            "namespace_prefix": list(namespace_prefix),
            "limit": min(limit, 100),
            "offset": offset,
        }
        if query:
            body["query"] = query
        if filter:
            body["filter"] = filter

        resp = await self._client.post("/v1/memories/search", json=body)
        resp.raise_for_status()
        return [_to_search_item(item) for item in resp.json().get("items", [])]

    async def _alist_namespaces(self, op: ListNamespacesOp) -> list[tuple[str, ...]]:
        params: list[tuple[str, str]] = []
        if op.match_conditions:
            for cond in op.match_conditions:
                for seg in cond.path:
                    if seg == "*":
                        break
                    params.append((cond.match_type, seg))
        if op.max_depth is not None:
            params.append(("max_depth", str(op.max_depth)))

        resp = await self._client.get("/v1/memories/namespaces", params=params)
        resp.raise_for_status()
        return [tuple(ns) for ns in resp.json().get("namespaces", [])]

    # ------------------------------------------------------------------
    # Async context manager
    # ------------------------------------------------------------------

    async def __aenter__(self) -> "AsyncMemoryServiceStore":
        return self

    async def __aexit__(self, *_: Any) -> None:
        await self._client.aclose()


def _ns_params(namespace: tuple[str, ...]) -> list[tuple[str, str]]:
    return [("ns", seg) for seg in namespace]


def _to_item(data: dict[str, Any]) -> Item:
    return Item(
        namespace=tuple(data["namespace"]),
        key=data["key"],
        value=data.get("value", {}),
        created_at=data.get("createdAt"),
        updated_at=data.get("createdAt"),
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
