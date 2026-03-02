"""Helpers for building Memory Service episodic `index` payloads."""

from __future__ import annotations

from typing import Any, Callable

IndexMode = bool | list[str] | None
IndexBuilder = Callable[[tuple[str, ...], str, dict[str, Any], IndexMode], dict[str, str] | None]
IndexRedactor = Callable[[str, str, dict[str, Any]], str | None]


def build_index_payload(
    namespace: tuple[str, ...],
    key: str,
    value: dict[str, Any],
    index: IndexMode,
    *,
    redactor: IndexRedactor | None = None,
) -> dict[str, str]:
    """Build the `index` payload for PUT /v1/memories.

    Behavior:
    - `index is False`: disable indexing (`{}`)
    - `index is list[str]`: index only those dotted paths when they resolve to strings
    - otherwise (`None`/`True`): index all string leaves
    """
    # Unused by the default implementation, but part of the callback contract.
    _ = namespace
    _ = key

    if index is False:
        return {}

    candidates = _extract_fields(value, index)
    if redactor is None:
        return candidates

    out: dict[str, str] = {}
    for path, text in candidates.items():
        next_text = redactor(path, text, value)
        if next_text is not None:
            out[path] = next_text
    return out


def _extract_fields(value: dict[str, Any], index: IndexMode) -> dict[str, str]:
    if isinstance(index, list):
        out: dict[str, str] = {}
        for path in index:
            if not isinstance(path, str) or path == "":
                continue
            text = _lookup_string(value, path)
            if text is not None:
                out[path] = text
        return out

    out: dict[str, str] = {}
    _collect_string_leaves(value, prefix="", out=out)
    return out


def _collect_string_leaves(node: Any, prefix: str, out: dict[str, str]) -> None:
    if isinstance(node, str):
        if prefix:
            out[prefix] = node
        return
    if not isinstance(node, dict):
        return

    for key, child in node.items():
        if not isinstance(key, str) or key == "":
            continue
        child_prefix = key if prefix == "" else f"{prefix}.{key}"
        _collect_string_leaves(child, child_prefix, out)


def _lookup_string(value: dict[str, Any], path: str) -> str | None:
    node: Any = value
    for part in path.split("."):
        if not isinstance(node, dict):
            return None
        if part not in node:
            return None
        node = node[part]
    if isinstance(node, str):
        return node
    return None
