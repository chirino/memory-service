from __future__ import annotations

import base64
import os
import uuid as _uuid_module
from collections.abc import AsyncIterator, Callable, Iterator, Sequence
from typing import Any

import httpx
from langchain_core.runnables import RunnableConfig
from langgraph.checkpoint.base import (
    BaseCheckpointSaver,
    ChannelVersions,
    Checkpoint,
    CheckpointMetadata,
    CheckpointTuple,
    get_checkpoint_id,
    get_checkpoint_metadata,
)

from .request_context import (
    get_conversation_authorization,
    get_request_authorization,
    get_request_forked_at_conversation_id,
    get_request_forked_at_entry_id,
)

# Stable namespace UUID for deriving conversation UUIDs from arbitrary thread_ids.
_CONV_ID_NAMESPACE = _uuid_module.UUID("6ba7b810-9dad-11d1-80b4-00c04fd430c8")


class MemoryServiceCheckpointSaver(BaseCheckpointSaver[str]):
    """LangGraph checkpoint saver backed by Memory Service MEMORY channel."""

    CHECKPOINT_CONTENT_TYPE = "LangGraph/checkpoint"

    def __init__(
        self,
        *,
        base_url: str | None = None,
        api_key: str | None = None,
        authorization_getter: Callable[[], str | None] | None = None,
        forked_at_conversation_id_getter: Callable[[], str | None] | None = None,
        forked_at_entry_id_getter: Callable[[], str | None] | None = None,
        **kwargs: Any,
    ):
        super().__init__(**kwargs)
        self.base_url = (base_url or os.getenv("MEMORY_SERVICE_URL", "http://localhost:8082")).rstrip("/")
        self.api_key = api_key or os.getenv("MEMORY_SERVICE_API_KEY", "agent-api-key-1")
        self.authorization_getter = authorization_getter or get_request_authorization
        self.forked_at_conversation_id_getter = (
            forked_at_conversation_id_getter or get_request_forked_at_conversation_id
        )
        self.forked_at_entry_id_getter = forked_at_entry_id_getter or get_request_forked_at_entry_id

    def _conv_id(self, thread_id: str) -> str:
        """Return a deterministic UUID string for any thread_id.

        If thread_id is already a valid UUID, it is returned unchanged.
        Otherwise a stable UUID v5 is derived so the server (which requires
        UUID-format conversation IDs) always receives a valid UUID while the
        caller can still use human-friendly thread IDs like "bob".
        """
        try:
            _uuid_module.UUID(thread_id)
            return thread_id
        except ValueError:
            return str(_uuid_module.uuid5(_CONV_ID_NAMESPACE, thread_id))

    def _headers(self, thread_id: str | None = None) -> dict[str, str]:
        headers = {"X-API-Key": self.api_key}
        authorization: str | None = None
        if self.authorization_getter:
            authorization = self.authorization_getter()
        if not authorization and thread_id:
            authorization = get_conversation_authorization(thread_id)
        if authorization:
            headers["Authorization"] = authorization
        return headers

    def _request(
        self,
        method: str,
        path: str,
        *,
        thread_id: str | None = None,
        params: dict[str, Any] | None = None,
        json_body: Any | None = None,
    ) -> httpx.Response:
        with httpx.Client(base_url=self.base_url, timeout=30.0) as client:
            return client.request(
                method=method,
                url=path,
                params=params,
                json=json_body,
                headers=self._headers(thread_id),
            )

    def _forked_at_conversation_id(self) -> str | None:
        if self.forked_at_conversation_id_getter is None:
            return None
        return self.forked_at_conversation_id_getter()

    def _forked_at_entry_id(self) -> str | None:
        if self.forked_at_entry_id_getter is None:
            return None
        return self.forked_at_entry_id_getter()

    def _payload_with_fork_metadata(self, payload: dict[str, Any]) -> dict[str, Any]:
        forked_at_conversation_id = self._forked_at_conversation_id()
        forked_at_entry_id = self._forked_at_entry_id()
        if not (forked_at_conversation_id and forked_at_entry_id):
            return payload
        fork_payload = dict(payload)
        fork_payload["forkedAtConversationId"] = forked_at_conversation_id
        fork_payload["forkedAtEntryId"] = forked_at_entry_id
        return fork_payload

    def _is_duplicate_conversation_error(self, response: httpx.Response) -> bool:
        if response.status_code < 500:
            return False
        body = response.text.lower()
        return "duplicate key value violates unique constraint" in body and "conversation_groups_pkey" in body

    def _encode_typed(self, value: Any) -> dict[str, str]:
        type_name, data = self.serde.dumps_typed(value)
        return {
            "type": type_name,
            "data": base64.b64encode(data).decode("ascii"),
        }

    def _decode_typed(self, encoded: dict[str, Any]) -> Any:
        type_name = encoded.get("type")
        data = encoded.get("data")
        if not isinstance(type_name, str) or not isinstance(data, str):
            raise RuntimeError("invalid serialized payload")
        return self.serde.loads_typed((type_name, base64.b64decode(data)))

    def _entry_content_item(self, entry: dict[str, Any]) -> dict[str, Any] | None:
        if entry.get("contentType") != self.CHECKPOINT_CONTENT_TYPE:
            return None
        content = entry.get("content")
        if not isinstance(content, list) or not content:
            return None
        item = content[0]
        if not isinstance(item, dict):
            return None
        return item

    def _entry_sort_key(self, entry: dict[str, Any]) -> str:
        created_at = entry.get("createdAt")
        if isinstance(created_at, str):
            return created_at
        return str(entry.get("id", ""))

    def _checkpoint_tuple_from_entry(
        self,
        thread_id: str,
        checkpoint_ns: str,
        entry: dict[str, Any],
    ) -> CheckpointTuple:
        item = self._entry_content_item(entry)
        if not item:
            raise RuntimeError("invalid checkpoint entry payload")

        checkpoint = self._decode_typed(item["checkpoint"])
        metadata = self._decode_typed(item["metadata"])
        checkpoint_id = item.get("checkpoint_id")
        if not isinstance(checkpoint_id, str):
            checkpoint_id = str(entry.get("id"))
        parent_checkpoint_id = item.get("parent_checkpoint_id")

        parent_config = None
        if isinstance(parent_checkpoint_id, str) and parent_checkpoint_id:
            parent_config = {
                "configurable": {
                    "thread_id": thread_id,
                    "checkpoint_ns": checkpoint_ns,
                    "checkpoint_id": parent_checkpoint_id,
                }
            }

        return CheckpointTuple(
            config={
                "configurable": {
                    "thread_id": thread_id,
                    "checkpoint_ns": checkpoint_ns,
                    "checkpoint_id": checkpoint_id,
                }
            },
            checkpoint=checkpoint,
            metadata=metadata,
            pending_writes=[],
            parent_config=parent_config,
        )

    def get_tuple(self, config: RunnableConfig) -> CheckpointTuple | None:
        thread_id: str = config["configurable"]["thread_id"]
        conv_id = self._conv_id(thread_id)
        checkpoint_ns: str = config["configurable"].get("checkpoint_ns", "")
        checkpoint_id = get_checkpoint_id(config)

        if checkpoint_id:
            response = self._request(
                "GET",
                f"/v1/conversations/{conv_id}/entries/{checkpoint_id}",
                thread_id=thread_id,
            )
            if response.status_code == 404:
                return None
            if response.status_code >= 400:
                raise RuntimeError(response.text)
            entry = response.json()
            item = self._entry_content_item(entry)
            if not item or item.get("checkpoint_ns", "") != checkpoint_ns:
                return None
            return self._checkpoint_tuple_from_entry(thread_id, checkpoint_ns, entry)

        response = self._request(
            "GET",
            f"/v1/conversations/{conv_id}/entries",
            thread_id=thread_id,
            params={"channel": "memory"},
        )
        if response.status_code == 404:
            return None
        if response.status_code >= 400:
            raise RuntimeError(response.text)

        entries = response.json().get("data", [])
        checkpoints = [
            e
            for e in entries
            if isinstance(e, dict)
            and (item := self._entry_content_item(e))
            and item.get("checkpoint_ns", "") == checkpoint_ns
        ]
        if not checkpoints:
            return None
        latest = max(checkpoints, key=self._entry_sort_key)
        return self._checkpoint_tuple_from_entry(thread_id, checkpoint_ns, latest)

    def list(
        self,
        config: RunnableConfig | None,
        *,
        filter: dict[str, Any] | None = None,
        before: RunnableConfig | None = None,
        limit: int | None = None,
    ) -> Iterator[CheckpointTuple]:
        if config is None:
            return iter(())

        thread_id: str = config["configurable"]["thread_id"]
        conv_id = self._conv_id(thread_id)
        checkpoint_ns: str = config["configurable"].get("checkpoint_ns", "")
        before_id = get_checkpoint_id(before) if before else None

        response = self._request(
            "GET",
            f"/v1/conversations/{conv_id}/entries",
            thread_id=thread_id,
            params={"channel": "memory"},
        )
        if response.status_code >= 400:
            return iter(())

        entries = response.json().get("data", [])
        checkpoints: list[dict[str, Any]] = []
        for entry in entries:
            if not isinstance(entry, dict):
                continue
            item = self._entry_content_item(entry)
            if not item or item.get("checkpoint_ns", "") != checkpoint_ns:
                continue
            if before_id and item.get("checkpoint_id") == before_id:
                continue
            if filter:
                metadata = self._decode_typed(item["metadata"])
                if not all(metadata.get(k) == v for k, v in filter.items()):
                    continue
            checkpoints.append(entry)

        checkpoints.sort(key=self._entry_sort_key, reverse=True)
        if limit is not None:
            checkpoints = checkpoints[:limit]
        return iter(
            [self._checkpoint_tuple_from_entry(thread_id, checkpoint_ns, e) for e in checkpoints]
        )

    def put(
        self,
        config: RunnableConfig,
        checkpoint: Checkpoint,
        metadata: CheckpointMetadata,
        new_versions: ChannelVersions,
    ) -> RunnableConfig:
        del new_versions
        thread_id: str = config["configurable"]["thread_id"]
        conv_id = self._conv_id(thread_id)
        checkpoint_ns: str = config["configurable"].get("checkpoint_ns", "")
        parent_checkpoint_id = get_checkpoint_id(config)

        payload = {
            "channel": "memory",
            "contentType": self.CHECKPOINT_CONTENT_TYPE,
            "content": [
                {
                    "checkpoint_id": checkpoint["id"],
                    "checkpoint_ns": checkpoint_ns,
                    "parent_checkpoint_id": parent_checkpoint_id,
                    "checkpoint": self._encode_typed(checkpoint),
                    "metadata": self._encode_typed(get_checkpoint_metadata(config, metadata)),
                }
            ],
        }

        response = self._request(
            "POST",
            f"/v1/conversations/{conv_id}/entries",
            thread_id=thread_id,
            json_body=payload,
        )
        if response.status_code == 404:
            fork_payload = self._payload_with_fork_metadata(payload)
            response = self._request(
                "POST",
                f"/v1/conversations/{conv_id}/entries",
                thread_id=thread_id,
                json_body=fork_payload,
            )
        if response.status_code == 404:
            # Auto-create the conversation with the derived UUID so subsequent
            # retries (and future calls) can find it by the same conv_id.
            self._request(
                "POST",
                "/v1/conversations",
                thread_id=thread_id,
                json_body={"id": conv_id, "title": f"Python checkpoint {thread_id}"},
            )
            response = self._request(
                "POST",
                f"/v1/conversations/{conv_id}/entries",
                thread_id=thread_id,
                json_body=payload,
            )
        if self._is_duplicate_conversation_error(response):
            response = self._request(
                "POST",
                f"/v1/conversations/{conv_id}/entries",
                thread_id=thread_id,
                json_body=payload,
            )
        if response.status_code >= 400:
            raise RuntimeError(response.text)

        return {
            "configurable": {
                "thread_id": thread_id,
                "checkpoint_ns": checkpoint_ns,
                "checkpoint_id": checkpoint["id"],
            }
        }

    def put_writes(
        self,
        config: RunnableConfig,
        writes: Sequence[tuple[str, Any]],
        task_id: str,
        task_path: str = "",
    ) -> None:
        del config, writes, task_id, task_path

    def delete_thread(self, thread_id: str) -> None:
        del thread_id

    async def aget_tuple(self, config: RunnableConfig) -> CheckpointTuple | None:
        return self.get_tuple(config)

    async def alist(
        self,
        config: RunnableConfig | None,
        *,
        filter: dict[str, Any] | None = None,
        before: RunnableConfig | None = None,
        limit: int | None = None,
    ) -> AsyncIterator[CheckpointTuple]:
        for item in self.list(config, filter=filter, before=before, limit=limit):
            yield item

    async def aput(
        self,
        config: RunnableConfig,
        checkpoint: Checkpoint,
        metadata: CheckpointMetadata,
        new_versions: ChannelVersions,
    ) -> RunnableConfig:
        return self.put(config, checkpoint, metadata, new_versions)

    async def aput_writes(
        self,
        config: RunnableConfig,
        writes: Sequence[tuple[str, Any]],
        task_id: str,
        task_path: str = "",
    ) -> None:
        self.put_writes(config, writes, task_id, task_path)

    async def adelete_thread(self, thread_id: str) -> None:
        self.delete_thread(thread_id)
