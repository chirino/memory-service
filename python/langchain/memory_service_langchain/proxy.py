from __future__ import annotations

from typing import Any, Callable

import httpx
from fastapi import Response
from fastapi.responses import JSONResponse, PlainTextResponse

from .request_context import get_request_authorization, memory_service_request


def to_fastapi_response(response: httpx.Response) -> Response:
    passthrough_headers = {}
    for header_name in (
        "cache-control",
        "content-disposition",
        "etag",
        "expires",
        "last-modified",
        "location",
        "pragma",
    ):
        header_value = response.headers.get(header_name)
        if header_value is not None:
            passthrough_headers[header_name] = header_value

    content_type = response.headers.get("content-type", "")
    if "application/json" in content_type:
        return JSONResponse(
            response.json(),
            status_code=response.status_code,
            headers=passthrough_headers,
        )

    media_type = content_type.split(";", 1)[0] if content_type else None
    if media_type and media_type.startswith("text/"):
        return PlainTextResponse(
            response.text,
            status_code=response.status_code,
            headers=passthrough_headers,
            media_type=media_type,
        )
    return Response(
        content=response.content,
        status_code=response.status_code,
        media_type=media_type,
        headers=passthrough_headers,
    )


class MemoryServiceProxy:
    def __init__(
        self,
        *,
        base_url: str | None = None,
        api_key: str | None = None,
        authorization_getter: Callable[[], str | None] | None = None,
    ):
        self.base_url = base_url
        self.api_key = api_key
        self.authorization_getter = authorization_getter or get_request_authorization

    @staticmethod
    def _compact_params(params: dict[str, Any]) -> dict[str, Any]:
        return {k: v for k, v in params.items() if v is not None}

    async def _request(
        self,
        method: str,
        path: str,
        *,
        params: dict[str, Any] | None = None,
        json_body: Any | None = None,
        content: bytes | str | None = None,
        data: dict[str, Any] | None = None,
        files: dict[str, Any] | None = None,
        include_api_key: bool = True,
        include_authorization: bool = True,
        extra_headers: dict[str, str] | None = None,
    ) -> httpx.Response:
        return await memory_service_request(
            method,
            path,
            params=params,
            json_body=json_body,
            content=content,
            data=data,
            files=files,
            base_url=self.base_url,
            api_key=self.api_key,
            authorization_getter=self.authorization_getter,
            include_api_key=include_api_key,
            include_authorization=include_authorization,
            extra_headers=extra_headers,
        )

    async def list_conversations(
        self,
        *,
        mode: str | None = None,
        after_cursor: str | None = None,
        limit: int | None = None,
        query: str | None = None,
    ) -> httpx.Response:
        return await self._request(
            "GET",
            "/v1/conversations",
            params=self._compact_params(
                {
                    "mode": mode,
                    "afterCursor": after_cursor,
                    "limit": limit,
                    "query": query,
                }
            ),
        )

    async def get_conversation(self, conversation_id: str) -> httpx.Response:
        return await self._request("GET", f"/v1/conversations/{conversation_id}")

    async def update_conversation(self, conversation_id: str, payload: dict[str, Any]) -> httpx.Response:
        return await self._request(
            "PATCH",
            f"/v1/conversations/{conversation_id}",
            json_body=payload,
        )

    async def delete_conversation(self, conversation_id: str) -> httpx.Response:
        return await self._request("DELETE", f"/v1/conversations/{conversation_id}")

    async def list_conversation_entries(
        self,
        conversation_id: str,
        *,
        after_cursor: str | None = None,
        limit: int | None = None,
        channel: str | None = None,
        epoch: str | None = None,
        forks: str | None = None,
    ) -> httpx.Response:
        return await self._request(
            "GET",
            f"/v1/conversations/{conversation_id}/entries",
            params=self._compact_params(
                {
                    "afterCursor": after_cursor,
                    "limit": limit,
                    "channel": channel,
                    "epoch": epoch,
                    "forks": forks,
                }
            ),
        )

    async def list_conversation_forks(
        self,
        conversation_id: str,
        *,
        after_cursor: str | None = None,
        limit: int | None = None,
    ) -> httpx.Response:
        return await self._request(
            "GET",
            f"/v1/conversations/{conversation_id}/forks",
            params=self._compact_params(
                {
                    "afterCursor": after_cursor,
                    "limit": limit,
                }
            ),
        )

    async def search_conversations(self, payload: dict[str, Any]) -> httpx.Response:
        return await self._request(
            "POST",
            "/v1/conversations/search",
            json_body=payload,
        )

    async def list_memberships(self, conversation_id: str) -> httpx.Response:
        return await self._request(
            "GET",
            f"/v1/conversations/{conversation_id}/memberships",
        )

    async def create_membership(self, conversation_id: str, payload: dict[str, Any]) -> httpx.Response:
        return await self._request(
            "POST",
            f"/v1/conversations/{conversation_id}/memberships",
            json_body=payload,
        )

    async def update_membership(
        self,
        conversation_id: str,
        user_id: str,
        payload: dict[str, Any],
    ) -> httpx.Response:
        return await self._request(
            "PATCH",
            f"/v1/conversations/{conversation_id}/memberships/{user_id}",
            json_body=payload,
        )

    async def delete_membership(self, conversation_id: str, user_id: str) -> httpx.Response:
        return await self._request(
            "DELETE",
            f"/v1/conversations/{conversation_id}/memberships/{user_id}",
        )

    async def list_ownership_transfers(
        self,
        *,
        role: str | None = None,
        after_cursor: str | None = None,
        limit: int | None = None,
    ) -> httpx.Response:
        return await self._request(
            "GET",
            "/v1/ownership-transfers",
            params=self._compact_params(
                {
                    "role": role,
                    "afterCursor": after_cursor,
                    "limit": limit,
                }
            ),
        )

    async def create_ownership_transfer(self, payload: dict[str, Any]) -> httpx.Response:
        return await self._request(
            "POST",
            "/v1/ownership-transfers",
            json_body=payload,
        )

    async def accept_ownership_transfer(self, transfer_id: str) -> httpx.Response:
        return await self._request(
            "POST",
            f"/v1/ownership-transfers/{transfer_id}/accept",
        )

    async def delete_ownership_transfer(self, transfer_id: str) -> httpx.Response:
        return await self._request(
            "DELETE",
            f"/v1/ownership-transfers/{transfer_id}",
        )

    async def get_ownership_transfer(self, transfer_id: str) -> httpx.Response:
        return await self._request(
            "GET",
            f"/v1/ownership-transfers/{transfer_id}",
        )

    async def cancel_response(self, conversation_id: str) -> httpx.Response:
        return await self._request(
            "DELETE",
            f"/v1/conversations/{conversation_id}/response",
        )

    async def index_conversations(self, payload: list[dict[str, Any]]) -> httpx.Response:
        return await self._request(
            "POST",
            "/v1/conversations/index",
            json_body=payload,
        )

    async def upload_attachment(
        self,
        *,
        file_name: str,
        file_bytes: bytes,
        content_type: str = "application/octet-stream",
        expires_in: str | None = None,
    ) -> httpx.Response:
        return await self._request(
            "POST",
            "/v1/attachments",
            params=self._compact_params({"expiresIn": expires_in}),
            files={
                "file": (file_name, file_bytes, content_type),
            },
        )

    async def create_attachment_from_url(self, payload: dict[str, Any]) -> httpx.Response:
        return await self._request(
            "POST",
            "/v1/attachments",
            json_body=payload,
        )

    async def get_attachment(self, attachment_id: str) -> httpx.Response:
        return await self._request("GET", f"/v1/attachments/{attachment_id}")

    async def get_attachment_download_url(self, attachment_id: str) -> httpx.Response:
        return await self._request("GET", f"/v1/attachments/{attachment_id}/download-url")

    async def delete_attachment(self, attachment_id: str) -> httpx.Response:
        return await self._request("DELETE", f"/v1/attachments/{attachment_id}")

    async def download_attachment_by_token(self, token: str, filename: str) -> httpx.Response:
        return await self._request(
            "GET",
            f"/v1/attachments/download/{token}/{filename}",
            include_api_key=False,
            include_authorization=False,
        )
