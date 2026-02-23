from __future__ import annotations

import asyncio
from dataclasses import dataclass, field
import threading
from typing import AsyncIterator


@dataclass
class _ResumeState:
    tokens: list[str]
    index: int = 0
    cancelled: bool = False
    complete: bool = False
    wake_event: asyncio.Event = field(default_factory=asyncio.Event)


class MemoryServiceResponseResumer:
    """Small in-memory helper used by docs checkpoints for resume/replay/cancel."""

    def __init__(self, *, token_delay_seconds: float = 0.01):
        self._token_delay_seconds = token_delay_seconds
        self._states: dict[str, _ResumeState] = {}
        self._lock = threading.Lock()

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
        with self._lock:
            self._states[conversation_id] = state

        asyncio.create_task(self._produce_from_source(state, source))
        async for chunk in self._stream_state(state):
            yield chunk

    def check(self, conversation_ids: list[str]) -> list[str]:
        in_progress: list[str] = []
        with self._lock:
            for conversation_id in conversation_ids:
                state = self._states.get(conversation_id)
                if state is None:
                    continue
                if state.complete or state.cancelled:
                    continue
                in_progress.append(conversation_id)
        return in_progress

    def replay(self, conversation_id: str) -> AsyncIterator[str]:
        with self._lock:
            state = self._states.get(conversation_id)
            if state is None or state.complete or state.cancelled:
                raise KeyError(conversation_id)
        return self._stream_state(state)

    def cancel(self, conversation_id: str) -> None:
        with self._lock:
            state = self._states.get(conversation_id)
            if state is None or state.complete:
                return
            state.cancelled = True
            state.wake_event.set()

    async def _produce_from_source(
        self, state: _ResumeState, source: AsyncIterator[str]
    ) -> None:
        try:
            async for chunk in source:
                with self._lock:
                    if state.cancelled:
                        state.complete = True
                        state.wake_event.set()
                        return
                    if chunk:
                        state.tokens.append(chunk)
                    state.wake_event.set()
        except Exception:
            # Do not fail downstream consumers if upstream generation errors.
            pass
        finally:
            with self._lock:
                state.complete = True
                state.wake_event.set()

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
