---
status: implemented
---

# Enhancement 088: Publish Python Packages to PyPI

> **Status**: Implemented — tested on [TestPyPI](https://test.pypi.org/project/memory-service-langchain/0.1.0/).

## Summary

Publish the `memory-service-langchain` Python package to PyPI via GitHub Actions trusted publishing so users can `pip install memory-service-langchain` instead of building local wheels.

## Motivation

The dev-setup docs previously required users to clone the repo, run `uv build`, and set `UV_FIND_LINKS`. Published packages remove this friction entirely.

## Problem

Two Python packages existed:

| Package | Source | Purpose |
|---------|--------|---------|
| `memory-service-langchain` | `python/langchain/` | LangChain/LangGraph checkpoint + history helpers, gRPC stubs, FastAPI middleware |
| `memory-service-langgraph` | `python/langgraph/` | LangGraph `BaseStore` for episodic memory |

PyPI blocks creation of a `memory-service-langgraph` trusted publisher (likely `langgraph` namespace protection by LangChain Inc.). Decision: **merge `memory-service-langgraph` into `memory-service-langchain`** — the langchain package already depends on `langgraph>=1.0.0`, so there is no extra dependency cost.

## What was implemented

### Phase 1: Merged `memory-service-langgraph` into `memory-service-langchain`

- Created `python/langchain/memory_service_langchain/langgraph/` sub-package with `store.py`, `async_store.py`, `indexing.py`, `transport.py`, and `__init__.py` re-exporting all public symbols.
- Deleted `python/langgraph/` entirely.
- Removed `langgraph` from `[tool.uv.workspace]` members in `python/pyproject.toml`.
- Updated imports: `from memory_service_langgraph import ...` → `from memory_service_langchain.langgraph import ...`.

Files changed:

| File | Change |
|------|--------|
| `python/examples/langgraph/doc-checkpoints/30-memories/app.py` | Updated import |
| `python/examples/langgraph/doc-checkpoints/30-memories/pyproject.toml` | Removed `memory-service-langgraph` dependency |
| `internal/sitebdd/checkpoint.go` | Removed `--reinstall-package memory-service-langgraph` |
| `internal/sitebdd/site_test.go` | Simplified `ensurePythonPackages` to single wheel build |
| `site/src/pages/docs/python-langgraph/memories.mdx` | Updated imports + explanation text |
| `site/src/pages/docs/python-langgraph/client-configuration.mdx` | Updated package name + import |
| `site/src/pages/docs/concepts/memories.md` | Updated package name + import |
| `python/FACTS.md` | Updated package layout description |
| Enhancement docs (069, 079, 081, 083, 068) | Updated paths for accuracy |

### Phase 2: Package metadata

Added to `python/langchain/pyproject.toml`:
- `license = {text = "Apache-2.0"}`
- `authors`
- `[project.urls]` (Homepage, Repository, Documentation)

### Phase 3: Release workflow

Created `.github/workflows/release.yml`:

```yaml
name: Release Python packages

on:
  release:
    types: [published]

jobs:
  publish-python:
    runs-on: ubuntu-latest
    environment: release
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
          packages-dir: python/dist/
```

Note: the uv workspace outputs wheels to `python/dist/` (not `python/langchain/dist/`), regardless of `working-directory`.

- Triggered on GitHub release creation
- gRPC stubs are committed — no generation step in CI
- OIDC trusted publishing (no API tokens)
- Versioning: manual bump in `pyproject.toml` before release

### Phase 4: Documentation updated

- Removed "Step 1 (Temporary): Build Local Package Wheel(s)" from both dev-setup pages.
- Removed 14 `UV_FIND_LINKS` prerequisite lines across all Python tutorial pages.
- Added `pip install` / `uv add` instructions to dev-setup pages.
