"""Test that the attachment proxy methods forward the caller-supplied attachment ID."""

from __future__ import annotations

import asyncio
import unittest
from typing import Any

import httpx

from memory_service_langchain.proxy import MemoryServiceProxy


def _run_through_mock_transport(
    call: Any, captured: list[httpx.Request], status: int = 200
) -> httpx.Response:
    """Run an async proxy call against a transport that records the request it receives."""

    def handler(request: httpx.Request) -> httpx.Response:
        captured.append(request)
        return httpx.Response(status, json={})

    transport = httpx.MockTransport(handler)
    proxy = MemoryServiceProxy(base_url="http://localhost", api_key="test")

    async def mock_request(
        method: str,
        path: str,
        *,
        params: dict | None = None,
        json_body: Any | None = None,
        data: dict | None = None,
        files: dict | None = None,
        **kwargs: Any,
    ) -> httpx.Response:
        async with httpx.AsyncClient(base_url="http://localhost", transport=transport) as client:
            return await client.request(
                method, path, params=params, json=json_body, data=data, files=files
            )

    proxy._request = mock_request  # type: ignore[method-assign]
    return asyncio.run(call(proxy))


class TestProxyAttachmentIds(unittest.TestCase):
    def test_upload_with_id_sends_id_field_before_file_part(self) -> None:
        captured: list[httpx.Request] = []
        attachment_id = "6f1c1d0e-8d3a-4a77-9d57-2a2c9c6d3a10"

        _run_through_mock_transport(
            lambda proxy: proxy.upload_attachment(
                file_name="a.txt",
                file_bytes=b"hello",
                content_type="text/plain",
                attachment_id=attachment_id,
            ),
            captured,
        )

        body = captured[0].read().decode()
        self.assertIn('name="id"\r\n\r\n' + attachment_id + "\r\n", body)
        self.assertLess(body.index('name="id"'), body.index('name="file"'))

    def test_upload_without_id_sends_no_id_field(self) -> None:
        captured: list[httpx.Request] = []

        _run_through_mock_transport(
            lambda proxy: proxy.upload_attachment(
                file_name="a.txt", file_bytes=b"hello", content_type="text/plain"
            ),
            captured,
        )

        body = captured[0].read().decode()
        self.assertNotIn('name="id"', body)
        self.assertIn('name="file"', body)

    def test_upload_sends_a_blank_id_unchanged_instead_of_dropping_it(self) -> None:
        captured: list[httpx.Request] = []

        _run_through_mock_transport(
            lambda proxy: proxy.upload_attachment(
                file_name="a.txt", file_bytes=b"hello", attachment_id=""
            ),
            captured,
        )

        # A blank ID must reach the memory service, which rejects it with 400. Dropping it would
        # silently turn the request into a non-idempotent upload.
        self.assertIn('name="id"\r\n\r\n\r\n', captured[0].read().decode())

    def test_upload_with_the_same_id_sends_the_same_id_on_every_attempt(self) -> None:
        captured: list[httpx.Request] = []
        attachment_id = "6f1c1d0e-8d3a-4a77-9d57-2a2c9c6d3a10"

        for _ in range(2):
            _run_through_mock_transport(
                lambda proxy: proxy.upload_attachment(
                    file_name="a.txt", file_bytes=b"hello", attachment_id=attachment_id
                ),
                captured,
            )

        self.assertEqual(len(captured), 2)
        for request in captured:
            self.assertIn('name="id"\r\n\r\n' + attachment_id + "\r\n", request.read().decode())

    def test_conflict_status_is_returned_not_hidden(self) -> None:
        captured: list[httpx.Request] = []

        response = _run_through_mock_transport(
            lambda proxy: proxy.upload_attachment(
                file_name="a.txt",
                file_bytes=b"hello",
                attachment_id="6f1c1d0e-8d3a-4a77-9d57-2a2c9c6d3a10",
            ),
            captured,
            status=409,
        )

        self.assertEqual(response.status_code, 409)

    def test_create_from_url_forwards_the_payload_id_unchanged(self) -> None:
        captured: list[httpx.Request] = []
        payload = {
            "sourceUrl": "https://example.com/a.png",
            "contentType": "image/png",
            "id": "6f1c1d0e-8d3a-4a77-9d57-2a2c9c6d3a10",
        }

        _run_through_mock_transport(
            lambda proxy: proxy.create_attachment_from_url(payload), captured
        )

        self.assertEqual(captured[0].method, "POST")
        self.assertEqual(captured[0].url.path, "/v1/attachments")
        import json

        self.assertEqual(json.loads(captured[0].read()), payload)

    def test_get_attachment_metadata_requests_the_metadata_route(self) -> None:
        captured: list[httpx.Request] = []

        _run_through_mock_transport(
            lambda proxy: proxy.get_attachment_metadata("6f1c1d0e-8d3a-4a77-9d57-2a2c9c6d3a10"),
            captured,
        )

        self.assertEqual(captured[0].method, "GET")
        self.assertEqual(
            captured[0].url.path,
            "/v1/attachments/6f1c1d0e-8d3a-4a77-9d57-2a2c9c6d3a10/metadata",
        )

    def test_get_attachment_metadata_returns_not_found_unchanged(self) -> None:
        captured: list[httpx.Request] = []

        response = _run_through_mock_transport(
            lambda proxy: proxy.get_attachment_metadata("6f1c1d0e-8d3a-4a77-9d57-2a2c9c6d3a10"),
            captured,
            status=404,
        )

        self.assertEqual(response.status_code, 404)


if __name__ == "__main__":
    unittest.main()
