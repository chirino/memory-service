from __future__ import annotations

import asyncio
import contextlib
from dataclasses import dataclass, field
import json
import logging
import os
import threading
from typing import Any, AsyncIterator, Callable
from urllib.parse import urlparse
import uuid

import grpc

from .grpc.memory.v1 import memory_service_pb2, memory_service_pb2_grpc
from .request_context import get_request_authorization
from .response_recorder import BaseResponseRecorder, create_grpc_response_recorder


LOG = logging.getLogger("uvicorn.error")

_GRPC_FALLBACK_CODES = {
    grpc.StatusCode.UNIMPLEMENTED,
    grpc.StatusCode.NOT_FOUND,
    grpc.StatusCode.FAILED_PRECONDITION,
}

_GRPC_REPLAY_TERMINAL_CODES = {
    grpc.StatusCode.DEADLINE_EXCEEDED,
    grpc.StatusCode.CANCELLED,
}


def _grpc_port_from_url(parsed_url: Any) -> str:
    port = getattr(parsed_url, "port", None)
    if port is not None:
        return str(port)
    scheme = getattr(parsed_url, "scheme", "")
    if scheme == "https":
        return "443"
    return "80"


@dataclass
class _ResumeState:
    tokens: list[str]
    index: int = 0
    cancelled: bool = False
    complete: bool = False
    wake_event: asyncio.Event = field(default_factory=asyncio.Event)
    producer_task: asyncio.Task[None] | None = None


