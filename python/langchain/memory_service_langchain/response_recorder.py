from __future__ import annotations

import logging
import queue
import threading
import uuid
from collections.abc import Iterator, Sequence

import grpc

from .grpc.memory.v1 import memory_service_pb2, memory_service_pb2_grpc


LOG = logging.getLogger("uvicorn.error")


class BaseResponseRecorder:
    def record(self, content: str) -> None:
        raise NotImplementedError

    def complete(self) -> None:
        raise NotImplementedError

    def is_cancelled(self) -> bool:
        return False

    def wait_for_cancel(self, timeout: float | None = None) -> bool:
        del timeout
        return False


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
        self._target = target
        self._conversation_id = conversation_id
        self._conversation_id_bytes = uuid.UUID(conversation_id).bytes
        self._metadata = tuple(metadata)
        self._timeout_seconds = timeout_seconds
        self._queue: queue.Queue[memory_service_pb2.RecordRequest | None] = queue.Queue()
        self._completed = threading.Event()
        self._finished = threading.Event()
        self._cancelled = threading.Event()
        self._registered = threading.Event()
        self._start_lock = threading.Lock()
        self._error: Exception | None = None
        self._channel: grpc.Channel | None = None
        self._stub: memory_service_pb2_grpc.ResponseRecorderServiceStub | None = None
        self._thread: threading.Thread | None = None
        self._ensure_started()

    def _iter_requests(self) -> Iterator[memory_service_pb2.RecordRequest]:
        self._registered.set()
        yield memory_service_pb2.RecordRequest(conversation_id=self._conversation_id_bytes)
        while True:
            item = self._queue.get()
            if item is None:
                break
            yield item

    def _ensure_started(self) -> None:
        if self._thread is not None or self._completed.is_set():
            return

        with self._start_lock:
            if self._thread is not None or self._completed.is_set():
                return
            try:
                self._channel = grpc.insecure_channel(self._target)
                self._stub = memory_service_pb2_grpc.ResponseRecorderServiceStub(
                    self._channel
                )
                LOG.info(
                    "response recorder started conversation_id=%s target=%s",
                    self._conversation_id,
                    self._target,
                )
                self._thread = threading.Thread(
                    target=self._run, name="memory-service-recorder", daemon=True
                )
                self._thread.start()
                if not self._registered.wait(timeout=1.0):
                    LOG.warning(
                        "gRPC response recorder did not start consuming initial registration promptly for conversation_id=%s",
                        self._conversation_id,
                    )
            except Exception as exc:
                self._error = exc
                self._completed.set()
                LOG.warning(
                    "failed to start gRPC response recorder for conversation_id=%s: %s",
                    self._conversation_id,
                    exc,
                )

    def _run(self) -> None:
        stub = self._stub
        if stub is None:
            self._finished.set()
            return
        try:
            response = stub.Record(
                self._iter_requests(),
                metadata=self._metadata,
            )
            # Proto enum RecordStatus: CANCELLED = 2.
            if getattr(response, "status", 0) == 2:
                self._cancelled.set()
                LOG.info(
                    "response recorder received remote cancel conversation_id=%s",
                    self._conversation_id,
                )
        except Exception as exc:  # pragma: no cover - non-deterministic transport failures
            self._error = exc
        finally:
            self._finished.set()

    def record(self, content: str) -> None:
        if self._completed.is_set():
            return
        if self._cancelled.is_set():
            return
        if not content:
            return
        self._ensure_started()
        if self._completed.is_set():
            return
        self._queue.put(memory_service_pb2.RecordRequest(content=content))

    def complete(self) -> None:
        if self._completed.is_set():
            return
        self._completed.set()

        if self._thread is None:
            if self._error is not None:
                LOG.warning(
                    "response recorder stream failed for conversation_id=%s: %s",
                    self._conversation_id,
                    self._error,
                )
            return

        if self._cancelled.is_set():
            if self._channel is not None:
                self._channel.close()
            LOG.info(
                "response recorder closed after remote cancel conversation_id=%s",
                self._conversation_id,
            )
            return

        self._queue.put(memory_service_pb2.RecordRequest(complete=True))
        self._queue.put(None)
        finished = self._finished.wait(timeout=self._timeout_seconds + 2.0)
        if not finished:
            LOG.warning(
                "response recorder completion wait timed out conversation_id=%s wait_seconds=%.1f",
                self._conversation_id,
                self._timeout_seconds + 2.0,
            )
        if self._channel is not None:
            self._channel.close()
        if self._error is None:
            if self._cancelled.is_set():
                LOG.info(
                    "response recorder completed with cancel conversation_id=%s",
                    self._conversation_id,
                )
            else:
                LOG.info(
                    "response recorder completed conversation_id=%s",
                    self._conversation_id,
                )
        if self._error is not None:
            LOG.warning(
                "response recorder stream failed for conversation_id=%s: %s",
                self._conversation_id,
                self._error,
            )

    def is_cancelled(self) -> bool:
        return self._cancelled.is_set()

    def wait_for_cancel(self, timeout: float | None = None) -> bool:
        return self._cancelled.wait(timeout=timeout)


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
