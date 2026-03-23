---
status: proposed
---

# Enhancement 088: Publish Python Packages to PyPI

> **Status**: Proposed — Option A (merge) accepted.

## Summary

Publish the `memory-service-langchain` Python package to PyPI via GitHub Actions trusted publishing so users can `pip install memory-service-langchain` instead of building local wheels.

## Motivation

The current dev-setup docs require users to clone the repo, run `uv build`, and set `UV_FIND_LINKS`. Published packages remove this friction entirely.

## Problem

Two Python packages exist today:

| Package | Source | Purpose |
|---------|--------|---------|
| `memory-service-langchain` | `python/langchain/` | LangChain/LangGraph checkpoint + history helpers, gRPC stubs, FastAPI middleware |
| `memory-service-langgraph` | `python/langgraph/` | LangGraph `BaseStore` for episodic memory |

PyPI blocks creation of a `memory-service-langgraph` trusted publisher (likely `langgraph` namespace protection by LangChain Inc.). Decision: **merge `memory-service-langgraph` into `memory-service-langchain`** — the langchain package already depends on `langgraph>=1.0.0`, so there is no extra dependency cost.

## Implementation Plan

### Phase 1: Merge `memory-service-langgraph` into `memory-service-langchain`

1. Create `python/langchain/memory_service_langchain/langgraph/` sub-package.
2. Copy the 4 source files (`store.py`, `async_store.py`, `indexing.py`, `transport.py`) from `python/langgraph/memory_service_langgraph/` into it.
3. Add `__init__.py` in the new sub-package that re-exports `MemoryServiceStore`, `AsyncMemoryServiceStore`, `IndexBuilder`, `IndexMode`, `IndexRedactor`, `build_index_payload`.
4. Remove `python/langgraph/` directory entirely.
5. Remove `langgraph` from the `[tool.uv.workspace]` members in `python/pyproject.toml`.

**Files to update** (imports `memory_service_langgraph` → `memory_service_langchain.langgraph`):

| File | Change |
|------|--------|
| `python/examples/langgraph/doc-checkpoints/30-memories/app.py` | Update import |
| `python/examples/langgraph/doc-checkpoints/30-memories/pyproject.toml` | Remove `memory-service-langgraph` dependency |
| `python/examples/langgraph/doc-checkpoints/30-memories/uv.lock` | Regenerate |
| `python/uv.lock` | Regenerate |
| `internal/sitebdd/checkpoint.go` | Remove `--reinstall-package memory-service-langgraph` |
| `internal/sitebdd/site_test.go` | Update comment about langgraph wheel |

**Docs to update** (references to the old package name):

| File | Change |
|------|--------|
| `site/src/pages/docs/python-langgraph/memories.mdx` | Update import + explanation text |
| `site/src/pages/docs/python-langgraph/client-configuration.mdx` | Update package name + import |
| `site/src/pages/docs/concepts/memories.md` | Update package name + import |

**Already-implemented enhancement docs** (update for accuracy, no functional impact):

| File |
|------|
| `docs/enhancements/implemented/069-policy-driven-episodic-indexing.md` |
| `docs/enhancements/implemented/079-client-unix-socket-support.md` |
| `docs/enhancements/implemented/081-rename-memory-channel-to-context.md` |
| `docs/enhancements/implemented/083-explicit-client-sdk-configuration.md` |
| `docs/enhancements/partial/068-namespaced-episodic-memory.md` |

**FACTS.md** to update:

| File |
|------|
| `python/FACTS.md` |

### Phase 2: Package metadata

Add to `python/langchain/pyproject.toml`:
- `license = {text = "Apache-2.0"}`
- `authors`
- `[project.urls]` (Homepage, Repository, Documentation)

### Phase 3: Release workflow

Create `.github/workflows/release.yml`:

```yaml
name: Release Python packages

on:
  release:
    types: [published]

jobs:
  publish-python:
    runs-on: ubuntu-latest
    permissions:
      id-token: write
    steps:
      - uses: actions/checkout@v4
      - uses: astral-sh/setup-uv@v4
      - name: Build wheel
        run: uv build
        working-directory: python/langchain
      - name: Publish to PyPI
        uses: pypa/gh-action-pypi-publish@release/v1
        with:
          packages-dir: python/langchain/dist/
```

- Triggered on GitHub release creation
- gRPC stubs are committed — no generation step in CI
- OIDC trusted publishing (no API tokens)
- Versioning: manual bump in `pyproject.toml` before release

### Phase 4: Update documentation

Remove "Step 1 (Temporary): Build Local Package Wheel(s)" from:
- `site/src/pages/docs/python-langchain/dev-setup.mdx`
- `site/src/pages/docs/python-langgraph/dev-setup.mdx`

Remove `UV_FIND_LINKS` references from user-facing doc pages. Keep them in `Taskfile.yml` dev targets (those intentionally use local builds).
