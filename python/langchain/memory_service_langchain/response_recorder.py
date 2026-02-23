from __future__ import annotations

import logging
import queue
import threading
import uuid
from collections.abc import Iterator, Sequence

import grpc

from .grpc.memory.v1 import memory_service_pb2, memory_service_pb2_grpc


LOG = logging.getLogger(__name__)


class BaseResponseRecorder:
    def record(self, content: str) -> None:
        raise NotImplementedError

    def complete(self) -> None:
        raise NotImplementedError


class NoopResponseRecorder(BaseResponseRecorder):
    def record(self, content: str) -> None:
        del content

    def complete(self) -> None:
        return


class GrpcResponseRecorder(BaseResponseRecorder):
    """Records response chunks to Memory Service via gRPC Record(stream)."""

    def __init__(
        self,
        *,
        target: str,
        conversation_id: str,
        metadata: Sequence[tuple[str, str]],
        timeout_seconds: float,
    ):
        self._conversation_id = conversation_id
        self._conversation_id_bytes = uuid.UUID(conversation_id).bytes
        self._metadata = tuple(metadata)
        self._timeout_seconds = timeout_seconds
        self._queue: queue.Queue[memory_service_pb2.RecordRequest | None] = queue.Queue()
        self._completed = threading.Event()
        self._finished = threading.Event()
        self._error: Exception | None = None
        self._channel = grpc.insecure_channel(target)
        self._stub = memory_service_pb2_grpc.ResponseRecorderServiceStub(self._channel)
        self._thread = threading.Thread(target=self._run, name="memory-service-recorder", daemon=True)
        self._thread.start()

    def _iter_requests(self) -> Iterator[memory_service_pb2.RecordRequest]:
        yield memory_service_pb2.RecordRequest(conversation_id=self._conversation_id_bytes)
        while True:
            item = self._queue.get()
            if item is None:
                break
            yield item

    def _run(self) -> None:
        try:
            self._stub.Record(
                self._iter_requests(),
                metadata=self._metadata,
                timeout=self._timeout_seconds,
            )
        except Exception as exc:  # pragma: no cover - non-deterministic transport failures
            self._error = exc
        finally:
            self._finished.set()

    def record(self, content: str) -> None:
        if self._completed.is_set():
            return
        if not content:
            return
        self._queue.put(memory_service_pb2.RecordRequest(content=content))

    def complete(self) -> None:
        if self._completed.is_set():
            return
        self._completed.set()
        self._queue.put(memory_service_pb2.RecordRequest(complete=True))
        self._queue.put(None)
        self._finished.wait(timeout=self._timeout_seconds + 2.0)
        self._channel.close()
        if self._error is not None:
            LOG.warning(
                "response recorder stream failed for conversation_id=%s: %s",
                self._conversation_id,
                self._error,
            )


def create_grpc_response_recorder(
    *,
    target: str,
    conversation_id: str,
    authorization: str | None,
    api_key: str | None,
    timeout_seconds: float,
) -> BaseResponseRecorder:
    metadata: list[tuple[str, str]] = []
    if authorization:
        metadata.append(("authorization", authorization))
    if api_key:
        metadata.append(("x-api-key", api_key))
    try:
        return GrpcResponseRecorder(
            target=target,
            conversation_id=conversation_id,
            metadata=metadata,
            timeout_seconds=timeout_seconds,
        )
    except Exception as exc:
        LOG.warning("failed to initialize gRPC response recorder: %s", exc)
        return NoopResponseRecorder()