class MemoryServiceResponseResumer:
    """Response resumption helper with gRPC-backed replay/check/cancel."""

    def __init__(
        self,
        *,
        token_delay_seconds: float = 0.01,
        base_url: str | None = None,
        api_key: str | None = None,
        authorization_getter: Callable[[], str | None] | None = None,
        grpc_target: str | None = None,
        grpc_timeout_seconds: float | None = None,
        grpc_max_redirects: int | None = None,
    ):
        self._token_delay_seconds = token_delay_seconds
        self._states: dict[str, _ResumeState] = {}
        self._lock = threading.Lock()

        self._api_key = api_key or os.getenv("MEMORY_SERVICE_API_KEY", "agent-api-key-1")
        self._authorization_getter = authorization_getter or get_request_authorization

        resolved_base_url = (base_url or os.getenv("MEMORY_SERVICE_URL", "http://localhost:8082")).rstrip("/")
        parsed = urlparse(resolved_base_url)
        grpc_host = parsed.hostname or "localhost"

        configured_target = grpc_target or os.getenv("MEMORY_SERVICE_GRPC_TARGET")
        grpc_port = os.getenv("MEMORY_SERVICE_GRPC_PORT") or _grpc_port_from_url(parsed)
        self._grpc_target = configured_target or f"{grpc_host}:{grpc_port}"

        self._grpc_timeout_seconds = grpc_timeout_seconds or float(
            os.getenv("MEMORY_SERVICE_GRPC_TIMEOUT_SECONDS", "30")
        )
        replay_timeout_raw = os.getenv("MEMORY_SERVICE_GRPC_REPLAY_TIMEOUT_SECONDS")
        self._grpc_replay_timeout_seconds: float | None = None
        if replay_timeout_raw is not None and replay_timeout_raw.strip() != "":
            try:
                parsed_timeout = float(replay_timeout_raw)
                if parsed_timeout > 0:
                    self._grpc_replay_timeout_seconds = parsed_timeout
            except ValueError:
                LOG.warning(
                    "invalid MEMORY_SERVICE_GRPC_REPLAY_TIMEOUT_SECONDS=%s; replay timeout disabled",
                    replay_timeout_raw,
                )
        self._grpc_max_redirects = grpc_max_redirects or int(
            os.getenv("MEMORY_SERVICE_GRPC_MAX_REDIRECTS", "3")
        )

        LOG.info(
            "response resumer gRPC enabled target=%s timeout=%ss replay_timeout=%s",
            self._grpc_target,
            self._grpc_timeout_seconds,
            self._grpc_replay_timeout_seconds,
        )

    async def stream(self, conversation_id: str, text: str) -> AsyncIterator[str]:
        async def source() -> AsyncIterator[str]:
            tokens = text.split(" ")
            for index, token in enumerate(tokens):
                separator = " " if index < len(tokens) - 1 else ""
                yield token + separator
                await asyncio.sleep(self._token_delay_seconds)

        async for chunk in self.stream_from_source(conversation_id, source()):
            yield chunk

    async def stream_from_source(
        self, conversation_id: str, source: AsyncIterator[str]
    ) -> AsyncIterator[str]:
        state = _ResumeState(tokens=[])
        recorder = self._create_stream_recorder(conversation_id)
        producer_task = asyncio.create_task(
            self._produce_from_source(conversation_id, state, source, recorder)
        )
        cancel_watch_task = asyncio.create_task(
            self._watch_remote_cancel(conversation_id, state, recorder, producer_task)
        )
        with self._lock:
            state.producer_task = producer_task
            self._states[conversation_id] = state

        try:
            async for chunk in self._stream_state(state):
                yield chunk
        except asyncio.CancelledError:
            LOG.info(
                "response stream consumer disconnected conversation_id=%s; producer continues",
                conversation_id,
            )
            return
        finally:
            cancel_watch_task.cancel()
            with contextlib.suppress(asyncio.CancelledError):
                await cancel_watch_task
            if producer_task.done():
                try:
                    producer_task.result()
                except asyncio.CancelledError:
                    pass
                except Exception:
                    # Upstream source failures are intentionally non-fatal for clients.
                    pass

    async def _watch_remote_cancel(
        self,
        conversation_id: str,
        state: _ResumeState,
        recorder: BaseResponseRecorder,
        producer_task: asyncio.Task[None],
    ) -> None:
        while True:
            with self._lock:
                if state.complete:
                    return
            cancelled = await asyncio.to_thread(recorder.wait_for_cancel, 0.25)
            if cancelled:
                LOG.info(
                    "response stream remote cancel observed conversation_id=%s",
                    conversation_id,
                )
                with self._lock:
                    if state.complete:
                        return
                    state.cancelled = True
                    state.wake_event.set()
                if not producer_task.done():
                    producer_task.cancel()
                return
            await asyncio.sleep(0.05)

    def _create_stream_recorder(self, conversation_id: str) -> BaseResponseRecorder:
        authorization = (
            self._authorization_getter() if self._authorization_getter else None
        )
        return create_grpc_response_recorder(
            target=self._grpc_target,
            conversation_id=conversation_id,
            authorization=authorization,
            api_key=self._api_key,
            timeout_seconds=self._grpc_timeout_seconds,
        )

    def check(self, conversation_ids: list[str]) -> list[str]:
        return self._check_grpc(conversation_ids)

    def replay(self, conversation_id: str) -> AsyncIterator[str]:
        return self._replay_grpc(conversation_id)

    def replay_sse(
        self, conversation_id: str, *, stream_mode: str | None = None
    ) -> AsyncIterator[str]:
        stream = self.replay(conversation_id)
        replay_mode = self._normalize_stream_mode(stream_mode)

        async def source() -> AsyncIterator[str]:
            buffer = ""
            framing_mode: str | None = None  # "sse" | "jsonl"
            outbound_preview_parts: list[str] = []
            outbound_preview_remaining = 500
            outbound_preview_logged = False
            try:
                async for chunk in stream:
                    if not chunk:
                        continue

                    buffer += chunk
                    if framing_mode is None:
                        stripped = buffer.lstrip()
                        if stripped.startswith("data:"):
                            framing_mode = "sse"
                        elif stripped.startswith("{"):
                            framing_mode = "jsonl"

                    # gRPC replay is a byte stream and does not preserve write boundaries.
                    # Reframe into replay records before forwarding to the client.
                    while True:
                        separator = -1
                        if framing_mode == "jsonl":
                            separator = buffer.find("\n")
                        else:
                            separator = buffer.find("\n\n")
                        if separator < 0:
                            break
                        if framing_mode == "jsonl":
                            frame = buffer[:separator]
                            buffer = buffer[separator + 1 :]
                        else:
                            frame = buffer[:separator]
                            buffer = buffer[separator + 2 :]
                        formatted = self._format_replay_frame(frame, replay_mode)
                        if formatted is not None:
                            if outbound_preview_remaining > 0:
                                captured = formatted[:outbound_preview_remaining]
                                if captured:
                                    outbound_preview_parts.append(captured)
                                    outbound_preview_remaining -= len(captured)
                            if outbound_preview_remaining <= 0 and not outbound_preview_logged:
                                outbound_preview_logged = True
                                LOG.info(
                                    "response replay first 500 outbound chars conversation_id=%s chars=%r",
                                    conversation_id,
                                    "".join(outbound_preview_parts),
                                )
                            yield formatted

                if buffer:
                    formatted = self._format_replay_frame(buffer, replay_mode)
                    if formatted is not None:
                        if outbound_preview_remaining > 0:
                            captured = formatted[:outbound_preview_remaining]
                            if captured:
                                outbound_preview_parts.append(captured)
                                outbound_preview_remaining -= len(captured)
                        yield formatted
                if outbound_preview_parts and not outbound_preview_logged:
                    LOG.info(
                        "response replay outbound chars (short) conversation_id=%s chars=%r",
                        conversation_id,
                        "".join(outbound_preview_parts),
                    )
            except Exception as exc:
                code = _grpc_error_code(exc)
                if code in _GRPC_REPLAY_TERMINAL_CODES:
                    LOG.info(
                        "response replay ended for conversation_id=%s with gRPC status=%s",
                        conversation_id,
                        code,
                    )
                    return
                LOG.warning(
                    "response replay stream failed for conversation_id=%s: %s",
                    conversation_id,
                    exc,
                )
                return

        return source()

    def _format_replay_frame(self, frame: str, replay_mode: str) -> str | None:
        stripped = frame.strip()
        if not stripped:
            return None
        if stripped.startswith("data:"):
            if replay_mode != "events":
                # Preserve already framed SSE payloads for token/auto modes.
                return stripped + "\n\n"
            # In events mode, normalize legacy token frames into eventType payloads.
            normalized: list[str] = []
            for line in stripped.splitlines():
                line_stripped = line.strip()
                if not line_stripped.startswith("data:"):
                    continue
                payload = line_stripped[5:].strip()
                if not payload or payload == "[DONE]":
                    continue
                transformed = self._token_payload_to_event_payload(payload)
                normalized.append(f"data: {transformed}\n\n")
            if normalized:
                return "".join(normalized)
            return None
        return self._format_replay_chunk(frame, replay_mode)

    @staticmethod
    def _token_payload_to_event_payload(payload: str) -> str:
        try:
            parsed = json.loads(payload)
        except json.JSONDecodeError:
            return json.dumps(
                {"eventType": "PartialResponse", "chunk": payload},
                separators=(",", ":"),
            )

        if isinstance(parsed, dict):
            if "eventType" in parsed or "event" in parsed:
                return json.dumps(parsed, separators=(",", ":"))
            token = parsed.get("token")
            if isinstance(token, str):
                return json.dumps(
                    {"eventType": "PartialResponse", "chunk": token},
                    separators=(",", ":"),
                )

        if isinstance(parsed, str):
            return json.dumps(
                {"eventType": "PartialResponse", "chunk": parsed},
                separators=(",", ":"),
            )

        return json.dumps(
            {"eventType": "PartialResponse", "chunk": payload},
            separators=(",", ":"),
        )

    def _format_replay_chunk(self, chunk: str, replay_mode: str) -> str | None:
        stripped = chunk.strip()
        if not stripped:
            return None

        if replay_mode == "events":
            event_payload = self._event_payload_from_chunk(chunk)
            if event_payload is not None:
                return f"data: {event_payload}\n\n"
            fallback = json.dumps(
                {"eventType": "PartialResponse", "chunk": chunk},
                separators=(",", ":"),
            )
            return f"data: {fallback}\n\n"

        if replay_mode == "tokens":
            return f"data: {json.dumps({'token': chunk}, separators=(',', ':'))}\n\n"

        event_payload = self._event_payload_from_chunk(chunk)
        if event_payload is not None:
            return f"data: {event_payload}\n\n"
        return None

    @staticmethod
    def _event_payload_from_chunk(chunk: str) -> str | None:
        stripped = chunk.strip()
        if not stripped.startswith("{") or not stripped.endswith("}"):
            return None
        try:
            parsed = json.loads(stripped)
        except json.JSONDecodeError:
            return None
        if not isinstance(parsed, dict):
            return None
        if "eventType" not in parsed and "event" not in parsed:
            return None
        return json.dumps(parsed, separators=(",", ":"))

    @staticmethod
    def _normalize_stream_mode(value: str | None) -> str:
        if value is None:
            return "auto"
        normalized = value.strip().lower()
        if normalized in {"tokens", "events", "auto"}:
            return normalized
        return "auto"

    def cancel(self, conversation_id: str) -> None:
        self._cancel_local(conversation_id)
        self._cancel_grpc(conversation_id)

    def _check_grpc(self, conversation_ids: list[str]) -> list[str]:
        if not conversation_ids:
            return []

        id_bytes: list[bytes] = []
        for conversation_id in conversation_ids:
            try:
                id_bytes.append(self._uuid_to_bytes(conversation_id))
            except ValueError:
                continue

        if not id_bytes:
            return []

        request = memory_service_pb2.CheckRecordingsRequest(conversation_ids=id_bytes)
        channel = grpc.insecure_channel(self._grpc_target)
        try:
            stub = memory_service_pb2_grpc.ResponseRecorderServiceStub(channel)
            response = stub.CheckRecordings(
                request,
                metadata=self._metadata(),
                timeout=self._grpc_timeout_seconds,
            )
        except grpc.RpcError as exc:
            if exc.code() in _GRPC_FALLBACK_CODES:
                return []
            LOG.warning("response recorder check failed: %s", exc)
            return []
        except Exception as exc:  # pragma: no cover - transport/environment dependent
            LOG.warning("response recorder check failed: %s", exc)
            return []
        finally:
            channel.close()

        active: list[str] = []
        for raw_id in response.conversation_ids:
            parsed = self._uuid_from_bytes(raw_id)
            if parsed:
                active.append(parsed)
        LOG.debug(
            "response recorder check target=%s requested=%d active=%d",
            self._grpc_target,
            len(conversation_ids),
            len(active),
        )
        return active

    def _replay_grpc(self, conversation_id: str) -> AsyncIterator[str]:
        conversation_id_bytes = self._uuid_to_bytes(conversation_id)
        metadata = self._metadata()

        async def source() -> AsyncIterator[str]:
            target = self._grpc_target
            redirects_remaining = self._grpc_max_redirects
            first_chars_parts: list[str] = []
            first_chars_remaining = 500
            first_chars_logged = False
            LOG.info(
                "response replay started conversation_id=%s target=%s replay_timeout=%s",
                conversation_id,
                target,
                self._grpc_replay_timeout_seconds,
            )
            while True:
                channel = grpc.aio.insecure_channel(target)
                try:
                    stub = memory_service_pb2_grpc.ResponseRecorderServiceStub(channel)
                    replay_request = memory_service_pb2.ReplayRequest(
                        conversation_id=conversation_id_bytes
                    )
                    if self._grpc_replay_timeout_seconds is None:
                        stream = stub.Replay(
                            replay_request,
                            metadata=metadata,
                        )
                    else:
                        stream = stub.Replay(
                            replay_request,
                            metadata=metadata,
                            timeout=self._grpc_replay_timeout_seconds,
                        )

                    redirected_to: str | None = None
                    async for response in stream:
                        if response.redirect_address:
                            redirected_to = self._validate_redirect_target(
                                response.redirect_address
                            )
                            break
                        if response.content:
                            if first_chars_remaining > 0:
                                captured = response.content[:first_chars_remaining]
                                if captured:
                                    first_chars_parts.append(captured)
                                    first_chars_remaining -= len(captured)
                            if first_chars_remaining <= 0 and not first_chars_logged:
                                first_chars_logged = True
                                LOG.info(
                                    "response replay first 500 chars conversation_id=%s target=%s chars=%r",
                                    conversation_id,
                                    target,
                                    "".join(first_chars_parts),
                                )
                            yield response.content

                    if redirected_to is None:
                        if first_chars_parts and not first_chars_logged:
                            first_chars_logged = True
                            LOG.info(
                                "response replay first chars (short) conversation_id=%s target=%s chars=%r",
                                conversation_id,
                                target,
                                "".join(first_chars_parts),
                            )
                        LOG.info(
                            "response replay completed conversation_id=%s target=%s",
                            conversation_id,
                            target,
                        )
                        return
                    if redirects_remaining <= 0:
                        raise RuntimeError("too many gRPC replay redirects")
                    LOG.info(
                        "response replay redirect conversation_id=%s from=%s to=%s remaining=%d",
                        conversation_id,
                        target,
                        redirected_to,
                        redirects_remaining - 1,
                    )
                    redirects_remaining -= 1
                    target = redirected_to
                except Exception as exc:
                    code = _grpc_error_code(exc)
                    if code in _GRPC_FALLBACK_CODES:
                        LOG.info(
                            "response replay unsupported conversation_id=%s target=%s status=%s",
                            conversation_id,
                            target,
                            code,
                        )
                        return
                    if code in _GRPC_REPLAY_TERMINAL_CODES:
                        LOG.info(
                            "response replay ended conversation_id=%s target=%s status=%s",
                            conversation_id,
                            target,
                            code,
                        )
                        return
                    LOG.warning(
                        "response replay failed conversation_id=%s target=%s status=%s error=%s",
                        conversation_id,
                        target,
                        code,
                        exc,
                    )
                    raise
                finally:
                    await channel.close()

        return source()

    def _cancel_local(self, conversation_id: str) -> None:
        producer_task: asyncio.Task[None] | None = None
        with self._lock:
            state = self._states.get(conversation_id)
            if state is None or state.complete:
                return
            state.cancelled = True
            state.wake_event.set()
            producer_task = state.producer_task
        LOG.info(
            "response stream local cancel requested conversation_id=%s producer_task_running=%s",
            conversation_id,
            bool(producer_task is not None and not producer_task.done()),
        )
        if producer_task is not None and not producer_task.done():
            producer_task.cancel()

    def _cancel_grpc(self, conversation_id: str) -> None:
        try:
            conversation_id_bytes = self._uuid_to_bytes(conversation_id)
        except ValueError:
            return

        request = memory_service_pb2.CancelRecordRequest(conversation_id=conversation_id_bytes)
        metadata = self._metadata()
        target = self._grpc_target
        redirects_remaining = self._grpc_max_redirects

        while True:
            channel = grpc.insecure_channel(target)
            try:
                stub = memory_service_pb2_grpc.ResponseRecorderServiceStub(channel)
                response = stub.Cancel(
                    request,
                    metadata=metadata,
                    timeout=self._grpc_timeout_seconds,
                )
            except grpc.RpcError as exc:
                if exc.code() in _GRPC_FALLBACK_CODES:
                    return
                LOG.warning(
                    "response recorder cancel failed for conversation_id=%s: %s",
                    conversation_id,
                    exc,
                )
                return
            except Exception as exc:  # pragma: no cover - transport/environment dependent
                LOG.warning(
                    "response recorder cancel failed for conversation_id=%s: %s",
                    conversation_id,
                    exc,
                )
                return
            finally:
                channel.close()

            if response.redirect_address:
                if redirects_remaining <= 0:
                    LOG.warning(
                        "response recorder cancel failed for conversation_id=%s: too many redirects",
                        conversation_id,
                    )
                    return
                target = self._validate_redirect_target(response.redirect_address)
                redirects_remaining -= 1
                continue

            if not response.accepted:
                LOG.warning(
                    "response recorder cancel was not accepted for conversation_id=%s",
                    conversation_id,
                )
            return

    def _metadata(self) -> tuple[tuple[str, str], ...]:
        headers: list[tuple[str, str]] = []
        authorization = (
            self._authorization_getter() if self._authorization_getter else None
        )
        if authorization:
            headers.append(("authorization", authorization))
        if self._api_key:
            headers.append(("x-api-key", self._api_key))
        return tuple(headers)

    @staticmethod
    def _uuid_to_bytes(conversation_id: str) -> bytes:
        return uuid.UUID(conversation_id).bytes

    @staticmethod
    def _uuid_from_bytes(value: bytes) -> str | None:
        try:
            return str(uuid.UUID(bytes=bytes(value)))
        except ValueError:
            return None

    @staticmethod
    def _validate_redirect_target(address: str) -> str:
        colon_index = address.rfind(":")
        if colon_index <= 0:
            raise RuntimeError(f"invalid gRPC redirect address: {address}")
        host = address[:colon_index].strip()
        try:
            port = int(address[colon_index + 1 :].strip())
        except ValueError as exc:
            raise RuntimeError(f"invalid gRPC redirect address: {address}") from exc
        if not host or port <= 0:
            raise RuntimeError(f"invalid gRPC redirect address: {address}")
        return f"{host}:{port}"


    async def _produce_from_source(
        self,
        conversation_id: str,
        state: _ResumeState,
        source: AsyncIterator[str],
        recorder: BaseResponseRecorder,
    ) -> None:
        try:
            async for chunk in source:
                with self._lock:
                    if state.cancelled:
                        LOG.info(
                            "response stream producer observed cancelled state conversation_id=%s",
                            conversation_id,
                        )
                        state.complete = True
                        state.wake_event.set()
                        return
                    if chunk:
                        for recorded in self._record_contents(chunk):
                            recorder.record(recorded)
                        state.tokens.append(chunk)
                    state.wake_event.set()
        except asyncio.CancelledError:
            LOG.info(
                "response stream producer task cancelled conversation_id=%s",
                conversation_id,
            )
            raise
        except Exception:
            # Do not fail downstream consumers if upstream generation errors.
            pass
        finally:
            recorder.complete()
            with self._lock:
                state.complete = True
                state.producer_task = None
                state.wake_event.set()
                if self._states.get(conversation_id) is state:
                    self._states.pop(conversation_id, None)

    def _record_contents(self, chunk: str) -> list[str]:
        stripped = chunk.strip()
        if not stripped:
            return []

        # SSE chunks use "data: <payload>" lines; record payloads only.
        if "data:" in chunk:
            recorded: list[str] = []
            for line in chunk.splitlines():
                line_stripped = line.strip()
                if not line_stripped.startswith("data:"):
                    continue
                payload = line_stripped[5:].strip()
                if not payload or payload == "[DONE]":
                    continue
                recorded.extend(self._normalize_record_payload(payload))
            return recorded

        return self._normalize_record_payload(stripped)

    @staticmethod
    def _normalize_record_payload(payload: str) -> list[str]:
        try:
            parsed = json.loads(payload)
        except json.JSONDecodeError:
            return [payload]

        if isinstance(parsed, dict):
            # Quarkus-style rich event recording: JSON line per event.
            if "eventType" in parsed or "event" in parsed:
                return [json.dumps(parsed, separators=(",", ":")) + "\n"]
            # Token frame payloads record the token text itself.
            token = parsed.get("token")
            if isinstance(token, str):
                return [token]
            return [payload]

        if isinstance(parsed, str):
            return [parsed]

        return [payload]

    async def _stream_state(self, state: _ResumeState) -> AsyncIterator[str]:
        while True:
            token: str | None = None
            wait_event: asyncio.Event | None = None
            with self._lock:
                if state.cancelled:
                    state.complete = True
                    return

                if state.index < len(state.tokens):
                    token = state.tokens[state.index]
                    state.index += 1
                elif state.complete:
                    return
                else:
                    wait_event = state.wake_event

            if token is not None:
                yield token
                continue

            if wait_event is None:
                return

            await wait_event.wait()
            wait_event.clear()

def _grpc_error_code(exc: Exception) -> grpc.StatusCode | None:
    code_method = getattr(exc, "code", None)
    if not callable(code_method):
        return None
    try:
        code = code_method()
    except Exception:
        return None
    if isinstance(code, grpc.StatusCode):
        return code
    return None
