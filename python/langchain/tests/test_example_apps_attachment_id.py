"""Test that the Python demo apps forward the caller-supplied attachment ID exactly as received.

A blank ``id`` form field must reach the proxy as an empty string. Dropping it would silently turn
an idempotent retry into a server-generated ID, which the server's blank-ID rejection exists to
prevent.
"""

from __future__ import annotations

import importlib.util
import sys
import types
import unittest
from pathlib import Path
from typing import Any

import httpx
from fastapi.testclient import TestClient

EXAMPLES = Path(__file__).resolve().parents[2] / "examples"
APPS = {
    "langchain": EXAMPLES / "langchain" / "chat-langchain" / "app.py",
    "langgraph": EXAMPLES / "langgraph" / "chat-langgraph" / "app.py",
}


class _RecordingProxy:
    def __init__(self) -> None:
        self.calls: list[dict[str, Any]] = []

    async def upload_attachment(self, **kwargs: Any) -> Any:
        self.calls.append(kwargs)
        return httpx.Response(201, json={})


def _load_app(name: str, path: Path) -> types.ModuleType:
    # The demo apps import langchain_openai at module level; the model is never invoked here.
    if "langchain_openai" not in sys.modules:
        stub = types.ModuleType("langchain_openai")

        class ChatOpenAI:
            def __init__(self, *args: Any, **kwargs: Any) -> None:
                pass

        stub.ChatOpenAI = ChatOpenAI  # type: ignore[attr-defined]
        sys.modules["langchain_openai"] = stub
    spec = importlib.util.spec_from_file_location(f"example_app_{name}", path)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class ExampleAppAttachmentIdTest(unittest.TestCase):
    def _upload(self, name: str, form: dict[str, str]) -> list[dict[str, Any]]:
        module = _load_app(name, APPS[name])
        recorder = _RecordingProxy()
        module.proxy = recorder
        client = TestClient(module.app)
        response = client.post(
            "/v1/attachments",
            files={"file": ("a.txt", b"hello", "text/plain")},
            data=form,
        )
        self.assertEqual(201, response.status_code, response.text)
        return recorder.calls

    def test_blank_id_is_forwarded_as_blank(self) -> None:
        for name in APPS:
            with self.subTest(app=name):
                calls = self._upload(name, {"id": ""})
                self.assertEqual(1, len(calls))
                self.assertEqual("", calls[0]["attachment_id"])

    def test_supplied_id_is_forwarded(self) -> None:
        for name in APPS:
            with self.subTest(app=name):
                calls = self._upload(name, {"id": "11111111-1111-4111-8111-111111111111"})
                self.assertEqual(1, len(calls))
                self.assertEqual("11111111-1111-4111-8111-111111111111", calls[0]["attachment_id"])

    def test_missing_id_is_forwarded_as_none(self) -> None:
        for name in APPS:
            with self.subTest(app=name):
                calls = self._upload(name, {})
                self.assertEqual(1, len(calls))
                self.assertIsNone(calls[0]["attachment_id"])


if __name__ == "__main__":
    unittest.main()
