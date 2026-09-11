"""Test that list_conversations encodes metadata as repeated query parameters."""

from __future__ import annotations

import asyncio
import unittest
from unittest.mock import patch

import httpx


class TestProxyMetadataEncoding(unittest.TestCase):
    def test_two_metadata_expressions_encode_as_repeated_params(self) -> None:
        captured: list[httpx.Request] = []

        def handler(request: httpx.Request) -> httpx.Response:
            captured.append(request)
            return httpx.Response(200, json={"conversations": []})

        transport = httpx.MockTransport(handler)

        from memory_service_langchain.proxy import MemoryServiceProxy

        proxy = MemoryServiceProxy(base_url="http://localhost", api_key="test")

        async def mock_request(
            method: str,
            path: str,
            *,
            params: dict | None = None,
            **kwargs,
        ) -> httpx.Response:
            async with httpx.AsyncClient(
                base_url="http://localhost", transport=transport
            ) as client:
                return await client.request(method, path, params=params)

        with patch.object(proxy, "_request", side_effect=mock_request):
            asyncio.run(
                proxy.list_conversations(
                    metadata=["status=waiting", "agent-id=worker-1"]
                )
            )

        self.assertEqual(len(captured), 1)
        self.assertEqual(
            captured[0].url.params.get_list("metadata"),
            ["status=waiting", "agent-id=worker-1"],
        )


if __name__ == "__main__":
    unittest.main()
